// Package testutil holds shared helpers for tests. Use synthetic data only.
package testutil

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/amascii/vinance/internal/db"
	"github.com/amascii/vinance/internal/db/gen"
	"github.com/amascii/vinance/internal/ledger"
	"github.com/amascii/vinance/internal/logging"
	"github.com/amascii/vinance/internal/web"
)

func quietLogger() *slog.Logger { return logging.New(io.Discard, slog.LevelError) }

// NewDB returns a fresh, migrated SQLite database in a temp dir, closed when the test ends.
func NewDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Migrate(context.Background(), conn, quietLogger()); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	return conn
}

// Today is the fixed "now" test servers use: Thursday 2026-10-01.
var Today = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

// NewServerWithDB starts the real router against a fresh temp DB and returns both.
// The server's clock is fixed at Today.
func NewServerWithDB(t *testing.T) (*httptest.Server, *sql.DB) {
	t.Helper()
	conn := NewDB(t)
	srv := httptest.NewServer(web.NewRouter(quietLogger(), conn, web.WithClock(func() time.Time { return Today })))
	t.Cleanup(srv.Close)
	return srv, conn
}

// NewServer is NewServerWithDB for tests that don't need the DB handle.
func NewServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv, _ := NewServerWithDB(t)
	return srv
}

// SeedAccount creates a real account and returns its id.
func SeedAccount(t *testing.T, conn *sql.DB, name, typ, currency string) int64 {
	t.Helper()
	a, err := gen.New(conn).CreateAccount(context.Background(), gen.CreateAccountParams{
		Name: name, Slug: ledger.AccountSlug(name), Type: typ, Currency: sql.NullString{String: currency, Valid: true},
	})
	if err != nil {
		t.Fatalf("seed account %q: %v", name, err)
	}
	return a.ID
}

// SeedDefaultAccounts creates BRISK (USD card), Wharf Bank (USD bank) and Neo (MXN bank).
func SeedDefaultAccounts(t *testing.T, conn *sql.DB) {
	t.Helper()
	SeedAccount(t, conn, "BRISK", "liability", "USD")
	SeedAccount(t, conn, "Wharf Bank", "asset", "USD")
	SeedAccount(t, conn, "Neo", "asset", "MXN")
}

// SeedSpend records an expense on the BRISK account (create it with SeedDefaultAccounts first).
func SeedSpend(t *testing.T, conn *sql.DB, date, description string, cents int64, tags ...string) {
	t.Helper()
	ctx := context.Background()
	q := gen.New(conn)
	brisk, err := q.GetAccountBySlug(ctx, "brisk")
	if err != nil {
		t.Fatalf("seed spend: no BRISK account: %v", err)
	}
	exp, err := q.GetBuiltinAccount(ctx, "expense")
	if err != nil {
		t.Fatalf("seed spend: %v", err)
	}
	_, err = ledger.NewService(conn).Create(ctx, ledger.TxnInput{
		Date: date, Description: description, Currency: "USD",
		Splits: []ledger.SplitInput{
			{AccountID: brisk.ID, Currency: "USD", Amount: -cents, Value: -cents},
			{AccountID: exp.ID, Currency: "USD", Amount: cents, Value: cents, Tags: tags},
		},
	})
	if err != nil {
		t.Fatalf("seed spend %q: %v", description, err)
	}
}

// SeedImbalanced records a BRISK charge whose other side sits in the Imbalance account,
// like the leftovers GnuCash creates for transactions it couldn't categorise.
func SeedImbalanced(t *testing.T, conn *sql.DB, date, description string, cents int64) {
	t.Helper()
	ctx := context.Background()
	q := gen.New(conn)
	brisk, err := q.GetAccountBySlug(ctx, "brisk")
	if err != nil {
		t.Fatalf("seed imbalanced: no BRISK account: %v", err)
	}
	imb, err := q.GetBuiltinAccount(ctx, "imbalance")
	if err != nil {
		t.Fatalf("seed imbalanced: %v", err)
	}
	_, err = ledger.NewService(conn).Create(ctx, ledger.TxnInput{
		Date: date, Description: description, Currency: "USD",
		Splits: []ledger.SplitInput{
			{AccountID: brisk.ID, Currency: "USD", Amount: -cents, Value: -cents},
			{AccountID: imb.ID, Currency: "USD", Amount: cents, Value: cents},
		},
	})
	if err != nil {
		t.Fatalf("seed imbalanced %q: %v", description, err)
	}
}

// SeedIncome records income into the Wharf Bank account (create it with SeedDefaultAccounts first).
func SeedIncome(t *testing.T, conn *sql.DB, date, description string, cents int64, tags ...string) {
	t.Helper()
	ctx := context.Background()
	q := gen.New(conn)
	wharf, err := q.GetAccountBySlug(ctx, "wharf-bank")
	if err != nil {
		t.Fatalf("seed income: no Wharf Bank account: %v", err)
	}
	inc, err := q.GetBuiltinAccount(ctx, "income")
	if err != nil {
		t.Fatalf("seed income: %v", err)
	}
	_, err = ledger.NewService(conn).Create(ctx, ledger.TxnInput{
		Date: date, Description: description, Currency: "USD",
		Splits: []ledger.SplitInput{
			{AccountID: wharf.ID, Currency: "USD", Amount: cents, Value: cents},
			{AccountID: inc.ID, Currency: "USD", Amount: -cents, Value: -cents, Tags: tags},
		},
	})
	if err != nil {
		t.Fatalf("seed income %q: %v", description, err)
	}
}
