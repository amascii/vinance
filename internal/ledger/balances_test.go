package ledger_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/db/gen"
	"github.com/amascii/vinance/internal/ledger"
)

func (e env) account(t *testing.T, name, typ, cur string) int64 {
	t.Helper()
	a, err := gen.New(e.db).CreateAccount(context.Background(), gen.CreateAccountParams{
		Name: name, Slug: ledger.AccountSlug(name), Type: typ, Currency: sql.NullString{String: cur, Valid: true},
	})
	require.NoError(t, err)
	return a.ID
}

func (e env) move(t *testing.T, from, to int64, cur string, cents int64) {
	t.Helper()
	_, err := e.svc.Create(context.Background(), ledger.TxnInput{
		Date: "2026-09-01", Description: "move", Currency: cur,
		Splits: []ledger.SplitInput{
			{AccountID: from, Currency: cur, Amount: -cents, Value: -cents},
			{AccountID: to, Currency: cur, Amount: cents, Value: cents},
		},
	})
	require.NoError(t, err)
}

func (e env) price(t *testing.T, cur, date, rate string) {
	t.Helper()
	_, err := e.db.Exec("INSERT INTO prices VALUES (?,?,?)", cur, date, rate)
	require.NoError(t, err)
}

func byName(list []ledger.AccountBalance) map[string]ledger.AccountBalance {
	m := map[string]ledger.AccountBalance{}
	for _, a := range list {
		m[a.Name] = a
	}
	return m
}

func TestBalanceSheet(t *testing.T) {
	e := setup(t) // BRISK (USD liability), Seoul Bank (KRW asset), Yen Wallet (JPY asset)
	ctx := context.Background()
	wharf := e.account(t, "Wharf Bank", "asset", "USD")
	neo := e.account(t, "Neo", "asset", "MXN")
	equity, err := gen.New(e.db).GetBuiltinAccount(ctx, "equity")
	require.NoError(t, err)

	// Fund accounts from Equity: Wharf $1,000.00, Neo MX$5,000.00, Seoul Bank ₩100,000, Yen Wallet ¥50,000.
	fund := func(acct int64, cur string, amt int64) {
		_, err := e.svc.Create(ctx, ledger.TxnInput{
			Date: "2026-01-01", Description: "Opening", Currency: cur,
			Splits: []ledger.SplitInput{
				{AccountID: acct, Currency: cur, Amount: amt, Value: amt},
				{AccountID: equity.ID, Currency: cur, Amount: -amt, Value: -amt},
			},
		})
		require.NoError(t, err)
	}
	fund(wharf, "USD", 100000)
	fund(neo, "MXN", 500000)
	fund(e.kbank, "KRW", 100000)
	fund(e.yen, "JPY", 50000)
	// Spend $65.00 on BRISK: the card now owes $65.00.
	_, err = e.svc.Create(ctx, e.walmart())
	require.NoError(t, err)

	// Rates: two dates for MXN (latest wins); none for JPY.
	e.price(t, "MXN", "2026-01-01", "0.0500")
	e.price(t, "MXN", "2026-09-01", "0.0550")
	e.price(t, "KRW", "2026-03-07", "0.0007")

	bs, err := e.svc.BalanceSheet(ctx)
	require.NoError(t, err)

	assets, liabs := byName(bs.Assets), byName(bs.Liabilities)
	require.Len(t, bs.Assets, 4)
	require.Len(t, bs.Liabilities, 1)

	assert.EqualValues(t, 100000, assets["Wharf Bank"].Balance)
	assert.True(t, assets["Wharf Bank"].HasUSD)
	assert.EqualValues(t, 100000, assets["Wharf Bank"].USD)
	assert.Empty(t, assets["Wharf Bank"].Rate)

	assert.EqualValues(t, 500000, assets["Neo"].Balance)
	assert.EqualValues(t, 27500, assets["Neo"].USD, "MX$5,000 at the latest rate 0.055 = $275.00")
	assert.Equal(t, "0.0550", assets["Neo"].Rate)

	assert.EqualValues(t, 100000, assets["Seoul Bank"].Balance)
	assert.EqualValues(t, 7000, assets["Seoul Bank"].USD, "₩100,000 at 0.0007 = $70.00")

	assert.EqualValues(t, 50000, assets["Yen Wallet"].Balance)
	assert.False(t, assets["Yen Wallet"].HasUSD, "no JPY rate known")

	brisk := liabs["BRISK"]
	assert.EqualValues(t, -6500, brisk.Balance, "debit-positive: owing money is negative")
	assert.EqualValues(t, 6500, brisk.Display(), "displayed as the amount owed")
	assert.EqualValues(t, 6500, brisk.DisplayUSD())

	assert.EqualValues(t, 100000+27500+7000, bs.AssetsUSD, "yen is excluded: no rate")
	assert.EqualValues(t, 6500, bs.OwedUSD)
	assert.EqualValues(t, 100000+27500+7000-6500, bs.NetWorthUSD)
	assert.Equal(t, []string{"JPY"}, bs.MissingRates)
	assert.Equal(t, "2026-09-01", bs.RateDates["MXN"])
}

