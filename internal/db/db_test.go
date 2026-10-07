package db_test

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/db"
	"github.com/amascii/vinance/internal/db/gen"
	"github.com/amascii/vinance/internal/testutil"
)

func exec(t *testing.T, conn *sql.DB, q string, args ...any) error {
	t.Helper()
	_, err := conn.Exec(q, args...)
	return err
}

func mustExec(t *testing.T, conn *sql.DB, q string, args ...any) {
	t.Helper()
	require.NoError(t, exec(t, conn, q, args...))
}

func count(t *testing.T, conn *sql.DB, table string) int {
	t.Helper()
	var n int
	require.NoError(t, conn.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&n))
	return n
}

func TestMigrateSeedsBuiltinAccounts(t *testing.T) {
	conn := testutil.NewDB(t)
	accts, err := gen.New(conn).ListAccounts(context.Background())
	require.NoError(t, err)

	got := map[string]string{}
	for _, a := range accts {
		assert.EqualValues(t, 1, a.Builtin, a.Name)
		assert.False(t, a.Currency.Valid, a.Name)
		got[a.Slug] = a.Type
	}
	assert.Equal(t, map[string]string{
		"expenses": "expense", "income": "income", "equity": "equity", "imbalance": "imbalance",
	}, got)
}

func TestMigrateIsIdempotent(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "x.db"))
	require.NoError(t, err)
	defer conn.Close()
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	require.NoError(t, db.Migrate(context.Background(), conn, log))
	require.NoError(t, db.Migrate(context.Background(), conn, log))
	assert.Equal(t, 4, count(t, conn, "accounts")) // seed not duplicated
}

func TestOpenEnablesForeignKeysAndWAL(t *testing.T) {
	conn := testutil.NewDB(t)
	var fk int
	require.NoError(t, conn.QueryRow("PRAGMA foreign_keys").Scan(&fk))
	assert.Equal(t, 1, fk)
	var mode string
	require.NoError(t, conn.QueryRow("PRAGMA journal_mode").Scan(&mode))
	assert.Equal(t, "wal", mode)
}

func TestAccountConstraints(t *testing.T) {
	conn := testutil.NewDB(t)
	ins := "INSERT INTO accounts (name, slug, type, currency) VALUES (?, ?, ?, ?)"
	mustExec(t, conn, ins, "BRISK", "brisk", "liability", "USD")

	tests := []struct {
		name string
		args []any
	}{
		{"duplicate name", []any{"BRISK", "bilt2", "liability", "USD"}},
		{"duplicate slug", []any{"Brisk 2", "brisk", "liability", "USD"}},
		{"bad type", []any{"X", "x", "bogus", "USD"}},
		{"uppercase slug", []any{"X", "Xx", "asset", "USD"}},
		{"slug with space", []any{"X", "x y", "asset", "USD"}},
		{"empty slug", []any{"X", "", "asset", "USD"}},
		{"real account without currency", []any{"X", "x", "asset", nil}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Error(t, exec(t, conn, ins, tc.args...))
		})
	}

	t.Run("builtin must not have a currency", func(t *testing.T) {
		assert.Error(t, exec(t, conn,
			"INSERT INTO accounts (name, slug, type, currency, builtin) VALUES ('Z','z','expense','USD',1)"))
	})
}

func TestTagNameConstraints(t *testing.T) {
	conn := testutil.NewDB(t)
	for _, ok := range []string{"snacks", "food-truck", "pc-tech", "café"} {
		assert.NoError(t, exec(t, conn, "INSERT INTO tags (name) VALUES (?)", ok), ok)
	}
	for _, bad := range []string{"", "Snacks", "food truck", "#snacks", "snacks "} {
		assert.Error(t, exec(t, conn, "INSERT INTO tags (name) VALUES (?)", bad), "%q", bad)
	}
	assert.Error(t, exec(t, conn, "INSERT INTO tags (name) VALUES ('snacks')"), "duplicate")
}

func TestTransactionConstraints(t *testing.T) {
	conn := testutil.NewDB(t)
	ins := "INSERT INTO transactions (date, description, currency, gnucash_id) VALUES (?, 'x', 'USD', ?)"
	assert.NoError(t, exec(t, conn, ins, "2026-09-24", nil))
	assert.NoError(t, exec(t, conn, ins, "2026-09-25", nil), "multiple NULL gnucash_ids are allowed")
	assert.NoError(t, exec(t, conn, ins, "2026-09-25", "abc"))
	assert.Error(t, exec(t, conn, ins, "2026-09-25", "abc"), "duplicate gnucash_id")
	for _, bad := range []string{"09/24/2026", "2026-9-24", "yesterday", ""} {
		assert.Error(t, exec(t, conn, ins, bad, nil), "%q", bad)
	}
}

