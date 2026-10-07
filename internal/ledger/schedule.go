package ledger

import (
	"fmt"
	"time"
)

// Freq is how often a recurring rule repeats.
type Freq string

const (
	Weekly  Freq = "weekly"
	Monthly Freq = "monthly"
	Yearly  Freq = "yearly"
)

// Schedule describes a repeating series of dates: every `Every` weeks/months/years from Anchor.
// Occurrences are numbered from 0 (the anchor itself). Monthly and yearly series keep the
// anchor's day and clamp to shorter months, so "the 31st" falls on Feb 28, then Mar 31 again;
// clamping never drifts because every occurrence is computed from the anchor.
type Schedule struct {
	Freq   Freq
	Every  int    // >= 1
	Anchor string // YYYY-MM-DD, occurrence 0
	End    string // optional inclusive last date
}

// Validate checks the schedule's fields.
func (s Schedule) Validate() error {
	switch s.Freq {
	case Weekly, Monthly, Yearly:
	default:
		return fmt.Errorf("frequency must be weekly, monthly or yearly")
	}
	if s.Every < 1 || s.Every > 99 {
		return fmt.Errorf("\"every\" must be between 1 and 99")
	}
	anchor, err := time.Parse("2006-01-02", s.Anchor)
	if err != nil {
		return fmt.Errorf("the start date must look like 2026-10-31")
	}
	if s.End != "" {
		end, err := time.Parse("2006-01-02", s.End)
		if err != nil {
			return fmt.Errorf("the end date must look like 2026-12-31")
		}
		if end.Before(anchor) {
			return fmt.Errorf("the end date is before the start date")
		}
	}
	return nil
}

// Occurrence returns the date of occurrence k (k >= 0). The schedule must be valid.
func (s Schedule) Occurrence(k int) string {
	a, _ := time.Parse("2006-01-02", s.Anchor)
	switch s.Freq {
	case Weekly:
		return a.AddDate(0, 0, 7*s.Every*k).Format("2006-01-02")
	case Monthly:
		return addMonthsClamped(a, s.Every*k).Format("2006-01-02")
	default: // Yearly
		return addMonthsClamped(a, 12*s.Every*k).Format("2006-01-02")
	}
}

// addMonthsClamped adds months to t keeping t's day of month, clamped to the target month's length.
func addMonthsClamped(t time.Time, months int) time.Time {
	total := t.Year()*12 + int(t.Month()) - 1 + months
	year, month := total/12, time.Month(total%12+1)
	day := t.Day()
	if last := daysIn(year, month); day > last {
		day = last
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func daysIn(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// Within reports whether date is on or before the schedule's end (always true without one).
func (s Schedule) Within(date string) bool { return s.End == "" || date <= s.End }

// Describe renders the schedule for people: "Monthly on the 1st", "Every 2 weeks on Mondays",
// "Yearly on Mar 5".
func (s Schedule) Describe() string {
	a, err := time.Parse("2006-01-02", s.Anchor)
	if err != nil {
		return ""
	}
	every := func(unit, plural string) string {
		if s.Every == 1 {
			return ""
		}
		return fmt.Sprintf("Every %d %s", s.Every, plural)
	}
	switch s.Freq {
	case Weekly:
		if s.Every == 1 {
			return "Weekly on " + a.Weekday().String() + "s"
		}
		return every("week", "weeks") + " on " + a.Weekday().String() + "s"
	case Monthly:
		if s.Every == 1 {
			return "Monthly on the " + ordinal(a.Day())
		}
		return every("month", "months") + " on the " + ordinal(a.Day())
	default:
		if s.Every == 1 {
			return "Yearly on " + a.Format("Jan 2")
		}
		return every("year", "years") + " on " + a.Format("Jan 2")
	}
}

func ordinal(n int) string {
	suffix := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return fmt.Sprintf("%d%s", n, suffix)
}
