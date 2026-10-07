package ledger_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/db/gen"
	"github.com/amascii/vinance/internal/ledger"
)

// add creates a one-line expense on BRISK with the given memo and tags.
func (e env) add(t *testing.T, date, desc string, cents int64, memo string, tags ...string) int64 {
	t.Helper()
	id, err := e.svc.Create(context.Background(), ledger.TxnInput{
		Date: date, Description: desc, Currency: "USD",
		Splits: []ledger.SplitInput{
			{AccountID: e.brisk, Currency: "USD", Amount: -cents, Value: -cents},
			{AccountID: e.expense, Memo: memo, Currency: "USD", Amount: cents, Value: cents, Tags: tags},
		},
	})
	require.NoError(t, err)
	return id
}

func descs(p ledger.Page) []string {
	if len(p.Transactions) == 0 {
		return nil
	}
	out := make([]string, len(p.Transactions))
	for i, t := range p.Transactions {
		out[i] = t.Description
	}
	return out
}

func list(t *testing.T, e env, f ledger.Filter) ledger.Page {
	t.Helper()
	p, err := e.svc.List(context.Background(), f)
	require.NoError(t, err)
	return p
}

func TestListFilters(t *testing.T) {
	e := setup(t)
	e.add(t, "2026-09-01", "Dairy Queen", 1250, "", "fast-food", "outside-food")
	e.add(t, "2026-09-02", "HEB", 4200, "Milk", "groceries")
	e.add(t, "2026-09-03", "Walmart", 6500, "Notebook for school", "stationary", "groceries")
	e.add(t, "2026-09-03", "100% Juice Bar", 800, "", "drinks")
	e.add(t, "2026-09-04", "Under_score Cafe", 500, "", "cafes")
	// a KRW transaction on the Seoul Bank account
	_, err := e.svc.Create(context.Background(), ledger.TxnInput{
		Date: "2026-09-05", Description: "Korean Air", Currency: "KRW",
		Splits: []ledger.SplitInput{
			{AccountID: e.kbank, Currency: "KRW", Amount: -713937, Value: -713937},
			{AccountID: e.expense, Currency: "KRW", Amount: 713937, Value: 713937, Tags: []string{"travel"}},
		},
	})
	require.NoError(t, err)

	tests := []struct {
		name string
		f    ledger.Filter
		want []string
	}{
		{"everything, newest first", ledger.Filter{}, []string{"Korean Air", "Under_score Cafe", "100% Juice Bar", "Walmart", "HEB", "Dairy Queen"}},
		{"text in description, case-insensitive", ledger.Filter{Text: "dairy"}, []string{"Dairy Queen"}},
		{"text in split memo", ledger.Filter{Text: "milk"}, []string{"HEB"}},
		{"text spanning words", ledger.Filter{Text: "for school"}, []string{"Walmart"}},
		{"text with % is literal", ledger.Filter{Text: "100%"}, []string{"100% Juice Bar"}},
		{"a lone % does not match everything", ledger.Filter{Text: "%"}, []string{"100% Juice Bar"}},
		{"text with _ is literal", ledger.Filter{Text: "r_s"}, []string{"Under_score Cafe"}},
		{"text with no match", ledger.Filter{Text: "zzz"}, nil},
		{"surrounding spaces ignored", ledger.Filter{Text: "  heb "}, []string{"HEB"}},
		{"one tag", ledger.Filter{Tags: []string{"groceries"}}, []string{"Walmart", "HEB"}},
		{"tags are ANDed", ledger.Filter{Tags: []string{"groceries", "stationary"}}, []string{"Walmart"}},
		{"unknown tag", ledger.Filter{Tags: []string{"nope"}}, nil},
		{"account", ledger.Filter{AccountID: e.kbank}, []string{"Korean Air"}},
		{"account on every other row", ledger.Filter{AccountID: e.brisk}, []string{"Under_score Cafe", "100% Juice Bar", "Walmart", "HEB", "Dairy Queen"}},
		{"from is inclusive", ledger.Filter{From: "2026-09-03"}, []string{"Korean Air", "Under_score Cafe", "100% Juice Bar", "Walmart"}},
		{"to is inclusive", ledger.Filter{To: "2026-09-02"}, []string{"HEB", "Dairy Queen"}},
		{"date range", ledger.Filter{From: "2026-09-02", To: "2026-09-03"}, []string{"100% Juice Bar", "Walmart", "HEB"}},
		{"filters combine", ledger.Filter{Tags: []string{"groceries"}, From: "2026-09-03", Text: "wal"}, []string{"Walmart"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := list(t, e, tc.f)
			assert.Equal(t, tc.want, descs(p))
			n, err := e.svc.Count(context.Background(), tc.f)
			require.NoError(t, err)
			assert.Equal(t, len(tc.want), n, "Count agrees with List")
		})
	}
}

