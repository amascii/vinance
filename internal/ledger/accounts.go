package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/amascii/vinance/internal/db/gen"
)

// Errors returned by account operations.
var (
	ErrAccountNotFound = errors.New("account not found")
	ErrAccountExists   = errors.New("an account with that name already exists")
	ErrAccountInUse    = errors.New("account has transactions")
	ErrInvalidAccount  = errors.New("invalid account")
)

// Account is a real (non-built-in) account with its usage.
type Account struct {
	ID       int64
	Name     string
	Slug     string
	Type     string // asset | liability
	Currency string
	Archived bool
	Splits   int // how many transaction lines use it
}

// ListAccounts returns every real account (archived included) with usage, by name.
func (s *Service) ListAccounts(ctx context.Context) ([]Account, error) {
	rows, err := s.q.AccountBalances(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Account, len(rows))
	for i, r := range rows {
		out[i] = Account{ID: r.ID, Name: r.Name, Slug: r.Slug, Type: r.Type, Currency: r.Currency.String, Archived: r.Archived == 1, Splits: int(r.SplitCount)}
	}
	return out, nil
}

// NewAccount is the input for CreateAccount.
type NewAccount struct {
	Name     string
	Type     string // asset | liability
	Currency string
	// Optional opening balance: a positive amount in minor units of Currency (what you hold
	// for an asset, what you owe for a liability), dated OpeningDate (YYYY-MM-DD).
	OpeningBalance int64
	OpeningDate    string
}

func (n NewAccount) validate() (name, slug, currency string, err error) {
	name = strings.TrimSpace(n.Name)
	currency = strings.ToUpper(strings.TrimSpace(n.Currency))
	if name == "" {
		return "", "", "", fmt.Errorf("%w: a name is required", ErrInvalidAccount)
	}
	if len([]rune(name)) > 60 {
		return "", "", "", fmt.Errorf("%w: the name is too long (60 characters max)", ErrInvalidAccount)
	}
	if slug = AccountSlug(name); slug == "" {
		return "", "", "", fmt.Errorf("%w: the name needs at least one letter or number", ErrInvalidAccount)
	}
	if n.Type != "asset" && n.Type != "liability" {
		return "", "", "", fmt.Errorf("%w: type must be asset or liability", ErrInvalidAccount)
	}
	if !ValidCurrency(currency) {
		return "", "", "", fmt.Errorf("%w: currency must be a 3-letter code like USD", ErrInvalidAccount)
	}
	return name, slug, currency, nil
}

// CreateAccount adds an account and, when OpeningBalance is non-zero, an "Opening balance"
// transaction against the built-in Equity account (tagged opening-balance, like imported ones).
func (s *Service) CreateAccount(ctx context.Context, n NewAccount) (Account, error) {
	name, slug, currency, err := n.validate()
	if err != nil {
		return Account{}, err
	}
	if n.OpeningBalance < 0 {
		return Account{}, fmt.Errorf("%w: the opening balance can't be negative", ErrInvalidAccount)
	}
	date := n.OpeningDate
	if n.OpeningBalance != 0 {
		if _, err := time.Parse("2006-01-02", date); err != nil {
			return Account{}, fmt.Errorf("%w: the opening balance needs a date like 2026-01-31", ErrInvalidAccount)
		}
	}

	var acct Account
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		q := s.q.WithTx(tx)
		if err := s.checkUnique(ctx, q, name, slug, 0); err != nil {
			return err
		}
		row, err := q.CreateAccount(ctx, gen.CreateAccountParams{
			Name: name, Slug: slug, Type: n.Type, Currency: sql.NullString{String: currency, Valid: true},
		})
		if err != nil {
			return fmt.Errorf("create account: %w", err)
		}
		acct = Account{ID: row.ID, Name: row.Name, Slug: row.Slug, Type: row.Type, Currency: currency}
		if n.OpeningBalance == 0 {
			return nil
		}
		equity, err := q.GetBuiltinAccount(ctx, "equity")
		if err != nil {
			return fmt.Errorf("equity account: %w", err)
		}
		amount := n.OpeningBalance // debit-positive: an asset holds +X; a liability owes -X
		if n.Type == "liability" {
			amount = -amount
		}
		_, err = s.CreateIn(ctx, tx, TxnInput{
			Date: date, Description: "Opening balance", Currency: currency,
			Splits: []SplitInput{
				{AccountID: row.ID, Currency: currency, Amount: amount, Value: amount},
				{AccountID: equity.ID, Currency: currency, Amount: -amount, Value: -amount, Tags: []string{"opening-balance"}},
			},
		})
		return err
	})
	if err != nil {
		return Account{}, err
	}
	return acct, nil
}

