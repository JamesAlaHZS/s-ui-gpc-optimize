package trafficreview

import (
	"net/url"
	"testing"
	"time"
)

func TestLosAngelesBillingDaysUseCivilMidnights(t *testing.T) {
	now := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		day      string
		from, to time.Time
		hours    int64
	}{
		{"2026-10-08", time.Date(2026, 10, 8, 7, 0, 0, 0, time.UTC), time.Date(2026, 10, 9, 7, 0, 0, 0, time.UTC), 24},
		{"2026-03-08", time.Date(2026, 3, 8, 8, 0, 0, 0, time.UTC), time.Date(2026, 3, 9, 7, 0, 0, 0, time.UTC), 23},
		{"2026-11-01", time.Date(2026, 11, 1, 7, 0, 0, 0, time.UTC), time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC), 25},
	}
	for _, tc := range cases {
		t.Run(tc.day, func(t *testing.T) {
			r, err := ParseRange(url.Values{"date": {tc.day}, "timezone": {"America/Los_Angeles"}}, now)
			if err != nil {
				t.Fatal(err)
			}
			if r.From != tc.from.Unix() || r.To != tc.to.Unix() || r.To-r.From != tc.hours*3600 {
				t.Fatalf("LA day %s: got %d..%d (%dh), want %d..%d (%dh)", tc.day, r.From, r.To, (r.To-r.From)/3600, tc.from.Unix(), tc.to.Unix(), tc.hours)
			}
		})
	}
}

func TestRangeAndLimitValidation(t *testing.T) {
	now := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	for _, q := range []url.Values{
		{"timezone": {"Mars/Olympus"}},
		{"date": {"2026-10-08"}, "from": {"1"}},
		{"date": {"2026-02-30"}},
		{"from": {"200"}, "to": {"100"}},
		{"from": {"1"}, "to": {"94694402"}},
		{"limit": {"0"}},
		{"limit": {"501"}},
		{"limit": {"1;DROP TABLE review_alerts"}},
		{"from": {"0 OR 1=1"}},
	} {
		if _, err := ParseRange(q, now); err == nil {
			t.Errorf("accepted invalid query: %v", q)
		}
	}
	r, err := ParseRange(url.Values{"limit": {"500"}, "timezone": {"Asia/Shanghai"}}, now)
	if err != nil || r.Limit != 500 || r.Timezone != "Asia/Shanghai" {
		t.Fatalf("valid range rejected: %+v, %v", r, err)
	}
}

func TestDateRangeInclusiveEndCalendarYearsAndDST(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		start, end, zone string
		hours            int64
	}{
		{"2023-01-01", "2025-12-31", "UTC", (365 + 366 + 365) * 24},
		{"2024-02-29", "2027-02-28", "UTC", (366 + 365 + 365) * 24},
		{"2026-03-07", "2026-03-09", "America/Los_Angeles", 71},
		{"2026-10-31", "2026-11-02", "America/Los_Angeles", 73},
		{"2026-10-18", "2026-10-18", "Asia/Shanghai", 24},
	} {
		r, err := ParseRange(url.Values{"startDate": {tc.start}, "endDate": {tc.end}, "timezone": {tc.zone}}, now)
		if err != nil || r.To-r.From != tc.hours*3600 {
			t.Fatalf("range %s..%s (%s): %+v, %v", tc.start, tc.end, tc.zone, r, err)
		}
		loc, _ := time.LoadLocation(tc.zone)
		end, _ := time.ParseInLocation("2006-01-02", tc.end, loc)
		if r.To != end.AddDate(0, 0, 1).Unix() {
			t.Fatalf("end date was not inclusive: %+v", r)
		}
	}
	for _, query := range []url.Values{
		{"startDate": {"2023-01-01"}, "endDate": {"2026-01-01"}},
		{"startDate": {"2024-02-29"}, "endDate": {"2027-03-01"}},
		{"startDate": {"2026-10-18"}},
		{"endDate": {"2026-10-18"}},
		{"startDate": {"2026-10-18"}, "endDate": {"2026-10-17"}},
		{"startDate": {"2026-10-18"}, "endDate": {"2026-10-19"}, "date": {"2026-10-18"}},
		{"startDate": {"2026-10-18"}, "endDate": {"2026-10-19"}, "from": {"1"}},
		{"startDate": {"2026-10-18"}, "endDate": {""}},
		{"startDate": {"2026-10-18", "2026-10-17"}, "endDate": {"2026-10-19"}},
		{"date": {"2026-10-18"}, "from": {""}},
		{"from": {"9223372036854775806"}, "to": {"9223372036854775807"}},
	} {
		if r, err := ParseRange(query, now); err == nil {
			t.Fatalf("accepted invalid range %v: %+v", query, r)
		}
	}
}
