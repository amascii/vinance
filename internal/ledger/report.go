package ledger

import (
	"context"
	"fmt"
	"sort"
)

// CurrencyAmount is an amount of one currency, in minor units.
type CurrencyAmount struct {
	Currency string
	Amount   int64
}

// TagLine is one row of the tag report.
type TagLine struct {
	Tag        string // empty for the Untagged and Total lines
	Splits     int    // how many lines (splits) contributed
	USD        int64  // converted at the rate in effect on each transaction's date
	ByCurrency []CurrencyAmount
}

// TagReport totals spending (or income) by tag over a date range.
//
// A split with several tags counts toward each of them, so the tag lines overlap and do not
// sum to Total. Untagged covers splits with no tag; Total counts every split exactly once.
type TagReport struct {
	Lines    []TagLine // by USD descending, then tag
	Untagged TagLine
	Total    TagLine
	Missing  []string // currencies with no exchange rate: left out of every USD figure
}

// ReportFilter selects what the report covers.
type ReportFilter struct {
	Income   bool   // report income instead of spending
	From, To string // inclusive YYYY-MM-DD bounds ("" = open)
}

// accumulator builds one TagLine.
type accumulator struct {
	splits int
	usd    int64
	by     map[string]int64
}

func newAcc() *accumulator { return &accumulator{by: map[string]int64{}} }

func (a *accumulator) add(cur string, amount, usd int64) {
	a.splits++
	a.usd += usd
	a.by[cur] += amount
}

func (a *accumulator) line(tag string) TagLine {
	l := TagLine{Tag: tag, Splits: a.splits, USD: a.usd}
	for cur, amt := range a.by {
		l.ByCurrency = append(l.ByCurrency, CurrencyAmount{cur, amt})
	}
	sort.Slice(l.ByCurrency, func(i, j int) bool { return l.ByCurrency[i].Currency < l.ByCurrency[j].Currency })
	return l
}

// TagReport aggregates the built-in Expenses (or Income) account's splits by tag. Amounts are
// reported positive either way (income splits are credits, so their sign is flipped).
func (s *Service) TagReport(ctx context.Context, f ReportFilter) (TagReport, error) {
	rates, err := s.LoadRates(ctx)
	if err != nil {
		return TagReport{}, err
	}
	accType, sign := "expense", int64(1)
	if f.Income {
		accType, sign = "income", -1
	}
	q := `SELECT s.id, t.date, s.currency, s.amount, tg.name
		FROM splits s
		JOIN accounts a ON a.id = s.account_id AND a.builtin = 1 AND a.type = ?
		JOIN transactions t ON t.id = s.transaction_id
		LEFT JOIN split_tags st ON st.split_id = s.id
		LEFT JOIN tags tg ON tg.id = st.tag_id
		WHERE 1 = 1`
	args := []any{accType}
	if f.From != "" {
		q += " AND t.date >= ?"
		args = append(args, f.From)
	}
	if f.To != "" {
		q += " AND t.date <= ?"
		args = append(args, f.To)
	}
	q += " ORDER BY s.id"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return TagReport{}, fmt.Errorf("tag report query: %w", err)
	}
	defer rows.Close()

	byTag := map[string]*accumulator{}
	untagged, total := newAcc(), newAcc()
	missing := map[string]bool{}
	var lastSplit int64 = -1
	var splitUSD, splitAmt int64
	var splitCur string
	for rows.Next() {
		var id int64
		var date, cur string
		var amount int64
		var tag *string
		if err := rows.Scan(&id, &date, &cur, &amount, &tag); err != nil {
			return TagReport{}, err
		}
		if id != lastSplit { // a new split: convert once, count it in the total once
			lastSplit = id
			splitAmt, splitCur = sign*amount, cur
			usd, ok := rates.ToUSD(splitAmt, cur, date)
			if !ok {
				missing[cur] = true
			}
			splitUSD = usd
			total.add(splitCur, splitAmt, splitUSD)
			if tag == nil {
				untagged.add(splitCur, splitAmt, splitUSD)
			}
		}
		if tag != nil {
			a := byTag[*tag]
			if a == nil {
				a = newAcc()
				byTag[*tag] = a
			}
			a.add(splitCur, splitAmt, splitUSD)
		}
	}
	if err := rows.Err(); err != nil {
		return TagReport{}, err
	}

	rep := TagReport{Untagged: untagged.line(""), Total: total.line("")}
	for tag, a := range byTag {
		rep.Lines = append(rep.Lines, a.line(tag))
	}
	sort.Slice(rep.Lines, func(i, j int) bool {
		if rep.Lines[i].USD != rep.Lines[j].USD {
			return rep.Lines[i].USD > rep.Lines[j].USD
		}
		return rep.Lines[i].Tag < rep.Lines[j].Tag
	})
	for c := range missing {
		rep.Missing = append(rep.Missing, c)
	}
	sort.Strings(rep.Missing)
	return rep, nil
}
