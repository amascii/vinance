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

// SplitInput is one line of a transaction. Amount is in Currency (the account's currency);
// Value is in the transaction's currency. For same-currency splits Amount == Value.
// Sign is debit-positive: money leaving a card/bank is negative, the matching expense positive.
type SplitInput struct {
	AccountID  int64
	Memo       string
	Currency   string
	Amount     int64
	Value      int64
	Reconciled string // "n" (default), "c" or "y"
	Tags       []string
}

// TxnInput is what callers pass to Create/Update.
type TxnInput struct {
	Date        string // YYYY-MM-DD
	Description string
	Currency    string // currency Values are expressed in
	Notes       string
	GnucashID   string // optional; set only on rows imported from GnuCash (importer removed)
	Splits      []SplitInput
}

// Split is a stored split with display info joined in.
type Split struct {
	ID          int64
	AccountID   int64
	AccountName string
	AccountSlug string
	AccountType string
	Position    int
	Memo        string
	Currency    string
	Amount      int64
	Value       int64
	Reconciled  string
	Tags        []string
}

// Transaction is a stored transaction with its splits.
type Transaction struct {
	ID          int64
	Date        string
	Description string
	Currency    string
	Notes       string
	GnucashID   string
	CreatedAt   string
	UpdatedAt   string
	Splits      []Split
}

// ErrNotFound is returned when a transaction doesn't exist.
var ErrNotFound = errors.New("transaction not found")

// ErrUnbalanced is wrapped by ValidationError when split values don't sum to zero.
var ErrUnbalanced = errors.New("splits do not balance")

// ValidationError lists everything wrong with a TxnInput, so the UI can show it all at once.
type ValidationError struct {
	Problems []string
	Err      error // ErrUnbalanced when the values don't sum to zero, else nil
}

func (e *ValidationError) Error() string {
	return "invalid transaction: " + strings.Join(e.Problems, "; ")
}
func (e *ValidationError) Unwrap() error { return e.Err }

// Remaining is the amount still to allocate: the negated sum of split values. Zero means balanced.
// The editor shows this live.
func Remaining(splits []SplitInput) int64 {
	var sum int64
	for _, s := range splits {
		sum += s.Value
	}
	return -sum
}

// Normalize returns a copy of in with trimmed text, default flags and cleaned tags.
func Normalize(in TxnInput) TxnInput {
	out := in
	out.Date = strings.TrimSpace(in.Date)
	out.Description = strings.TrimSpace(in.Description)
	out.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	out.Notes = strings.TrimSpace(in.Notes)
	out.Splits = make([]SplitInput, len(in.Splits))
	for i, s := range in.Splits {
		s.Currency = strings.ToUpper(strings.TrimSpace(s.Currency))
		s.Memo = strings.TrimSpace(s.Memo)
		if s.Reconciled == "" {
			s.Reconciled = "n"
		}
		s.Tags = cleanTags(s.Tags)
		out.Splits[i] = s
	}
	return out
}

