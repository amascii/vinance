package ledger

import (
	"context"
	"sort"
)

// rateEntry is one known exchange rate.
type rateEntry struct {
	date string
	rate string // USD per one whole unit
}

// Rates answers "what was one unit of X worth in USD on a given day?" from the prices table.
// For a date it uses the most recent rate on or before it, falling back to the earliest later
// one when the date precedes every known rate. USD is always 1.
type Rates struct {
	byCurrency map[string][]rateEntry // each slice sorted by date ascending
}

// LoadRates reads every price into memory (the table is small: one row per currency per day
// a cross-currency transaction was entered).
func (s *Service) LoadRates(ctx context.Context) (*Rates, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT currency, date, usd_per_unit FROM prices ORDER BY currency, date")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	r := &Rates{byCurrency: map[string][]rateEntry{}}
	for rows.Next() {
		var cur string
		var e rateEntry
		if err := rows.Scan(&cur, &e.date, &e.rate); err != nil {
			return nil, err
		}
		r.byCurrency[cur] = append(r.byCurrency[cur], e)
	}
	return r, rows.Err()
}

// At returns the rate to use for cur on date. ok is false when there are no rates for cur.
// The returned rate is "" for USD.
func (r *Rates) At(cur, date string) (rate string, ok bool) {
	if cur == "USD" {
		return "", true
	}
	list := r.byCurrency[cur]
	if len(list) == 0 {
		return "", false
	}
	// first index whose date is after `date`
	i := sort.Search(len(list), func(i int) bool { return list[i].date > date })
	if i == 0 {
		return list[0].rate, true // before every known rate: use the earliest
	}
	return list[i-1].rate, true
}

// ToUSD converts minor units of cur on date. ok is false when no rate is known.
func (r *Rates) ToUSD(minor int64, cur, date string) (usd int64, ok bool) {
	rate, ok := r.At(cur, date)
	if !ok {
		return 0, false
	}
	usd, err := ConvertToUSD(minor, cur, rate)
	return usd, err == nil
}
