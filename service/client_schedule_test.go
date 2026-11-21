package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/alireza0/s-ui/database/model"
)

func TestMonthlyResetArmsAndKeepsCalendarBoundary(t *testing.T) {
	db := clientTestDB(t)
	s := &ClientService{}
	now := time.Date(2026, time.November, 21, 6, 0, 0, 0, time.UTC)
	client := createClient(t, db, &model.Client{
		Name: "monthly", Enable: true, AutoReset: true, ResetDays: 1, Up: 123,
		NextReset: now.Unix() + 86400,
	})
	client.ResetMonthlyTime, client.ResetTimezone = "00:00", "Asia/Shanghai"
	if err := s.prepareClientReset(db, client, true, now, clientResetUpdate{}); err != nil {
		t.Fatal(err)
	}
	december := time.Date(2026, time.November, 30, 16, 0, 0, 0, time.UTC)
	if client.NextReset != december.Unix() || client.Up != 123 {
		t.Fatalf("arming reset changed the usage or selected the wrong boundary: %+v", client)
	}
	if err := db.Save(client).Error; err != nil {
		t.Fatal(err)
	}
	client.NextReset = 0 // An open panel can send a stale timestamp.
	if err := s.prepareClientReset(db, client, true, december.Add(time.Hour), clientResetUpdate{}); err != nil {
		t.Fatal(err)
	}
	if client.NextReset != december.Unix() {
		t.Fatal("an unrelated edit must preserve a due monthly reset")
	}
	if _, err := s.ResetClients(db, december.Unix()); err != nil {
		t.Fatal(err)
	}
	next := time.Date(2026, time.December, 31, 16, 0, 0, 0, time.UTC).Unix()
	got := reload(t, db, client.Id)
	if got.NextReset != next || got.Up != 0 || got.TotalUp != 123 {
		t.Fatalf("unexpected result after monthly reset: %+v", got)
	}
}

func TestMonthlyResetUsesSelectedZoneOffset(t *testing.T) {
	now := time.Date(2026, time.March, 1, 9, 30, 0, 0, time.UTC)
	got, err := nextMonthlyReset(now, "01:30", "America/Los_Angeles")
	want := time.Date(2026, time.April, 1, 8, 30, 0, 0, time.UTC)
	if err != nil || !got.Equal(want) {
		t.Fatalf("next reset = %v (%v), want %v", got, err, want)
	}
}

func TestManualResetTakesPrecedenceAndResumesMonthly(t *testing.T) {
	db := clientTestDB(t)
	s := &ClientService{}
	monthly := time.Date(2026, time.December, 1, 0, 0, 0, 0, time.UTC)
	manual := monthly.Add(4 * 24 * time.Hour)
	client := createClient(t, db, &model.Client{
		Name: "monthly-override", Enable: true, AutoReset: true, Up: 123,
		NextReset: monthly.Unix(), ResetMonthlyTime: "00:00", ResetTimezone: "UTC",
		ManualResetAt: manual.Unix(),
	})
	if _, err := s.ResetClients(db, monthly.Unix()); err != nil {
		t.Fatal(err)
	}
	if got := reload(t, db, client.Id); got.Up != 123 || got.ManualResetAt != manual.Unix() {
		t.Fatalf("automatic reset interfered with the manual date: %+v", got)
	}
	if _, err := s.ResetClients(db, manual.Unix()); err != nil {
		t.Fatal(err)
	}
	next := monthly.AddDate(0, 1, 0).Unix()
	if got := reload(t, db, client.Id); got.Up != 0 || got.TotalUp != 123 || got.ManualResetAt != 0 || got.NextReset != next {
		t.Fatalf("manual reset did not resume the calendar schedule: %+v", got)
	}
}

func TestSaveAndConsumeManualResetWithoutAutomaticReset(t *testing.T) {
	db := clientTestDB(t)
	s := &ClientService{}
	client := createClient(t, db, &model.Client{Name: "one-time", Enable: true, Up: 123})
	manual := time.Now().Add(2 * time.Hour).Unix()
	save := func(override *int64) error {
		t.Helper()
		body, err := json.Marshal(struct {
			model.Client
			ManualResetAt *int64 `json:"manualResetAt,omitempty"`
		}{*client, override})
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.Save(db, "edit", body, "localhost")
		return err
	}
	if err := save(&manual); err != nil {
		t.Fatal(err)
	}
	if err := save(nil); err != nil {
		t.Fatal(err)
	}
	if got := reload(t, db, client.Id); got.ManualResetAt != manual || got.Up != 123 {
		t.Fatalf("an unrelated edit changed a saved manual reset: %+v", got)
	}
	zero, past := int64(0), time.Now().Add(-time.Hour).Unix()
	if err := save(&past); err == nil {
		t.Fatal("accepted a reset date in the past")
	}
	if err := save(&zero); err != nil {
		t.Fatal(err)
	}
	if got := reload(t, db, client.Id); got.ManualResetAt != 0 || got.Up != 123 {
		t.Fatalf("clearing the date did not preserve usage: %+v", got)
	}
	if err := save(&manual); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResetClients(db, manual); err != nil {
		t.Fatal(err)
	}
	if got := reload(t, db, client.Id); got.Up != 0 || got.TotalUp != 123 || got.ManualResetAt != 0 || got.AutoReset || got.NextReset != 0 {
		t.Fatalf("unexpected result after one-time reset: %+v", got)
	}
	if err := db.Model(client).Update("up", 45).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResetClients(db, manual+60); err != nil {
		t.Fatal(err)
	}
	if got := reload(t, db, client.Id); got.Up != 45 || got.TotalUp != 123 {
		t.Fatalf("one-time reset ran again: %+v", got)
	}
}
