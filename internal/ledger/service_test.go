package ledger_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/db/gen"
	"github.com/amascii/vinance/internal/ledger"
	"github.com/amascii/vinance/internal/testutil"
)

type env struct {
	svc                        *ledger.Service
	db                         *sql.DB
	brisk, kbank, yen, expense int64
}

func setup(t *testing.T) env {
	t.Helper()
	conn := testutil.NewDB(t)
	q := gen.New(conn)
	ctx := context.Background()
	mk := func(name, typ, cur string) int64 {
		a, err := q.CreateAccount(ctx, gen.CreateAccountParams{
			Name: name, Slug: ledger.AccountSlug(name), Type: typ, Currency: sql.NullString{String: cur, Valid: true},
		})
		require.NoError(t, err)
		return a.ID
	}
	exp, err := q.GetBuiltinAccount(ctx, "expense")
	require.NoError(t, err)
	return env{
		svc: ledger.NewService(conn), db: conn, expense: exp.ID,
		brisk: mk("BRISK", "liability", "USD"), kbank: mk("Seoul Bank", "asset", "KRW"), yen: mk("Yen Wallet", "asset", "JPY"),
	}
}

func count(t *testing.T, conn *sql.DB, table string) int {
	t.Helper()
	var n int
	require.NoError(t, conn.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&n))
	return n
}

// walmart is the statement-split style: one $65.00 charge broken into tagged item lines.
func (e env) walmart() ledger.TxnInput {
	return ledger.TxnInput{
		Date: "2026-09-24", Description: "Walmart", Currency: "USD",
		Splits: []ledger.SplitInput{
			{AccountID: e.brisk, Currency: "USD", Amount: -6500, Value: -6500},
			{AccountID: e.expense, Memo: "Sodas", Currency: "USD", Amount: 1000, Value: 1000, Tags: []string{"Drinks"}},
			{AccountID: e.expense, Memo: "Chips", Currency: "USD", Amount: 2000, Value: 2000, Tags: []string{"snacks", "#Snacks"}},
			{AccountID: e.expense, Memo: "Notebook", Currency: "USD", Amount: 3500, Value: 3500, Tags: []string{"stationary", "school"}},
		},
	}
}

func TestCreateAndGetSplitTransaction(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	id, err := e.svc.Create(ctx, e.walmart())
	require.NoError(t, err)

	got, err := e.svc.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "Walmart", got.Description)
	assert.Equal(t, "2026-09-24", got.Date)
	assert.Empty(t, got.GnucashID)
	require.Len(t, got.Splits, 4)

	assert.Equal(t, "BRISK", got.Splits[0].AccountName)
	assert.EqualValues(t, -6500, got.Splits[0].Amount)
	assert.Equal(t, "n", got.Splits[0].Reconciled)
	assert.Equal(t, "Sodas", got.Splits[1].Memo)
	assert.Equal(t, []string{"drinks"}, got.Splits[1].Tags)
	assert.Equal(t, []string{"snacks"}, got.Splits[2].Tags, "duplicate/#-prefixed tags collapse")
	assert.Equal(t, []string{"school", "stationary"}, got.Splits[3].Tags)
	for i, s := range got.Splits {
		assert.Equal(t, i, s.Position, "splits keep input order")
	}
}

func TestCreateRejectsUnbalancedAndPersistsNothing(t *testing.T) {
	e := setup(t)
	in := e.walmart()
	in.Splits[3].Amount, in.Splits[3].Value = 3000, 3000 // lines now total $60, statement says $65

	_, err := e.svc.Create(context.Background(), in)
	require.ErrorIs(t, err, ledger.ErrUnbalanced)
	assert.Contains(t, err.Error(), "off by 5.00")
	assert.Equal(t, 0, count(t, e.db, "transactions"))
	assert.Equal(t, 0, count(t, e.db, "splits"))
	assert.Equal(t, 0, count(t, e.db, "tags"), "no tags leak from a rejected transaction")
}

func TestCreateRollsBackOnDatabaseError(t *testing.T) {
	e := setup(t)
	in := e.walmart()
	in.GnucashID = "dup"
	_, err := e.svc.Create(context.Background(), in)
	require.NoError(t, err)

	_, err = e.svc.Create(context.Background(), in) // same gnucash_id violates UNIQUE midway
	require.Error(t, err)
	assert.Equal(t, 1, count(t, e.db, "transactions"))
	assert.Equal(t, 4, count(t, e.db, "splits"))
}

func TestCreateValidatesAccounts(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	in := e.walmart()
	in.Splits[0].AccountID = 99999
	_, err := e.svc.Create(ctx, in)
	assert.ErrorContains(t, err, "does not exist")

	// BRISK is a USD account; a MXN split on it is rejected even if it balances.
	in = e.walmart()
	in.Splits[0].Currency = "MXN"
	_, err = e.svc.Create(ctx, in)
	assert.ErrorContains(t, err, "account currency is USD")
	assert.Equal(t, 0, count(t, e.db, "transactions"))
}

