package ledger

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func occurrences(s Schedule, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s.Occurrence(i)
	}
	return out
}

func TestScheduleWeekly(t *testing.T) {
	assert.Equal(t, []string{"2026-10-05", "2026-10-12", "2026-10-19", "2026-10-26", "2026-11-02"},
		occurrences(Schedule{Freq: Weekly, Every: 1, Anchor: "2026-10-05"}, 5))
	assert.Equal(t, []string{"2026-12-28", "2027-01-11", "2027-01-25"},
		occurrences(Schedule{Freq: Weekly, Every: 2, Anchor: "2026-12-28"}, 3), "every two weeks across a year boundary")
}

func TestScheduleMonthly(t *testing.T) {
	assert.Equal(t, []string{"2026-10-01", "2026-11-01", "2026-12-01", "2027-01-01"},
		occurrences(Schedule{Freq: Monthly, Every: 1, Anchor: "2026-10-01"}, 4))

	// The 31st clamps to short months and springs back: no drift.
	assert.Equal(t, []string{"2026-01-31", "2026-02-28", "2026-03-31", "2026-04-30", "2026-05-31", "2026-06-30"},
		occurrences(Schedule{Freq: Monthly, Every: 1, Anchor: "2026-01-31"}, 6))
	// Leap year February.
	assert.Equal(t, "2028-02-29", Schedule{Freq: Monthly, Every: 1, Anchor: "2028-01-31"}.Occurrence(1))
	assert.Equal(t, "2027-02-28", Schedule{Freq: Monthly, Every: 1, Anchor: "2027-01-30"}.Occurrence(1))

	assert.Equal(t, []string{"2026-01-15", "2026-04-15", "2026-07-15", "2026-10-15", "2027-01-15"},
		occurrences(Schedule{Freq: Monthly, Every: 3, Anchor: "2026-01-15"}, 5), "quarterly")
	assert.Equal(t, "2036-10-01", Schedule{Freq: Monthly, Every: 1, Anchor: "2026-10-01"}.Occurrence(120), "ten years out")
}

func TestScheduleYearly(t *testing.T) {
	assert.Equal(t, []string{"2026-03-05", "2027-03-05", "2028-03-05"},
		occurrences(Schedule{Freq: Yearly, Every: 1, Anchor: "2026-03-05"}, 3))
	// Feb 29 anchors fall back to Feb 28 in common years and return in leap years.
	assert.Equal(t, []string{"2028-02-29", "2029-02-28", "2030-02-28", "2031-02-28", "2032-02-29"},
		occurrences(Schedule{Freq: Yearly, Every: 1, Anchor: "2028-02-29"}, 5))
	assert.Equal(t, "2028-06-01", Schedule{Freq: Yearly, Every: 2, Anchor: "2026-06-01"}.Occurrence(1))
}

func TestScheduleOccurrencesAreStrictlyIncreasing(t *testing.T) {
	for _, s := range []Schedule{
		{Freq: Weekly, Every: 1, Anchor: "2026-01-01"}, {Freq: Monthly, Every: 1, Anchor: "2026-01-31"},
		{Freq: Monthly, Every: 12, Anchor: "2028-02-29"}, {Freq: Yearly, Every: 1, Anchor: "2028-02-29"},
	} {
		prev := ""
		for k := 0; k < 60; k++ {
			d := s.Occurrence(k)
			assert.Greater(t, d, prev, "%+v k=%d", s, k)
			prev = d
		}
	}
}

func TestScheduleValidate(t *testing.T) {
	ok := Schedule{Freq: Monthly, Every: 1, Anchor: "2026-10-01"}
	assert.NoError(t, ok.Validate())
	ok.End = "2026-10-01"
	assert.NoError(t, ok.Validate(), "ending on the start date is a one-off")

	bad := []struct {
		name string
		s    Schedule
		want string
	}{
		{"freq", Schedule{Freq: "daily", Every: 1, Anchor: "2026-10-01"}, "frequency"},
		{"every zero", Schedule{Freq: Monthly, Every: 0, Anchor: "2026-10-01"}, "between 1 and 99"},
		{"every huge", Schedule{Freq: Monthly, Every: 100, Anchor: "2026-10-01"}, "between 1 and 99"},
		{"anchor", Schedule{Freq: Monthly, Every: 1, Anchor: "10/01/2026"}, "start date"},
		{"anchor impossible", Schedule{Freq: Monthly, Every: 1, Anchor: "2026-02-30"}, "start date"},
		{"end", Schedule{Freq: Monthly, Every: 1, Anchor: "2026-10-01", End: "soon"}, "end date must look"},
		{"end before start", Schedule{Freq: Monthly, Every: 1, Anchor: "2026-10-01", End: "2026-09-30"}, "before the start"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.s.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestScheduleWithin(t *testing.T) {
	s := Schedule{Freq: Monthly, Every: 1, Anchor: "2026-01-01", End: "2026-03-15"}
	assert.True(t, s.Within("2026-03-15"), "the end date is inclusive")
	assert.False(t, s.Within("2026-04-01"))
	assert.True(t, Schedule{}.Within("2099-01-01"), "no end means never")
}

func TestScheduleDescribe(t *testing.T) {
	tests := []struct {
		s    Schedule
		want string
	}{
		{Schedule{Freq: Monthly, Every: 1, Anchor: "2026-10-01"}, "Monthly on the 1st"},
		{Schedule{Freq: Monthly, Every: 1, Anchor: "2026-10-02"}, "Monthly on the 2nd"},
		{Schedule{Freq: Monthly, Every: 1, Anchor: "2026-10-03"}, "Monthly on the 3rd"},
		{Schedule{Freq: Monthly, Every: 1, Anchor: "2026-10-11"}, "Monthly on the 11th"},
		{Schedule{Freq: Monthly, Every: 1, Anchor: "2026-10-22"}, "Monthly on the 22nd"},
		{Schedule{Freq: Monthly, Every: 3, Anchor: "2026-10-31"}, "Every 3 months on the 31st"},
		{Schedule{Freq: Weekly, Every: 1, Anchor: "2026-10-05"}, "Weekly on Mondays"},
		{Schedule{Freq: Weekly, Every: 2, Anchor: "2026-10-09"}, "Every 2 weeks on Fridays"},
		{Schedule{Freq: Yearly, Every: 1, Anchor: "2026-03-05"}, "Yearly on Mar 5"},
		{Schedule{Freq: Yearly, Every: 2, Anchor: "2026-03-05"}, "Every 2 years on Mar 5"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, tc.s.Describe(), "%+v", tc.s)
	}
}