func TestListLoadsSplitsAndTags(t *testing.T) {
	e := setup(t)
	id, err := e.svc.Create(context.Background(), e.walmart())
	require.NoError(t, err)

	p := list(t, e, ledger.Filter{})
	require.Len(t, p.Transactions, 1)
	got := p.Transactions[0]
	require.Len(t, got.Splits, 4)
	assert.Equal(t, id, got.ID)
	assert.Equal(t, []string{"drinks"}, got.Splits[1].Tags)
	assert.Equal(t, []string{"school", "stationary"}, got.Splits[3].Tags)
	assert.Equal(t, "BRISK", got.Splits[0].AccountName)

	single, err := e.svc.Get(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, got, single, "Get and List load identical data")
}

func TestListImbalanceFilter(t *testing.T) {
	e := setup(t)
	e.add(t, "2026-09-01", "fine", 100, "")
	q := gen.New(e.db)
	imb, err := q.GetBuiltinAccount(context.Background(), "imbalance")
	require.NoError(t, err)
	_, err = e.svc.Create(context.Background(), ledger.TxnInput{
		Date: "2026-09-02", Description: "Clothing store", Currency: "USD",
		Splits: []ledger.SplitInput{
			{AccountID: e.brisk, Currency: "USD", Amount: -12000, Value: -12000},
			{AccountID: imb.ID, Currency: "USD", Amount: 12000, Value: 12000},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"Clothing store"}, descs(list(t, e, ledger.Filter{Imbalance: true})))
	assert.Len(t, list(t, e, ledger.Filter{}).Transactions, 2)
}

func TestListPagination(t *testing.T) {
	e := setup(t)
	// 120 transactions over 40 days: three per day, so many share a date.
	for i := 0; i < 120; i++ {
		e.add(t, fmt.Sprintf("2026-08-%02d", 1+i/3%28), fmt.Sprintf("txn-%03d", i), int64(100+i), "")
	}

	var seen []string
	var f ledger.Filter
	pages := 0
	for {
		p := list(t, e, f)
		pages++
		require.NotEmpty(t, p.Transactions)
		for _, tx := range p.Transactions {
			seen = append(seen, tx.Description)
		}
		if p.Next == nil {
			break
		}
		assert.Len(t, p.Transactions, ledger.DefaultPageSize, "only the last page may be short")
		f.Before = p.Next
		require.Less(t, pages, 10, "pagination must terminate")
	}
	assert.Equal(t, 3, pages)
	assert.Len(t, seen, 120)
	assert.Len(t, dedupe(seen), 120, "no transaction appears twice or is skipped across pages")

	// Ordering is date desc, then id desc, across page boundaries.
	all := list(t, e, ledger.Filter{Limit: 1000})
	assert.Nil(t, all.Next)
	for i := 1; i < len(all.Transactions); i++ {
		a, b := all.Transactions[i-1], all.Transactions[i]
		assert.True(t, a.Date > b.Date || (a.Date == b.Date && a.ID > b.ID), "%v before %v", a.ID, b.ID)
	}
	assert.Equal(t, descsOf(all.Transactions), seen, "paged order equals one-shot order")
}

func TestListExactPageBoundaryHasNoNext(t *testing.T) {
	e := setup(t)
	for i := 0; i < 5; i++ {
		e.add(t, "2026-09-01", fmt.Sprintf("t%d", i), 100, "")
	}
	p := list(t, e, ledger.Filter{Limit: 5})
	assert.Len(t, p.Transactions, 5)
	assert.Nil(t, p.Next, "exactly a full page and nothing after it")
	p = list(t, e, ledger.Filter{Limit: 4})
	require.NotNil(t, p.Next)
	p = list(t, e, ledger.Filter{Limit: 4, Before: p.Next})
	assert.Len(t, p.Transactions, 1)
	assert.Nil(t, p.Next)
}

func TestListEmpty(t *testing.T) {
	e := setup(t)
	p := list(t, e, ledger.Filter{})
	assert.Empty(t, p.Transactions)
	assert.Nil(t, p.Next)
}

func TestCursorRoundTrip(t *testing.T) {
	c := ledger.Cursor{Date: "2026-09-24", ID: 1234}
	assert.Equal(t, "2026-09-24:1234", c.String())
	got, err := ledger.ParseCursor(c.String())
	require.NoError(t, err)
	assert.Equal(t, c, got)

	for _, bad := range []string{"", "2026-09-24", "2026-09-24:", "x:1", "2026-09-24:abc", "2026-13-01:1", ":5"} {
		_, err := ledger.ParseCursor(bad)
		assert.Error(t, err, "%q", bad)
	}
}

func dedupe(in []string) map[string]bool {
	m := map[string]bool{}
	for _, s := range in {
		m[s] = true
	}
	return m
}

func descsOf(ts []ledger.Transaction) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Description
	}
	return out
}

