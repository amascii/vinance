package ledger_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/db/gen"
	"github.com/amascii/vinance/internal/ledger"
)

func TestCreateAccount(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	a, err := e.svc.CreateAccount(ctx, ledger.NewAccount{Name: "  Wharf Bank Savings ", Type: "asset", Currency: "usd"})
	require.NoError(t, err)
	assert.Equal(t, "Wharf Bank Savings", a.Name)
	assert.Equal(t, "wharf-bank-savings", a.Slug)
	assert.Equal(t, "USD", a.Currency)
	assert.NotZero(t, a.ID)

	got, err := gen.New(e.db).GetAccountBySlug(ctx, "wharf-bank-savings")
	require.NoError(t, err)
	assert.EqualValues(t, 0, got.Builtin)
	assert.EqualValues(t, 0, got.Archived)
	assert.Equal(t, "USD", got.Currency.String)
	assert.Equal(t, 0, count(t, e.db, "transactions"), "no opening balance means no transaction")
}

func TestCreateAccountValidation(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	tests := []struct {
		name string
		in   ledger.NewAccount
		want string
	}{
		{"no name", ledger.NewAccount{Name: "  ", Type: "asset", Currency: "USD"}, "name is required"},
		{"name without letters", ledger.NewAccount{Name: "!!!", Type: "asset", Currency: "USD"}, "at least one letter or number"},
		{"name too long", ledger.NewAccount{Name: string(make([]rune, 61)), Type: "asset", Currency: "USD"}, "too long"},
		{"bad type", ledger.NewAccount{Name: "X", Type: "expense", Currency: "USD"}, "asset or liability"},
		{"bad currency", ledger.NewAccount{Name: "X", Type: "asset", Currency: "dollars"}, "3-letter"},
		{"empty currency", ledger.NewAccount{Name: "X", Type: "asset"}, "3-letter"},
		{"negative opening", ledger.NewAccount{Name: "X", Type: "asset", Currency: "USD", OpeningBalance: -1, OpeningDate: "2026-01-01"}, "can't be negative"},
		{"opening without date", ledger.NewAccount{Name: "X", Type: "asset", Currency: "USD", OpeningBalance: 100}, "needs a date"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := e.svc.CreateAccount(ctx, tc.in)
			require.Error(t, err)
			assert.ErrorIs(t, err, ledger.ErrInvalidAccount)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
	all, err := e.svc.ListAccounts(ctx)
	require.NoError(t, err)
	assert.Len(t, all, 3, "nothing was created by the failed attempts (setup has 3 accounts)")
}

func TestCreateAccountRejectsDuplicates(t *testing.T) {
	e := setup(t) // has BRISK
	ctx := context.Background()
	_, err := e.svc.CreateAccount(ctx, ledger.NewAccount{Name: "brisk", Type: "liability", Currency: "USD"})
	assert.ErrorIs(t, err, ledger.ErrAccountExists, "names are compared case-insensitively")
	_, err = e.svc.CreateAccount(ctx, ledger.NewAccount{Name: "Brisk!", Type: "liability", Currency: "USD"})
	assert.ErrorIs(t, err, ledger.ErrAccountExists, "two names with the same @slug would be ambiguous")
	_, err = e.svc.CreateAccount(ctx, ledger.NewAccount{Name: "Expenses", Type: "asset", Currency: "USD"})
	assert.ErrorIs(t, err, ledger.ErrAccountExists, "built-in names are taken too")
}

func TestCreateAccountWithOpeningBalance(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	asset, err := e.svc.CreateAccount(ctx, ledger.NewAccount{Name: "Savings", Type: "asset", Currency: "USD", OpeningBalance: 150000, OpeningDate: "2026-01-31"})
	require.NoError(t, err)
	card, err := e.svc.CreateAccount(ctx, ledger.NewAccount{Name: "Visa", Type: "liability", Currency: "USD", OpeningBalance: 25000, OpeningDate: "2026-01-31"})
	require.NoError(t, err)
	yen, err := e.svc.CreateAccount(ctx, ledger.NewAccount{Name: "Yen", Type: "asset", Currency: "JPY", OpeningBalance: 30000, OpeningDate: "2026-02-01"})
	require.NoError(t, err)

	bs, err := e.svc.BalanceSheet(ctx)
	require.NoError(t, err)
	assets, liabs := byName(bs.Assets), byName(bs.Liabilities)
	assert.EqualValues(t, 150000, assets["Savings"].Balance)
	assert.EqualValues(t, -25000, liabs["Visa"].Balance, "a liability's opening balance is what you owe")
	assert.EqualValues(t, 25000, liabs["Visa"].Display())
	assert.EqualValues(t, 30000, assets["Yen"].Balance)

	// Each opening balance is a balanced transaction against Equity, tagged like imported ones.
	rows, err := e.svc.List(ctx, ledger.Filter{AccountID: asset.ID})
	require.NoError(t, err)
	require.Len(t, rows.Transactions, 1)
	tx := rows.Transactions[0]
	assert.Equal(t, "Opening balance", tx.Description)
	assert.Equal(t, "2026-01-31", tx.Date)
	assert.Equal(t, "Equity", tx.Splits[1].AccountName)
	assert.Equal(t, []string{"opening-balance"}, tx.Splits[1].Tags)
	assert.EqualValues(t, 0, tx.Splits[0].Value+tx.Splits[1].Value)
	_ = card
	_ = yen
}

func TestCreateAccountIsAtomicWithItsOpeningBalance(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	// Remove the Equity account so the opening transaction can't be created: the account must not survive.
	_, err := e.db.Exec("PRAGMA foreign_keys = OFF")
	require.NoError(t, err)
	_, err = e.db.Exec("DELETE FROM accounts WHERE builtin = 1 AND type = 'equity'")
	require.NoError(t, err)

	_, err = e.svc.CreateAccount(ctx, ledger.NewAccount{Name: "Ghost", Type: "asset", Currency: "USD", OpeningBalance: 100, OpeningDate: "2026-01-01"})
	require.Error(t, err)
	all, err := e.svc.ListAccounts(ctx)
	require.NoError(t, err)
	for _, a := range all {
		assert.NotEqual(t, "Ghost", a.Name, "the half-created account was rolled back")
	}
}

func TestUpdateAccount(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	a, err := e.svc.CreateAccount(ctx, ledger.NewAccount{Name: "Neo", Type: "asset", Currency: "MXN"})
	require.NoError(t, err)

	// Rename: the slug follows.
	got, err := e.svc.UpdateAccount(ctx, a.ID, ledger.AccountUpdate{Name: "Nubank", Type: "asset", Currency: "MXN"})
	require.NoError(t, err)
	assert.Equal(t, "Nubank", got.Name)
	assert.Equal(t, "nubank", got.Slug)
	_, err = gen.New(e.db).GetAccountBySlug(ctx, "neo")
	assert.Error(t, err, "the old slug is gone")

	// Keeping the same name is fine (not a duplicate of itself); the type can change.
	got, err = e.svc.UpdateAccount(ctx, a.ID, ledger.AccountUpdate{Name: "nubank", Type: "liability", Currency: "MXN"})
	require.NoError(t, err)
	assert.Equal(t, "liability", got.Type)

	// Unused account: the currency may change.
	got, err = e.svc.UpdateAccount(ctx, a.ID, ledger.AccountUpdate{Name: "Nubank", Type: "asset", Currency: "USD"})
	require.NoError(t, err)
	assert.Equal(t, "USD", got.Currency)

	// Collisions are refused.
	_, err = e.svc.UpdateAccount(ctx, a.ID, ledger.AccountUpdate{Name: "BRISK", Type: "asset", Currency: "USD"})
	assert.ErrorIs(t, err, ledger.ErrAccountExists)
	_, err = e.svc.UpdateAccount(ctx, a.ID, ledger.AccountUpdate{Name: "Brisk!", Type: "asset", Currency: "USD"})
	assert.ErrorIs(t, err, ledger.ErrAccountExists, "same slug as BRISK")

	// Validation and missing accounts.
	_, err = e.svc.UpdateAccount(ctx, a.ID, ledger.AccountUpdate{Name: "", Type: "asset", Currency: "USD"})
	assert.ErrorIs(t, err, ledger.ErrInvalidAccount)
	_, err = e.svc.UpdateAccount(ctx, 99999, ledger.AccountUpdate{Name: "x", Type: "asset", Currency: "USD"})
	assert.ErrorIs(t, err, ledger.ErrAccountNotFound)
	exp, err := gen.New(e.db).GetBuiltinAccount(ctx, "expense")
	require.NoError(t, err)
	_, err = e.svc.UpdateAccount(ctx, exp.ID, ledger.AccountUpdate{Name: "Spending", Type: "asset", Currency: "USD"})
	assert.ErrorIs(t, err, ledger.ErrAccountNotFound, "built-in accounts can't be edited")
}

func TestUpdateAccountCurrencyLockedOnceUsed(t *testing.T) {
	e := setup(t) // BRISK is USD and gets used
	ctx := context.Background()
	_, err := e.svc.Create(ctx, e.walmart())
	require.NoError(t, err)

	_, err = e.svc.UpdateAccount(ctx, e.brisk, ledger.AccountUpdate{Name: "BRISK", Type: "liability", Currency: "MXN"})
	assert.ErrorIs(t, err, ledger.ErrAccountInUse)
	got, err := e.svc.UpdateAccount(ctx, e.brisk, ledger.AccountUpdate{Name: "BRISK Mastercard", Type: "liability", Currency: "USD"})
	require.NoError(t, err, "renaming a used account is fine")
	assert.Equal(t, 1, got.Splits)

	// History followed the rename.
	p, err := e.svc.List(ctx, ledger.Filter{AccountID: e.brisk})
	require.NoError(t, err)
	assert.Equal(t, "BRISK Mastercard", p.Transactions[0].Splits[0].AccountName)
}

func TestArchiveAccount(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	require.NoError(t, e.svc.SetAccountArchived(ctx, e.brisk, true))
	all, err := e.svc.ListAccounts(ctx)
	require.NoError(t, err)
	assert.True(t, byID(all, e.brisk).Archived)
	require.NoError(t, e.svc.SetAccountArchived(ctx, e.brisk, false))
	all, _ = e.svc.ListAccounts(ctx)
	assert.False(t, byID(all, e.brisk).Archived)

	assert.ErrorIs(t, e.svc.SetAccountArchived(ctx, 99999, true), ledger.ErrAccountNotFound)
	exp, _ := gen.New(e.db).GetBuiltinAccount(ctx, "expense")
	assert.ErrorIs(t, e.svc.SetAccountArchived(ctx, exp.ID, true), ledger.ErrAccountNotFound)
}

func byID(list []ledger.Account, id int64) ledger.Account {
	for _, a := range list {
		if a.ID == id {
			return a
		}
	}
	return ledger.Account{}
}

func TestDeleteAccount(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	unused, err := e.svc.CreateAccount(ctx, ledger.NewAccount{Name: "Spare", Type: "asset", Currency: "USD"})
	require.NoError(t, err)
	_, err = e.svc.Create(ctx, e.walmart())
	require.NoError(t, err)

	assert.ErrorIs(t, e.svc.DeleteAccount(ctx, e.brisk), ledger.ErrAccountInUse)
	require.NoError(t, e.svc.DeleteAccount(ctx, unused.ID))
	assert.ErrorIs(t, e.svc.DeleteAccount(ctx, unused.ID), ledger.ErrAccountNotFound)
	exp, _ := gen.New(e.db).GetBuiltinAccount(ctx, "expense")
	assert.ErrorIs(t, e.svc.DeleteAccount(ctx, exp.ID), ledger.ErrAccountNotFound)
}

func TestListAccountsIncludesUsageAndArchived(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	_, err := e.svc.Create(ctx, e.walmart())
	require.NoError(t, err)
	require.NoError(t, e.svc.SetAccountArchived(ctx, e.kbank, true))

	all, err := e.svc.ListAccounts(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, byID(all, e.brisk).Splits)
	assert.Equal(t, 0, byID(all, e.yen).Splits)
	assert.True(t, byID(all, e.kbank).Archived)
	for _, a := range all {
		assert.NotEqual(t, "Expenses", a.Name, "built-ins aren't listed")
	}
}
