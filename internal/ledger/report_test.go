package ledger_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/db/gen"
	"github.com/amascii/vinance/internal/ledger"
)

type tagSpend struct {
	date, cur string
	cents     int64
	tags      []string
}

// spendOn records an expense in the given currency on a real account of that currency.
func (e env) spendOn(t *testing.T, acct int64, s tagSpend) {
	t.Helper()
	_, err := e.svc.Create(context.Background(), ledger.TxnInput{
		Date: s.date, Description: "x", Currency: s.cur,
		Splits: []ledger.SplitInput{
			{AccountID: acct, Currency: s.cur, Amount: -s.cents, Value: -s.cents},
			{AccountID: e.expense, Currency: s.cur, Amount: s.cents, Value: s.cents, Tags: s.tags},
		},
	})
	require.NoError(t, err)
}

func lineFor(rep ledger.TagReport, tag string) ledger.TagLine {
	for _, l := range rep.Lines {
		if l.Tag == tag {
			return l
		}
	}
	return ledger.TagLine{}
}

func TestTagReport(t *testing.T) {
	e := setup(t)
	neo := e.account(t, "Neo", "asset", "MXN")
	e.price(t, "MXN", "2026-01-01", "0.0500")
	e.price(t, "MXN", "2026-06-01", "0.0600")

	// A $50 grocery run with two tags (it counts toward both), a $20 snack, an untagged $10,
	// a 100.00 MXN taxi early in the year (rate 0.05 = $5.00) and 100.00 MXN later (rate 0.06 = $6.00),
	// and a $5 refund (negative expense) on groceries.
	e.spendOn(t, e.brisk, tagSpend{"2026-02-01", "USD", 5000, []string{"groceries", "ingredients"}})
	e.spendOn(t, e.brisk, tagSpend{"2026-02-02", "USD", 2000, []string{"snacks"}})
	e.spendOn(t, e.brisk, tagSpend{"2026-02-03", "USD", 1000, nil})
	e.spendOn(t, neo, tagSpend{"2026-02-04", "MXN", 10000, []string{"transport"}})
	e.spendOn(t, neo, tagSpend{"2026-07-04", "MXN", 10000, []string{"transport"}})
	e.spendOn(t, e.brisk, tagSpend{"2026-02-05", "USD", -500, []string{"groceries"}})

	rep, err := e.svc.TagReport(context.Background(), ledger.ReportFilter{})
	require.NoError(t, err)

	assert.EqualValues(t, 4500, lineFor(rep, "groceries").USD, "$50 spent minus the $5 refund")
	assert.Equal(t, 2, lineFor(rep, "groceries").Splits)
	assert.EqualValues(t, 5000, lineFor(rep, "ingredients").USD, "a multi-tag split counts toward every tag")
	assert.EqualValues(t, 2000, lineFor(rep, "snacks").USD)

	tr := lineFor(rep, "transport")
	assert.EqualValues(t, 1100, tr.USD, "each MXN purchase converts at the rate in effect on its own date: $5.00 + $6.00")
	assert.Equal(t, []ledger.CurrencyAmount{{Currency: "MXN", Amount: 20000}}, tr.ByCurrency)
	assert.Equal(t, 2, tr.Splits)

	assert.EqualValues(t, 1000, rep.Untagged.USD)
	assert.Equal(t, 1, rep.Untagged.Splits)

	// Total counts every split exactly once: 50 + 20 + 10 + 5 + 6 - 5 = 86.00
	assert.EqualValues(t, 8600, rep.Total.USD)
	assert.Equal(t, 6, rep.Total.Splits)
	assert.Equal(t, []ledger.CurrencyAmount{{"MXN", 20000}, {"USD", 7500}}, rep.Total.ByCurrency)
	assert.Empty(t, rep.Missing)

	// Largest first.
	var order []string
	for _, l := range rep.Lines {
		order = append(order, l.Tag)
	}
	assert.Equal(t, []string{"ingredients", "groceries", "snacks", "transport"}, order)
}