// checkUnique returns ErrAccountExists when another account (not exceptID) has the name or slug.
func (s *Service) checkUnique(ctx context.Context, q *gen.Queries, name, slug string, exceptID int64) error {
	all, err := q.ListAccounts(ctx)
	if err != nil {
		return err
	}
	for _, a := range all {
		if a.ID != exceptID && (strings.EqualFold(a.Name, name) || a.Slug == slug) {
			return fmt.Errorf("%w (%q)", ErrAccountExists, a.Name)
		}
	}
	return nil
}

// AccountUpdate is the input for UpdateAccount.
type AccountUpdate struct {
	Name     string
	Type     string // asset | liability
	Currency string // may only change while the account has no transactions
}

// UpdateAccount renames an account (its @slug follows the name), changes its type, and, while
// it has no transactions, its currency. Built-in accounts can't be edited.
func (s *Service) UpdateAccount(ctx context.Context, id int64, u AccountUpdate) (Account, error) {
	name, slug, currency, err := NewAccount{Name: u.Name, Type: u.Type, Currency: u.Currency}.validate()
	if err != nil {
		return Account{}, err
	}
	var out Account
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		q := s.q.WithTx(tx)
		cur, err := q.GetAccount(ctx, id)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && cur.Builtin == 1) {
			return ErrAccountNotFound
		} else if err != nil {
			return err
		}
		var splits int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM splits WHERE account_id = ?", id).Scan(&splits); err != nil {
			return err
		}
		if currency != cur.Currency.String && splits > 0 {
			return fmt.Errorf("%w: the currency can't change once the account has transactions", ErrAccountInUse)
		}
		if err := s.checkUnique(ctx, q, name, slug, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE accounts SET name = ?, slug = ?, type = ?, currency = ? WHERE id = ?",
			name, slug, u.Type, currency, id); err != nil {
			return fmt.Errorf("update account: %w", err)
		}
		out = Account{ID: id, Name: name, Slug: slug, Type: u.Type, Currency: currency, Archived: cur.Archived == 1, Splits: splits}
		return nil
	})
	return out, err
}

// SetAccountArchived hides (or restores) an account in quick-add and pickers. Its history and
// balance are untouched.
func (s *Service) SetAccountArchived(ctx context.Context, id int64, archived bool) error {
	flag := 0
	if archived {
		flag = 1
	}
	res, err := s.db.ExecContext(ctx, "UPDATE accounts SET archived = ? WHERE id = ? AND builtin = 0", flag, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrAccountNotFound
	}
	return nil
}

// DeleteAccount removes an account that has never been used. Accounts with transactions
// should be archived instead.
func (s *Service) DeleteAccount(ctx context.Context, id int64) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		var builtin int
		switch err := tx.QueryRowContext(ctx, "SELECT builtin FROM accounts WHERE id = ?", id).Scan(&builtin); {
		case errors.Is(err, sql.ErrNoRows) || (err == nil && builtin == 1):
			return ErrAccountNotFound
		case err != nil:
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM splits WHERE account_id = ?", id).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrAccountInUse
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM accounts WHERE id = ?", id)
		return err
	})
}