func TestLastLike(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.add(t, "2026-09-01", "McDonald's", 1000, "", "old-tag")
	e.add(t, "2026-09-05", "mcdonald's", 1200, "", "fast-food", "outside-food")
	e.add(t, "2026-09-03", "McDonald's", 900, "", "middle")
	e.add(t, "2026-09-09", "Burger King", 800, "", "bk")

	got, err := e.svc.LastLike(ctx, "McDONALD'S ", false)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "2026-09-05", got.Date, "newest match wins, case-insensitively and ignoring surrounding spaces")
	assert.Equal(t, []string{"fast-food", "outside-food"}, got.Summary().Tags)

	none, err := e.svc.LastLike(ctx, "Wendy's", false)
	require.NoError(t, err)
	assert.Nil(t, none)
	none, err = e.svc.LastLike(ctx, "   ", false)
	require.NoError(t, err)
	assert.Nil(t, none)

	// Direction matters: an expense named "Refund" doesn't answer an income lookup.
	e.add(t, "2026-09-10", "Refund", 500, "", "returns")
	none, err = e.svc.LastLike(ctx, "Refund", true)
	require.NoError(t, err)
	assert.Nil(t, none)
	wharf := e.account(t, "Wharf Bank", "asset", "USD")
	inc, err := gen.New(e.db).GetBuiltinAccount(ctx, "income")
	require.NoError(t, err)
	_, err = e.svc.Create(ctx, ledger.TxnInput{
		Date: "2026-09-02", Description: "Refund", Currency: "USD",
		Splits: []ledger.SplitInput{
			{AccountID: wharf, Currency: "USD", Amount: 500, Value: 500},
			{AccountID: inc.ID, Currency: "USD", Amount: -500, Value: -500, Tags: []string{"refunds"}},
		},
	})
	require.NoError(t, err)
	got, err = e.svc.LastLike(ctx, "Refund", true)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, []string{"refunds"}, got.Summary().Tags)
	got, err = e.svc.LastLike(ctx, "Refund", false)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "2026-09-10", got.Date, "and the expense lookup still finds the expense")
}

func descriptionsOf(entries []ledger.PastEntry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Description)
	}
	return out
}