func TestTagReportDateRange(t *testing.T) {
	e := setup(t)
	e.spendOn(t, e.brisk, tagSpend{"2026-01-31", "USD", 100, []string{"a"}})
	e.spendOn(t, e.brisk, tagSpend{"2026-02-01", "USD", 200, []string{"a"}})
	e.spendOn(t, e.brisk, tagSpend{"2026-02-28", "USD", 400, []string{"a"}})
	e.spendOn(t, e.brisk, tagSpend{"2026-03-01", "USD", 800, []string{"a"}})
	ctx := context.Background()

	rep, err := e.svc.TagReport(ctx, ledger.ReportFilter{From: "2026-02-01", To: "2026-02-28"})
	require.NoError(t, err)
	assert.EqualValues(t, 600, rep.Total.USD, "both bounds are inclusive")

	rep, _ = e.svc.TagReport(ctx, ledger.ReportFilter{From: "2026-02-15"})
	assert.EqualValues(t, 1200, rep.Total.USD)
	rep, _ = e.svc.TagReport(ctx, ledger.ReportFilter{To: "2026-01-31"})
	assert.EqualValues(t, 100, rep.Total.USD)
	rep, _ = e.svc.TagReport(ctx, ledger.ReportFilter{})
	assert.EqualValues(t, 1500, rep.Total.USD)
	rep, _ = e.svc.TagReport(ctx, ledger.ReportFilter{From: "2030-01-01"})
	assert.Zero(t, rep.Total.USD)
	assert.Empty(t, rep.Lines)
}

func TestTagReportMissingRates(t *testing.T) {
	e := setup(t) // has a JPY wallet, no JPY rate
	e.spendOn(t, e.brisk, tagSpend{"2026-02-01", "USD", 1000, []string{"food"}})
	e.spendOn(t, e.yen, tagSpend{"2026-02-02", "JPY", 5000, []string{"food"}})

	rep, err := e.svc.TagReport(context.Background(), ledger.ReportFilter{})
	require.NoError(t, err)
	assert.Equal(t, []string{"JPY"}, rep.Missing)
	assert.EqualValues(t, 1000, lineFor(rep, "food").USD, "yen has no rate, so it is left out of the USD figure...")
	assert.Equal(t, []ledger.CurrencyAmount{{"JPY", 5000}, {"USD", 1000}}, lineFor(rep, "food").ByCurrency, "...but still shown in its own currency")
}

func TestTagReportIncomeAndTransfersExcluded(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	wharf := e.account(t, "Wharf Bank", "asset", "USD")
	inc, err := gen.New(e.db).GetBuiltinAccount(ctx, "income")
	require.NoError(t, err)
	_, err = e.svc.Create(ctx, ledger.TxnInput{
		Date: "2026-02-01", Description: "Paycheck", Currency: "USD",
		Splits: []ledger.SplitInput{
			{AccountID: wharf, Currency: "USD", Amount: 300000, Value: 300000},
			{AccountID: inc.ID, Currency: "USD", Amount: -300000, Value: -300000, Tags: []string{"salary"}},
		},
	})
	require.NoError(t, err)
	e.move(t, wharf, e.brisk, "USD", 5000) // a card payment: a transfer, not spending
	e.spendOn(t, e.brisk, tagSpend{"2026-02-02", "USD", 700, []string{"coffee"}})

	spend, err := e.svc.TagReport(ctx, ledger.ReportFilter{})
	require.NoError(t, err)
	assert.EqualValues(t, 700, spend.Total.USD, "income and transfers are not spending")
	assert.Equal(t, 1, len(spend.Lines))

	income, err := e.svc.TagReport(ctx, ledger.ReportFilter{Income: true})
	require.NoError(t, err)
	assert.EqualValues(t, 300000, income.Total.USD, "income is reported as a positive amount")
	assert.EqualValues(t, 300000, lineFor(income, "salary").USD)
}

func TestTagReportEmpty(t *testing.T) {
	e := setup(t)
	rep, err := e.svc.TagReport(context.Background(), ledger.ReportFilter{})
	require.NoError(t, err)
	assert.Empty(t, rep.Lines)
	assert.Zero(t, rep.Total.USD)
	assert.Zero(t, rep.Untagged.Splits)
}

func TestListUntaggedFilter(t *testing.T) {
	e := setup(t)
	e.add(t, "2026-09-01", "tagged", 100, "", "food")
	e.add(t, "2026-09-02", "untagged", 100, "")
	// a split receipt where only one line is untagged still qualifies
	_, err := e.svc.Create(context.Background(), ledger.TxnInput{
		Date: "2026-09-03", Description: "partly", Currency: "USD",
		Splits: []ledger.SplitInput{
			{AccountID: e.brisk, Currency: "USD", Amount: -300, Value: -300},
			{AccountID: e.expense, Currency: "USD", Amount: 100, Value: 100, Tags: []string{"food"}},
			{AccountID: e.expense, Currency: "USD", Amount: 200, Value: 200},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"partly", "untagged"}, descs(list(t, e, ledger.Filter{Untagged: true})))
	n, err := e.svc.Count(context.Background(), ledger.Filter{Untagged: true})
	require.NoError(t, err)
	assert.Equal(t, 2, n)
}
