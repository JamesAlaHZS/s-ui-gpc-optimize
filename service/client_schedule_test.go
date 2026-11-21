package service

import (
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
	if err := s.prepareClientReset(db, client, true, now); err != nil {
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
	if err := s.prepareClientReset(db, client, true, december.Add(time.Hour)); err != nil {
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