func TestSuggestDescriptions(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		e.add(t, "2026-09-0"+string(rune('1'+i)), "FNDXX Dividends", 4000+int64(i), "", "dividends")
	}
	e.add(t, "2026-09-10", "fndxx dividends", 4500, "", "dividends") // same description, different case
	e.add(t, "2026-09-05", "Maple One", 50000, "", "rent-share")
	e.add(t, "2026-09-06", "Maple One", 50000, "", "rent-share")
	e.add(t, "2026-09-07", "Map Coffee", 450, "", "cafes")
	e.add(t, "2026-09-08", "Dividends Reinvested", 100, "", "dividends")
	e.add(t, "2026-09-09", "100% Juice", 800, "", "drinks")
	e.add(t, "2026-09-09", "Under_score", 800, "", "x")

	got, err := e.svc.SuggestDescriptions(ctx, "fnd", 8)
	require.NoError(t, err)
	require.Len(t, got, 1, "case variants are one description")
	assert.Equal(t, "fndxx dividends", got[0].Description, "shown as last written")
	assert.Equal(t, 4, got[0].Count)
	assert.Equal(t, "2026-09-10", got[0].Last.Date, "carries the newest transaction")

	got, _ = e.svc.SuggestDescriptions(ctx, "MAP", 8)
	assert.Equal(t, []string{"Maple One", "Map Coffee"}, descriptionsOf(got), "most used first among prefix matches")

	got, _ = e.svc.SuggestDescriptions(ctx, "div", 8)
	assert.Equal(t, []string{"Dividends Reinvested", "fndxx dividends"}, descriptionsOf(got), "prefix matches before word-start matches")

	got, _ = e.svc.SuggestDescriptions(ctx, "maple o", 8)
	assert.Equal(t, []string{"Maple One"}, descriptionsOf(got), "multi-word queries work")

	got, _ = e.svc.SuggestDescriptions(ctx, "xx", 8)
	assert.Empty(t, got, "a match must start a description or a word, not sit in the middle")

	got, _ = e.svc.SuggestDescriptions(ctx, "100%", 8)
	assert.Equal(t, []string{"100% Juice"}, descriptionsOf(got))
	got, _ = e.svc.SuggestDescriptions(ctx, "%", 8)
	assert.Empty(t, got, "a lone % is literal (it starts no description), not a wildcard matching everything")
	got, _ = e.svc.SuggestDescriptions(ctx, "under_", 8)
	assert.Equal(t, []string{"Under_score"}, descriptionsOf(got))
	got, _ = e.svc.SuggestDescriptions(ctx, "und_r", 8)
	assert.Empty(t, got, "_ doesn't match any character")

	got, _ = e.svc.SuggestDescriptions(ctx, "ma", 1)
	assert.Len(t, got, 1, "the limit applies")
	got, _ = e.svc.SuggestDescriptions(ctx, "  ", 8)
	assert.Empty(t, got)
	got, _ = e.svc.SuggestDescriptions(ctx, "zzz", 8)
	assert.Empty(t, got)
}

func TestTotals(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	// "meal (john)": I paid 100; "transfer (john)": he paid back 50 (into the real account).
	e.add(t, "2026-09-01", "meal (john)", 10000, "")
	_, err := e.svc.Create(ctx, ledger.TxnInput{
		Date: "2026-09-02", Description: "transfer (john)", Currency: "USD",
		Splits: []ledger.SplitInput{
			{AccountID: e.brisk, Currency: "USD", Amount: 5000, Value: 5000},
			{AccountID: e.expense, Currency: "USD", Amount: -5000, Value: -5000},
		},
	})
	require.NoError(t, err)
	e.add(t, "2026-09-03", "lunch (mary)", 700, "")
	_, err = e.svc.Create(ctx, ledger.TxnInput{
		Date: "2026-09-04", Description: "taxi (john)", Currency: "KRW",
		Splits: []ledger.SplitInput{
			{AccountID: e.kbank, Currency: "KRW", Amount: -3000, Value: -3000},
			{AccountID: e.expense, Currency: "KRW", Amount: 3000, Value: 3000},
		},
	})
	require.NoError(t, err)
	// moving my own money between accounts nets to zero and must not show up
	_, err = e.svc.Create(ctx, ledger.TxnInput{
		Date: "2026-09-05", Description: "own transfer (john)", Currency: "USD",
		Splits: []ledger.SplitInput{
			{AccountID: e.brisk, Currency: "USD", Amount: -900, Value: -900},
			{AccountID: e.kbank, Currency: "KRW", Amount: 1200000, Value: 900},
		},
	})
	require.NoError(t, err)

	got, err := e.svc.Totals(ctx, ledger.Filter{Text: "john"})
	require.NoError(t, err)
	assert.Equal(t, []ledger.CurrencyTotal{{Currency: "KRW", Net: -3000}, {Currency: "USD", Net: -5000}}, got, "one total per currency, whichever account")

	got, err = e.svc.Totals(ctx, ledger.Filter{Text: "john", Limit: 1, Before: &ledger.Cursor{Date: "2026-09-02", ID: 99}})
	require.NoError(t, err)
	assert.Len(t, got, 2, "paging parameters don't narrow the sum")

	got, err = e.svc.Totals(ctx, ledger.Filter{Text: "nobody"})
	require.NoError(t, err)
	assert.Empty(t, got)

	assert.True(t, ledger.Filter{Text: "x"}.Active())
	assert.False(t, ledger.Filter{Limit: 5}.Active())
}