func TestCrossCurrencyTransaction(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	// Paying a KRW flight from a KRW account; the expense side is tracked in USD cents.
	id, err := e.svc.Create(ctx, ledger.TxnInput{
		Date: "2026-03-01", Description: "Korean Air", Currency: "KRW",
		Splits: []ledger.SplitInput{
			{AccountID: e.kbank, Currency: "KRW", Amount: -713937, Value: -713937},
			{AccountID: e.expense, Currency: "USD", Amount: 49976, Value: 713937, Tags: []string{"travel"}},
		},
	})
	require.NoError(t, err)
	got, err := e.svc.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "KRW", got.Currency)
	assert.EqualValues(t, 49976, got.Splits[1].Amount)
	assert.EqualValues(t, 713937, got.Splits[1].Value)
	assert.Equal(t, "USD", got.Splits[1].Currency)

	// Yen withdrawal: transaction in USD, wallet split in JPY.
	_, err = e.svc.Create(ctx, ledger.TxnInput{
		Date: "2026-06-11", Description: "Withdrawal", Currency: "USD",
		Splits: []ledger.SplitInput{
			{AccountID: e.yen, Currency: "JPY", Amount: 100000, Value: 62555},
			{AccountID: e.brisk, Currency: "USD", Amount: -62555, Value: -62555},
		},
	})
	require.NoError(t, err)
}

func TestUpdateReplacesSplitsAndTags(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	id, err := e.svc.Create(ctx, e.walmart())
	require.NoError(t, err)
	_, err = e.db.Exec("UPDATE transactions SET updated_at = '2000-01-01T00:00:00Z' WHERE id = ?", id)
	require.NoError(t, err)

	err = e.svc.Update(ctx, id, ledger.TxnInput{
		Date: "2026-09-25", Description: "Walmart (fixed)", Currency: "USD", Notes: "recount",
		Splits: []ledger.SplitInput{
			{AccountID: e.brisk, Currency: "USD", Amount: -6500, Value: -6500},
			{AccountID: e.expense, Currency: "USD", Amount: 6500, Value: 6500, Tags: []string{"groceries"}},
		},
	})
	require.NoError(t, err)

	got, err := e.svc.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "Walmart (fixed)", got.Description)
	assert.Equal(t, "2026-09-25", got.Date)
	assert.Equal(t, "recount", got.Notes)
	assert.NotEqual(t, "2000-01-01T00:00:00Z", got.UpdatedAt, "updated_at is bumped")
	require.Len(t, got.Splits, 2)
	assert.Equal(t, []string{"groceries"}, got.Splits[1].Tags)
	assert.Equal(t, 2, count(t, e.db, "splits"), "old splits removed")
	assert.Equal(t, 1, count(t, e.db, "split_tags"), "old split tags removed")
}

func TestUpdateInvalidLeavesOriginalIntact(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	id, err := e.svc.Create(ctx, e.walmart())
	require.NoError(t, err)

	bad := e.walmart()
	bad.Description = "changed"
	bad.Splits[1].Amount, bad.Splits[1].Value = 1, 1
	require.ErrorIs(t, e.svc.Update(ctx, id, bad), ledger.ErrUnbalanced)

	got, err := e.svc.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "Walmart", got.Description)
	assert.Len(t, got.Splits, 4)
}

func TestUpdateAndGetAndDeleteNotFound(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	assert.ErrorIs(t, e.svc.Update(ctx, 12345, e.walmart()), ledger.ErrNotFound)
	_, err := e.svc.Get(ctx, 12345)
	assert.ErrorIs(t, err, ledger.ErrNotFound)
	assert.ErrorIs(t, e.svc.Delete(ctx, 12345), ledger.ErrNotFound)
}

func TestDelete(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	id, err := e.svc.Create(ctx, e.walmart())
	require.NoError(t, err)
	require.NoError(t, e.svc.Delete(ctx, id))
	assert.Equal(t, 0, count(t, e.db, "transactions"))
	assert.Equal(t, 0, count(t, e.db, "splits"))
	assert.Equal(t, 0, count(t, e.db, "split_tags"))
}

func TestCreateInParticipatesInCallerTransaction(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	tx, err := e.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = e.svc.CreateIn(ctx, tx, e.walmart())
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())
	assert.Equal(t, 0, count(t, e.db, "transactions"), "caller rollback undoes CreateIn")
}

func TestListRecentOrdersByDateThenID(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	mk := func(date, desc string) int64 {
		in := e.walmart()
		in.Date, in.Description = date, desc
		id, err := e.svc.Create(ctx, in)
		require.NoError(t, err)
		return id
	}
	mk("2026-09-01", "old")
	mk("2026-09-03", "newest-first-inserted")
	mk("2026-09-03", "newest-second-inserted")
	mk("2026-09-02", "middle")

	got, err := e.svc.ListRecent(ctx, 3)
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, "newest-second-inserted", got[0].Description)
	assert.Equal(t, "newest-first-inserted", got[1].Description)
	assert.Equal(t, "middle", got[2].Description)
	assert.Len(t, got[0].Splits, 4, "splits are loaded")

	all, err := e.svc.ListRecent(ctx, 100)
	require.NoError(t, err)
	assert.Len(t, all, 4)
}

// setupEmpty returns a migrated database with no real accounts or transactions.
func setupEmpty(t *testing.T) *sql.DB { return testutil.NewDB(t) }