func cleanTags(tags []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range tags {
		t = TagSlug(t)
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// accountLookup resolves an account's currency for validation. Real accounts return their
// currency; built-in category accounts return "" (any currency).
type accountLookup func(id int64) (currency string, found bool)

// Validate checks the pure rules on an already-normalised input. lookup may be nil to skip
// account checks (used by unit tests).
func Validate(in TxnInput, lookup accountLookup) error {
	var problems []string
	add := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }

	if _, err := time.Parse("2006-01-02", in.Date); err != nil {
		add("date %q is not a valid YYYY-MM-DD date", in.Date)
	}
	if in.Description == "" {
		add("description is required")
	}
	if !ValidCurrency(in.Currency) {
		add("transaction currency %q is invalid", in.Currency)
	}
	if len(in.Splits) < 2 {
		add("a transaction needs at least 2 splits")
	}
	for i, s := range in.Splits {
		n := i + 1
		if !ValidCurrency(s.Currency) {
			add("split %d: currency %q is invalid", n, s.Currency)
		} else if s.Currency == in.Currency && s.Amount != s.Value {
			add("split %d: amount and value must match when the split is in the transaction currency", n)
		}
		switch s.Reconciled {
		case "n", "c", "y":
		default:
			add("split %d: reconciled flag %q must be n, c or y", n, s.Reconciled)
		}
		if lookup != nil {
			cur, found := lookup(s.AccountID)
			switch {
			case !found:
				add("split %d: account %d does not exist", n, s.AccountID)
			case cur != "" && cur != s.Currency:
				add("split %d: account currency is %s but the split is in %s", n, cur, s.Currency)
			}
		}
	}

	var unbalanced error
	if rem := Remaining(in.Splits); rem != 0 && len(in.Splits) >= 2 {
		add("splits are off by %s (values must sum to zero)", FormatPlain(rem, in.Currency))
		unbalanced = ErrUnbalanced
	}
	if len(problems) == 0 {
		return nil
	}
	return &ValidationError{Problems: problems, Err: unbalanced}
}

// Service creates, reads, updates and deletes transactions, enforcing the ledger rules.
type Service struct {
	db *sql.DB
	q  *gen.Queries
}

func NewService(db *sql.DB) *Service { return &Service{db: db, q: gen.New(db)} }

// Create validates and stores a transaction in its own DB transaction and returns its id.
func (s *Service) Create(ctx context.Context, in TxnInput) (int64, error) {
	var id int64
	err := s.inTx(ctx, func(tx *sql.Tx) (err error) {
		id, err = s.CreateIn(ctx, tx, in)
		return err
	})
	return id, err
}

// CreateIn is Create within a caller-owned DB transaction (lets a caller make several creates
// atomic).
func (s *Service) CreateIn(ctx context.Context, tx *sql.Tx, in TxnInput) (int64, error) {
	q := s.q.WithTx(tx)
	in = Normalize(in)
	if err := Validate(in, s.lookup(ctx, q)); err != nil {
		return 0, err
	}
	var gid sql.NullString
	if in.GnucashID != "" {
		gid = sql.NullString{String: in.GnucashID, Valid: true}
	}
	t, err := q.CreateTransaction(ctx, gen.CreateTransactionParams{
		Date: in.Date, Description: in.Description, Currency: in.Currency, Notes: in.Notes, GnucashID: gid,
	})
	if err != nil {
		return 0, fmt.Errorf("insert transaction: %w", err)
	}
	if err := s.insertSplits(ctx, q, t.ID, in.Splits); err != nil {
		return 0, err
	}
	return t.ID, nil
}

// Update replaces a transaction's fields and all of its splits (and their tags).
func (s *Service) Update(ctx context.Context, id int64, in TxnInput) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		q := s.q.WithTx(tx)
		in = Normalize(in)
		if err := Validate(in, s.lookup(ctx, q)); err != nil {
			return err
		}
		if _, err := q.GetTransaction(ctx, id); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		err := q.UpdateTransaction(ctx, gen.UpdateTransactionParams{
			Date: in.Date, Description: in.Description, Currency: in.Currency, Notes: in.Notes, ID: id,
		})
		if err != nil {
			return fmt.Errorf("update transaction: %w", err)
		}
		if err := q.DeleteSplitsByTransaction(ctx, id); err != nil {
			return fmt.Errorf("clear splits: %w", err)
		}
		return s.insertSplits(ctx, q, id, in.Splits)
	})
}

// Delete removes a transaction; its splits and split tags cascade.
func (s *Service) Delete(ctx context.Context, id int64) error {
	n, err := s.q.DeleteTransaction(ctx, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Get loads a transaction with its splits and tags.
func (s *Service) Get(ctx context.Context, id int64) (Transaction, error) {
	ts, err := s.loadMany(ctx, []int64{id})
	if err != nil {
		return Transaction{}, err
	}
	if len(ts) == 0 {
		return Transaction{}, ErrNotFound
	}
	return ts[0], nil
}

func (s *Service) insertSplits(ctx context.Context, q *gen.Queries, txnID int64, splits []SplitInput) error {
	for i, sp := range splits {
		row, err := q.CreateSplit(ctx, gen.CreateSplitParams{
			TransactionID: txnID, AccountID: sp.AccountID, Position: int64(i), Memo: sp.Memo,
			Currency: sp.Currency, Amount: sp.Amount, Value: sp.Value, Reconciled: sp.Reconciled,
		})
		if err != nil {
			return fmt.Errorf("insert split %d: %w", i+1, err)
		}
		for _, name := range sp.Tags {
			tag, err := q.EnsureTag(ctx, name)
			if err != nil {
				return fmt.Errorf("ensure tag %q: %w", name, err)
			}
			if err := q.AddSplitTag(ctx, gen.AddSplitTagParams{SplitID: row.ID, TagID: tag.ID}); err != nil {
				return fmt.Errorf("tag split: %w", err)
			}
		}
	}
	return nil
}

// lookup returns an accountLookup backed by the DB, caching per call.
func (s *Service) lookup(ctx context.Context, q *gen.Queries) accountLookup {
	cache := map[int64]*gen.Account{}
	return func(id int64) (string, bool) {
		a, ok := cache[id]
		if !ok {
			row, err := q.GetAccount(ctx, id)
			if err == nil {
				a = &row
			}
			cache[id] = a
		}
		if a == nil {
			return "", false
		}
		return a.Currency.String, true
	}
}

func (s *Service) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
