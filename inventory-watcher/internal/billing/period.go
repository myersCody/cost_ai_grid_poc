package billing

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ResolvePeriod returns the start and end of the billing window that
// contains the reference time.
//
// Supported period values:
//   - "monthly"  — calendar month (1st 00:00 UTC → 1st of next month)
//   - "weekly"   — ISO week (Monday 00:00 UTC → next Monday)
//   - "daily"    — calendar day (00:00 UTC → next 00:00 UTC)
//   - "Nd"       — N-day slots anchored to the 1st of the month.
//     If N doesn't divide the month length evenly, the last slot is shorter.
//   - "Nh"       — N-hour slots anchored to midnight UTC.
//     If N doesn't divide 24 evenly, the last slot of the day is shorter.
func ResolvePeriod(period string, ref time.Time) (start, end time.Time, err error) {
	ref = ref.UTC()

	switch period {
	case "monthly", "":
		start = time.Date(ref.Year(), ref.Month(), 1, 0, 0, 0, 0, time.UTC)
		end = start.AddDate(0, 1, 0)
		return start, end, nil

	case "weekly":
		weekday := ref.Weekday()
		if weekday == time.Sunday {
			weekday = 7
		}
		daysSinceMonday := int(weekday) - 1
		start = time.Date(ref.Year(), ref.Month(), ref.Day()-daysSinceMonday, 0, 0, 0, 0, time.UTC)
		end = start.AddDate(0, 0, 7)
		return start, end, nil

	case "daily":
		start = time.Date(ref.Year(), ref.Month(), ref.Day(), 0, 0, 0, 0, time.UTC)
		end = start.AddDate(0, 0, 1)
		return start, end, nil
	}

	if days, ok := parseDayDuration(period); ok {
		if days <= 0 || days > 31 {
			return time.Time{}, time.Time{}, fmt.Errorf("day duration must be 1-31, got %d", days)
		}
		monthStart := time.Date(ref.Year(), ref.Month(), 1, 0, 0, 0, 0, time.UTC)
		nextMonth := monthStart.AddDate(0, 1, 0)
		dayOfMonth := ref.Day() - 1 // 0-based
		slotStart := (dayOfMonth / days) * days
		start = monthStart.AddDate(0, 0, slotStart)
		end = monthStart.AddDate(0, 0, slotStart+days)
		if end.After(nextMonth) {
			end = nextMonth
		}
		return start, end, nil
	}

	if hours, ok := parseHourDuration(period); ok {
		if hours <= 0 || hours > 24 {
			return time.Time{}, time.Time{}, fmt.Errorf("hour duration must be 1-24, got %d", hours)
		}
		midnight := time.Date(ref.Year(), ref.Month(), ref.Day(), 0, 0, 0, 0, time.UTC)
		hourOfDay := ref.Hour()
		slotStart := (hourOfDay / hours) * hours
		start = midnight.Add(time.Duration(slotStart) * time.Hour)
		end = midnight.Add(time.Duration(slotStart+hours) * time.Hour)
		nextMidnight := midnight.AddDate(0, 0, 1)
		if end.After(nextMidnight) {
			end = nextMidnight
		}
		return start, end, nil
	}

	return time.Time{}, time.Time{}, fmt.Errorf("unsupported period: %q", period)
}

// PeriodLabel returns a human-readable label identifying the billing
// window that contains the reference time. Used as a dedup key for
// alerts and as a display label in quota status responses.
func PeriodLabel(period string, ref time.Time) string {
	start, end, err := ResolvePeriod(period, ref)
	if err != nil {
		return ref.UTC().Format("2006-01")
	}

	switch period {
	case "monthly", "":
		return start.Format("2006-01")
	case "weekly":
		return start.Format("2006-W") + fmt.Sprintf("%02d", isoWeek(start))
	case "daily":
		return start.Format("2006-01-02")
	default:
		if _, ok := parseDayDuration(period); ok {
			return start.Format("2006-01-02") + "/" + end.Add(-24*time.Hour).Format("2006-01-02")
		}
		return start.Format("2006-01-02") + "/" + start.Format("15:04") + "-" + end.Format("15:04")
	}
}

func parseDayDuration(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasSuffix(s, "d") {
		return 0, false
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil {
		return 0, false
	}
	return n, true
}

func parseHourDuration(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasSuffix(s, "h") {
		return 0, false
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil {
		return 0, false
	}
	return n, true
}

func isoWeek(t time.Time) int {
	_, week := t.ISOWeek()
	return week
}
