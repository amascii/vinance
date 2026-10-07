package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// BulkOp is what a bulk tag change does to the selected transactions.
type BulkOp string

const (
	BulkAdd    BulkOp = "add"    // tag every expense/income (category) line that lacks it
	BulkRemove BulkOp = "remove" // untag the lines that carry it
	BulkMove   BulkOp = "move"   // lines carrying Tag get To instead
)

// MaxBulk caps how many transactions one bulk change may touch.
const MaxBulk = 5000

// ErrTooManyTransactions is returned when a selection exceeds MaxBulk.
var ErrTooManyTransactions = errors.New("too many transactions selected")

// BulkChange describes one bulk tag change. Tag (and To for BulkMove) are normalised with TagSlug.
type BulkChange struct {
	Op  BulkOp
	Tag string
	To  string
}

// BulkResult counts what a change touches (BulkPreview) or touched (BulkApply).
type BulkResult struct {
	Transactions int // transactions with at least one changed line
	Lines        int // split lines that gained or lost a tag
}

func (c BulkChange) normalised() (BulkChange, error) {
	c.Tag, c.To = TagSlug(c.Tag), TagSlug(c.To)
	if c.Tag == "" {
		return c, ErrInvalidTag
	}
	switch c.Op {
	case BulkAdd, BulkRemove:
		c.To = ""
	case BulkMove:
		if c.To == "" || c.To == c.Tag {
			return c, ErrInvalidTag
		}
	default:
		return c, fmt.Errorf("unknown bulk operation %q", c.Op)
	}
	return c, nil
}

type lineRef struct{ split, txn int64 }

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// bulkLines finds the split lines a change would modify among the given transactions. Adding
// applies to category (built-in expense/income) lines only; removing and moving to lines that carry the tag.
func bulkLines(ctx context.Context, q queryer, ids []int64, c BulkChange) ([]lineRef, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > MaxBulk {
		return nil, ErrTooManyTransactions
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids)+1)
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, c.Tag)
	hasTag := `EXISTS (SELECT 1 FROM split_tags st JOIN tags tg ON tg.id = st.tag_id WHERE st.split_id = s.id AND tg.name = ?)`
	query := `SELECT s.id, s.transaction_id FROM splits s JOIN accounts a ON a.id = s.account_id
		WHERE s.transaction_id IN (` + marks + `) AND `
	if c.Op == BulkAdd {
		query += `a.builtin = 1 AND a.type IN ('expense', 'income') AND NOT ` + hasTag
	} else {
		query += hasTag
	}
	rows, err := q.QueryContext(ctx, query+" ORDER BY s.id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []lineRef
	for rows.Next() {
		var l lineRef
		if err := rows.Scan(&l.split, &l.txn); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func resultOf(lines []lineRef) BulkResult {
	txns := map[int64]bool{}
	for _, l := range lines {
		txns[l.txn] = true
	}
	return BulkResult{Transactions: len(txns), Lines: len(lines)}
}

// BulkPreview says how many transactions and lines the change would touch, without changing anything.
func (s *Service) BulkPreview(ctx context.Context, ids []int64, c BulkChange) (BulkResult, error) {
	c, err := c.normalised()
	if err != nil {
		return BulkResult{}, err
	}
	lines, err := bulkLines(ctx, s.db, ids, c)
	return resultOf(lines), err
}

// BulkApply makes the change in one database transaction. Tags that don't exist yet are created.
func (s *Service) BulkApply(ctx context.Context, ids []int64, c BulkChange) (BulkResult, error) {
	c, err := c.normalised()
	if err != nil {
		return BulkResult{}, err
	}
	var res BulkResult
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		lines, err := bulkLines(ctx, tx, ids, c)
		if err != nil {
			return err
		}
		res = resultOf(lines)
		if len(lines) == 0 {
			return nil
		}
		tagID := func(name string) (int64, error) {
			if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO tags (name) VALUES (?)", name); err != nil {
				return 0, err
			}
			var id int64
			err := tx.QueryRowContext(ctx, "SELECT id FROM tags WHERE name = ?", name).Scan(&id)
			return id, err
		}
		from, err := tagID(c.Tag) // for BulkAdd this is the tag being added
		if err != nil {
			return err
		}
		var to int64
		if c.Op == BulkMove {
			if to, err = tagID(c.To); err != nil {
				return err
			}
		}
		touched := map[int64]bool{}
		for _, l := range lines {
			switch c.Op {
			case BulkAdd:
				_, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO split_tags (split_id, tag_id) VALUES (?, ?)", l.split, from)
			case BulkRemove:
				_, err = tx.ExecContext(ctx, "DELETE FROM split_tags WHERE split_id = ? AND tag_id = ?", l.split, from)
			case BulkMove:
				if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO split_tags (split_id, tag_id) VALUES (?, ?)", l.split, to); err == nil {
					_, err = tx.ExecContext(ctx, "DELETE FROM split_tags WHERE split_id = ? AND tag_id = ?", l.split, from)
				}
			}
			if err != nil {
				return err
			}
			if !touched[l.txn] {
				touched[l.txn] = true
				if _, err := tx.ExecContext(ctx, "UPDATE transactions SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE id = ?", l.txn); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return res, err
}

// MatchingIDs returns the ids of every transaction matching the filter (ignoring paging), newest
// first, or ErrTooManyTransactions past MaxBulk.
func (s *Service) MatchingIDs(ctx context.Context, f Filter) ([]int64, error) {
	where, args := f.where()
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf("SELECT t.id FROM transactions t%s ORDER BY t.date DESC, t.id DESC LIMIT %d", where, MaxBulk+1), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if len(ids) > MaxBulk {
		return nil, ErrTooManyTransactions
	}
	return ids, rows.Err()
}