// seedSplit creates one transaction with one split on the Expenses account and one tag.
func seedSplit(t *testing.T, conn *sql.DB) (txnID, splitID int64) {
	t.Helper()
	res, err := conn.Exec("INSERT INTO transactions (date, description, currency) VALUES ('2026-09-24','Walmart','USD')")
	require.NoError(t, err)
	txnID, _ = res.LastInsertId()
	res, err = conn.Exec(`INSERT INTO splits (transaction_id, account_id, position, currency, amount, value)
		VALUES (?, (SELECT id FROM accounts WHERE slug='expenses'), 0, 'USD', 650, 650)`, txnID)
	require.NoError(t, err)
	splitID, _ = res.LastInsertId()
	mustExec(t, conn, "INSERT INTO tags (name) VALUES ('snacks')")
	mustExec(t, conn, "INSERT INTO split_tags (split_id, tag_id) VALUES (?, (SELECT id FROM tags WHERE name='snacks'))", splitID)
	return txnID, splitID
}

func TestSplitForeignKeysAndCascade(t *testing.T) {
	conn := testutil.NewDB(t)
	txnID, _ := seedSplit(t, conn)

	assert.Error(t, exec(t, conn,
		"INSERT INTO splits (transaction_id, account_id, position, currency, amount, value) VALUES (?, 9999, 1, 'USD', 1, 1)", txnID),
		"unknown account")
	assert.Error(t, exec(t,
		conn, "INSERT INTO splits (transaction_id, account_id, position, currency, amount, value) VALUES (9999, 1, 1, 'USD', 1, 1)"),
		"unknown transaction")
	assert.Error(t, exec(t, conn,
		"INSERT INTO splits (transaction_id, account_id, position, currency, amount, value, reconciled) VALUES (?, 1, 1, 'USD', 1, 1, 'q')", txnID),
		"bad reconciled flag")
	assert.Error(t, exec(t, conn, "DELETE FROM accounts WHERE slug='expenses'"), "account in use can't be deleted")
	assert.Error(t, exec(t, conn, "DELETE FROM tags WHERE name='snacks'"), "tag in use can't be deleted")

	mustExec(t, conn, "DELETE FROM transactions WHERE id = ?", txnID)
	assert.Equal(t, 0, count(t, conn, "splits"), "splits cascade with transaction")
	assert.Equal(t, 0, count(t, conn, "split_tags"), "split_tags cascade with split")
	assert.Equal(t, 1, count(t, conn, "tags"), "tags themselves remain")
}

func TestPricesPrimaryKey(t *testing.T) {
	conn := testutil.NewDB(t)
	mustExec(t, conn, "INSERT INTO prices VALUES ('MXN','2026-01-15','0.0562')")
	assert.Error(t, exec(t, conn, "INSERT INTO prices VALUES ('MXN','2026-01-15','0.0570')"))
	assert.NoError(t, exec(t, conn, "INSERT INTO prices VALUES ('MXN','2026-01-16','0.0570')"))
}

func TestGeneratedQueries(t *testing.T) {
	ctx := context.Background()
	q := gen.New(testutil.NewDB(t))

	a, err := q.CreateAccount(ctx, gen.CreateAccountParams{
		Name: "Wharf Bank", Slug: "wharf-bank", Type: "asset", Currency: sql.NullString{String: "USD", Valid: true},
	})
	require.NoError(t, err)
	assert.EqualValues(t, 0, a.Builtin)
	assert.NotEmpty(t, a.CreatedAt)

	got, err := q.GetAccountBySlug(ctx, "wharf-bank")
	require.NoError(t, err)
	assert.Equal(t, a.ID, got.ID)

	exp, err := q.GetBuiltinAccount(ctx, "expense")
	require.NoError(t, err)
	assert.Equal(t, "Expenses", exp.Name)

	all, err := q.ListAccounts(ctx)
	require.NoError(t, err)
	require.Len(t, all, 5)
	assert.EqualValues(t, 1, all[0].Builtin, "built-ins sort first")
	assert.Equal(t, "Wharf Bank", all[4].Name)

	t1, err := q.EnsureTag(ctx, "snacks")
	require.NoError(t, err)
	t2, err := q.EnsureTag(ctx, "snacks")
	require.NoError(t, err)
	assert.Equal(t, t1.ID, t2.ID, "EnsureTag is idempotent")
	tags, err := q.ListTags(ctx)
	require.NoError(t, err)
	assert.Len(t, tags, 1)
}

func TestDescriptionIndexExistsAndIsUsed(t *testing.T) {
	conn := testutil.NewDB(t)
	var name string
	require.NoError(t, conn.QueryRow("SELECT name FROM sqlite_master WHERE type='index' AND name='idx_transactions_description'").Scan(&name))

	rows, err := conn.Query("EXPLAIN QUERY PLAN SELECT id FROM transactions WHERE description = 'x' COLLATE NOCASE ORDER BY date DESC LIMIT 5")
	require.NoError(t, err)
	defer rows.Close()
	var plan string
	for rows.Next() {
		var a, b, c int
		var detail string
		require.NoError(t, rows.Scan(&a, &b, &c, &detail))
		plan += detail + "\n"
	}
	assert.Contains(t, plan, "idx_transactions_description", "the NOCASE lookup uses the index")
}
