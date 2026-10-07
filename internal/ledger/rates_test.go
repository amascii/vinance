package ledger

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func ratesFor(entries map[string][]rateEntry) *Rates { return &Rates{byCurrency: entries} }

func TestRatesAt(t *testing.T) {
	r := ratesFor(map[string][]rateEntry{
		"MXN": {{"2026-01-15", "0.0560"}, {"2026-03-01", "0.0570"}, {"2026-06-10", "0.0580"}},
	})
	tests := []struct {
		date string
		want string
	}{
		{"2026-01-15", "0.0560"}, // exactly on a rate day
		{"2026-02-28", "0.0560"}, // between: the most recent earlier rate
		{"2026-03-01", "0.0570"},
		{"2026-12-31", "0.0580"}, // after the last: stays at the last
		{"2025-12-30", "0.0560"}, // before the first: the earliest
	}
	for _, tc := range tests {
		got, ok := r.At("MXN", tc.date)
		assert.True(t, ok)
		assert.Equal(t, tc.want, got, tc.date)
	}

	_, ok := r.At("JPY", "2026-01-01")
	assert.False(t, ok, "no rates for JPY")
	rate, ok := r.At("USD", "2026-01-01")
	assert.True(t, ok)
	assert.Equal(t, "", rate)
}

func TestRatesToUSD(t *testing.T) {
	r := ratesFor(map[string][]rateEntry{"MXN": {{"2026-01-15", "0.0562050599"}}})
	usd, ok := r.ToUSD(50000, "MXN", "2026-02-01")
	assert.True(t, ok)
	assert.EqualValues(t, 2810, usd)
	usd, ok = r.ToUSD(1234, "USD", "2026-02-01")
	assert.True(t, ok)
	assert.EqualValues(t, 1234, usd)
	_, ok = r.ToUSD(100, "KRW", "2026-02-01")
	assert.False(t, ok)
}