func TestBalanceSheetArchivedAndEmptyAccounts(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	empty := e.account(t, "Empty", "asset", "USD")
	old := e.account(t, "Old Savings", "asset", "USD")
	oldWithMoney := e.account(t, "Old Checking", "asset", "USD")
	e.move(t, e.brisk, oldWithMoney, "USD", 1000)
	_ = empty
	for _, id := range []int64{old, oldWithMoney} {
		_, err := e.db.Exec("UPDATE accounts SET archived = 1 WHERE id = ?", id)
		require.NoError(t, err)
	}

	bs, err := e.svc.BalanceSheet(ctx)
	require.NoError(t, err)
	assets := byName(bs.Assets)
	assert.Contains(t, assets, "Empty", "an unarchived account shows even at zero")
	assert.NotContains(t, assets, "Old Savings", "an archived, empty account is hidden")
	assert.Contains(t, assets, "Old Checking", "an archived account that still holds money stays visible")
	assert.True(t, assets["Old Checking"].Archived)
}

func TestBalanceSheetOnEmptyDatabase(t *testing.T) {
	conn := setupEmpty(t)
	bs, err := ledger.NewService(conn).BalanceSheet(context.Background())
	require.NoError(t, err)
	assert.Empty(t, bs.Assets)
	assert.Empty(t, bs.Liabilities)
	assert.Zero(t, bs.NetWorthUSD)
	assert.Empty(t, bs.MissingRates)
}

func TestBalanceSheetIgnoresBuiltinsAndMatchesSplitSums(t *testing.T) {
	e := setup(t)
	_, err := e.svc.Create(context.Background(), e.walmart())
	require.NoError(t, err)
	bs, err := e.svc.BalanceSheet(context.Background())
	require.NoError(t, err)
	for _, a := range append(append([]ledger.AccountBalance{}, bs.Assets...), bs.Liabilities...) {
		var want int64
		require.NoError(t, e.db.QueryRow("SELECT COALESCE(SUM(amount),0) FROM splits WHERE account_id = ?", a.ID).Scan(&want))
		assert.Equal(t, want, a.Balance, a.Name)
		assert.NotEqual(t, "Expenses", a.Name)
	}
}

func TestRunningBalances(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	wharf := e.account(t, "Wharf Bank", "asset", "USD")
	equity, err := gen.New(e.db).GetBuiltinAccount(ctx, "equity")
	require.NoError(t, err)

	mk := func(date, desc string, lines ...ledger.SplitInput) int64 {
		id, err := e.svc.Create(ctx, ledger.TxnInput{Date: date, Description: desc, Currency: "USD", Splits: lines})
		require.NoError(t, err)
		return id
	}
	usd := func(acct int64, cents int64) ledger.SplitInput {
		return ledger.SplitInput{AccountID: acct, Currency: "USD", Amount: cents, Value: cents}
	}
	open := mk("2026-01-01", "Opening", usd(wharf, 100000), usd(equity.ID, -100000))
	rent := mk("2026-01-05", "Rent", usd(wharf, -30000), usd(e.expense, 30000))
	// Same date as the next one: id order breaks the tie, exactly like the list's ordering.
	pay := mk("2026-01-10", "Card payment", usd(wharf, -5000), usd(e.brisk, 5000))
	tip := mk("2026-01-10", "Tip", usd(wharf, -500), usd(e.expense, 500))
	// Entered later but dated earlier: it must slot into the middle of the history.
	late := mk("2026-01-03", "Back-dated deposit", usd(wharf, 2500), usd(equity.ID, -2500))
	// Two lines in the same account within one transaction are netted.
	split := mk("2026-01-20", "Split", usd(wharf, -1000), usd(wharf, 400), usd(e.expense, 600))

	all := []int64{open, rent, pay, tip, late, split}
	got, err := e.svc.RunningBalances(ctx, wharf, all)
	require.NoError(t, err)
	// History in (date, id) order: open +1000.00, late +25.00, rent -300.00, pay -50.00, tip -5.00, split -6.00
	assert.Equal(t, ledger.Movement{Delta: 100000, Balance: 100000}, got[open])
	assert.Equal(t, ledger.Movement{Delta: 2500, Balance: 102500}, got[late])
	assert.Equal(t, ledger.Movement{Delta: -30000, Balance: 72500}, got[rent])
	assert.Equal(t, ledger.Movement{Delta: -5000, Balance: 67500}, got[pay])
	assert.Equal(t, ledger.Movement{Delta: -500, Balance: 67000}, got[tip])
	assert.Equal(t, ledger.Movement{Delta: -600, Balance: 66400}, got[split], "two lines in one account are netted")

	// Asking for only some transactions still counts all earlier ones.
	part, err := e.svc.RunningBalances(ctx, wharf, []int64{tip})
	require.NoError(t, err)
	assert.Equal(t, map[int64]ledger.Movement{tip: {Delta: -500, Balance: 67000}}, part)

	// The last balance equals the account's current balance, and the balances page agrees.
	now, err := e.svc.AccountBalance(ctx, wharf)
	require.NoError(t, err)
	assert.EqualValues(t, 66400, now)
	bs, err := e.svc.BalanceSheet(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, now, byName(bs.Assets)["Wharf Bank"].Balance)

	// A transaction that doesn't touch the account is simply absent; an unknown id too.
	other := mk("2026-01-25", "Elsewhere", usd(e.brisk, -700), usd(e.expense, 700))
	part, err = e.svc.RunningBalances(ctx, wharf, []int64{other, 99999})
	require.NoError(t, err)
	assert.Empty(t, part)

	// Another account has its own history (liabilities are negative: owing money).
	cardBal, err := e.svc.RunningBalances(ctx, e.brisk, []int64{pay, other})
	require.NoError(t, err)
	assert.Equal(t, ledger.Movement{Delta: 5000, Balance: 5000}, cardBal[pay])
	assert.Equal(t, ledger.Movement{Delta: -700, Balance: 4300}, cardBal[other])

	empty, err := e.svc.RunningBalances(ctx, wharf, nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
}
