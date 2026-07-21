package billing

import (
	"testing"
	"time"
)

func ts(s string) time.Time {
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return parsed
}

func TestResolvePeriod_Monthly(t *testing.T) {
	start, end, err := ResolvePeriod("monthly", ts("2026-07-15T14:30:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if start != ts("2026-07-01T00:00:00Z") {
		t.Errorf("start: got %v, want 2026-07-01", start)
	}
	if end != ts("2026-08-01T00:00:00Z") {
		t.Errorf("end: got %v, want 2026-08-01", end)
	}
}

func TestResolvePeriod_MonthlyEmpty(t *testing.T) {
	start, end, err := ResolvePeriod("", ts("2026-07-15T14:30:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if start != ts("2026-07-01T00:00:00Z") {
		t.Errorf("empty string should default to monthly")
	}
	if end != ts("2026-08-01T00:00:00Z") {
		t.Errorf("end: got %v", end)
	}
}

func TestResolvePeriod_Weekly(t *testing.T) {
	// 2026-07-15 is a Wednesday
	start, end, err := ResolvePeriod("weekly", ts("2026-07-15T14:30:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if start != ts("2026-07-13T00:00:00Z") {
		t.Errorf("start: got %v, want Monday 2026-07-13", start)
	}
	if end != ts("2026-07-20T00:00:00Z") {
		t.Errorf("end: got %v, want Monday 2026-07-20", end)
	}
}

func TestResolvePeriod_WeeklyOnMonday(t *testing.T) {
	start, _, err := ResolvePeriod("weekly", ts("2026-07-13T00:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if start != ts("2026-07-13T00:00:00Z") {
		t.Errorf("Monday at midnight should be its own week start: got %v", start)
	}
}

func TestResolvePeriod_WeeklyOnSunday(t *testing.T) {
	// 2026-07-19 is a Sunday
	start, _, err := ResolvePeriod("weekly", ts("2026-07-19T14:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if start != ts("2026-07-13T00:00:00Z") {
		t.Errorf("Sunday should be in the Monday-starting week: got %v", start)
	}
}

func TestResolvePeriod_Daily(t *testing.T) {
	start, end, err := ResolvePeriod("daily", ts("2026-07-20T14:30:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if start != ts("2026-07-20T00:00:00Z") {
		t.Errorf("start: got %v", start)
	}
	if end != ts("2026-07-21T00:00:00Z") {
		t.Errorf("end: got %v", end)
	}
}

func TestResolvePeriod_8h(t *testing.T) {
	tests := []struct {
		ref   string
		start string
		end   string
	}{
		{"2026-07-20T03:00:00Z", "2026-07-20T00:00:00Z", "2026-07-20T08:00:00Z"},
		{"2026-07-20T10:00:00Z", "2026-07-20T08:00:00Z", "2026-07-20T16:00:00Z"},
		{"2026-07-20T20:00:00Z", "2026-07-20T16:00:00Z", "2026-07-21T00:00:00Z"},
	}
	for _, tc := range tests {
		start, end, err := ResolvePeriod("8h", ts(tc.ref))
		if err != nil {
			t.Fatal(err)
		}
		if start != ts(tc.start) {
			t.Errorf("ref=%s: start got %v, want %v", tc.ref, start, tc.start)
		}
		if end != ts(tc.end) {
			t.Errorf("ref=%s: end got %v, want %v", tc.ref, end, tc.end)
		}
	}
}

func TestResolvePeriod_5h_LastWindowTruncated(t *testing.T) {
	// 5h doesn't divide 24 evenly. Last window: 20:00-00:00 (4h, not 5h)
	start, end, err := ResolvePeriod("5h", ts("2026-07-20T22:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if start != ts("2026-07-20T20:00:00Z") {
		t.Errorf("start: got %v, want 20:00", start)
	}
	if end != ts("2026-07-21T00:00:00Z") {
		t.Errorf("end: got %v, want midnight (truncated to 4h)", end)
	}
}

func TestResolvePeriod_5h_NormalWindow(t *testing.T) {
	start, end, err := ResolvePeriod("5h", ts("2026-07-20T07:30:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if start != ts("2026-07-20T05:00:00Z") {
		t.Errorf("start: got %v, want 05:00", start)
	}
	if end != ts("2026-07-20T10:00:00Z") {
		t.Errorf("end: got %v, want 10:00", end)
	}
}

func TestResolvePeriod_1h(t *testing.T) {
	start, end, err := ResolvePeriod("1h", ts("2026-07-20T14:45:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if start != ts("2026-07-20T14:00:00Z") {
		t.Errorf("start: got %v", start)
	}
	if end != ts("2026-07-20T15:00:00Z") {
		t.Errorf("end: got %v", end)
	}
}

func TestResolvePeriod_24h(t *testing.T) {
	// 24h should behave identically to daily
	start, end, err := ResolvePeriod("24h", ts("2026-07-20T14:30:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if start != ts("2026-07-20T00:00:00Z") {
		t.Errorf("24h should equal daily: start got %v", start)
	}
	if end != ts("2026-07-21T00:00:00Z") {
		t.Errorf("24h should equal daily: end got %v", end)
	}
}

func TestResolvePeriod_7d(t *testing.T) {
	// July has 31 days. 7d slots: 1-7, 8-14, 15-21, 22-28, 29-31 (3-day stub)
	tests := []struct {
		ref   string
		start string
		end   string
	}{
		{"2026-07-03T12:00:00Z", "2026-07-01T00:00:00Z", "2026-07-08T00:00:00Z"},
		{"2026-07-10T12:00:00Z", "2026-07-08T00:00:00Z", "2026-07-15T00:00:00Z"},
		{"2026-07-20T12:00:00Z", "2026-07-15T00:00:00Z", "2026-07-22T00:00:00Z"},
		{"2026-07-25T12:00:00Z", "2026-07-22T00:00:00Z", "2026-07-29T00:00:00Z"},
	}
	for _, tc := range tests {
		start, end, err := ResolvePeriod("7d", ts(tc.ref))
		if err != nil {
			t.Fatalf("ref=%s: %v", tc.ref, err)
		}
		if start != ts(tc.start) {
			t.Errorf("ref=%s: start got %v, want %v", tc.ref, start, tc.start)
		}
		if end != ts(tc.end) {
			t.Errorf("ref=%s: end got %v, want %v", tc.ref, end, tc.end)
		}
	}
}

func TestResolvePeriod_7d_LastSlotTruncated(t *testing.T) {
	// July 30 falls in the last slot: 29-31 (3 days, not 7)
	start, end, err := ResolvePeriod("7d", ts("2026-07-30T12:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if start != ts("2026-07-29T00:00:00Z") {
		t.Errorf("start: got %v, want Jul 29", start)
	}
	if end != ts("2026-08-01T00:00:00Z") {
		t.Errorf("end: got %v, want Aug 1 (truncated to month end)", end)
	}
}

func TestResolvePeriod_10d(t *testing.T) {
	// July: 10d slots: 1-10, 11-20, 21-30, 31 (1-day stub)
	start, end, err := ResolvePeriod("10d", ts("2026-07-15T12:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if start != ts("2026-07-11T00:00:00Z") {
		t.Errorf("start: got %v, want Jul 11", start)
	}
	if end != ts("2026-07-21T00:00:00Z") {
		t.Errorf("end: got %v, want Jul 21", end)
	}
}

func TestResolvePeriod_1d_SameAsDaily(t *testing.T) {
	start, end, err := ResolvePeriod("1d", ts("2026-07-20T14:30:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	startDaily, endDaily, _ := ResolvePeriod("daily", ts("2026-07-20T14:30:00Z"))
	if start != startDaily || end != endDaily {
		t.Errorf("1d should equal daily: got %v-%v, want %v-%v", start, end, startDaily, endDaily)
	}
}

func TestResolvePeriod_0d_Error(t *testing.T) {
	_, _, err := ResolvePeriod("0d", ts("2026-07-20T14:00:00Z"))
	if err == nil {
		t.Error("expected error for 0d")
	}
}

func TestPeriodLabel_7d(t *testing.T) {
	label := PeriodLabel("7d", ts("2026-07-10T12:00:00Z"))
	if label != "2026-07-08/2026-07-14" {
		t.Errorf("got %q, want 2026-07-08/2026-07-14", label)
	}
}

func TestResolvePeriod_InvalidPeriod(t *testing.T) {
	_, _, err := ResolvePeriod("banana", ts("2026-07-20T14:00:00Z"))
	if err == nil {
		t.Error("expected error for invalid period")
	}
}

func TestResolvePeriod_ZeroHours(t *testing.T) {
	_, _, err := ResolvePeriod("0h", ts("2026-07-20T14:00:00Z"))
	if err == nil {
		t.Error("expected error for 0h")
	}
}

func TestPeriodLabel_Monthly(t *testing.T) {
	label := PeriodLabel("monthly", ts("2026-07-15T14:30:00Z"))
	if label != "2026-07" {
		t.Errorf("got %q, want 2026-07", label)
	}
}

func TestPeriodLabel_Daily(t *testing.T) {
	label := PeriodLabel("daily", ts("2026-07-20T14:30:00Z"))
	if label != "2026-07-20" {
		t.Errorf("got %q, want 2026-07-20", label)
	}
}

func TestPeriodLabel_HourWindow(t *testing.T) {
	label := PeriodLabel("5h", ts("2026-07-20T07:30:00Z"))
	if label != "2026-07-20/05:00-10:00" {
		t.Errorf("got %q, want 2026-07-20/05:00-10:00", label)
	}
}
