package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Errors returned by recurring-rule operations.
var (
	ErrRuleNotFound = errors.New("recurring rule not found")
	ErrNotDue       = errors.New("that occurrence is not the next one due")
	ErrAlreadyAdded = errors.New("that occurrence was already added")
	ErrInvalidRule  = errors.New("invalid recurring rule")
)

// Rule is a recurring entry: a quick-add line plus a schedule. Nothing is created
// automatically; due occurrences are offered to be added or skipped.
type Rule struct {
	ID        int64
	Text      string // quick-add text without a date, e.g. "1500 Rent #rent @wharf-bank"
	Schedule  Schedule
	NextIndex int
	Active    bool
}

// NextDue is the date of the next occurrence not yet added or skipped ("" once the rule has ended).
func (r Rule) NextDue() string {
	d := r.Schedule.Occurrence(r.NextIndex)
	if !r.Schedule.Within(d) {
		return ""
	}
	return d
}

// DueItem is one occurrence that is due on or before a given day.
type DueItem struct {
	Rule Rule
	Date string
}

// maxDuePerRule caps how many overdue occurrences of one rule are listed at once; as they
// are added or skipped, the following ones appear.
const maxDuePerRule = 12

func scanRule(row interface{ Scan(...any) error }) (Rule, error) {
	var r Rule
	var end sql.NullString
	var active int
	var freq string
	err := row.Scan(&r.ID, &r.Text, &freq, &r.Schedule.Every, &r.Schedule.Anchor, &r.NextIndex, &end, &active)
	r.Schedule.Freq, r.Schedule.End, r.Active = Freq(freq), end.String, active == 1
	return r, err
}

const ruleColumns = "id, text, freq, every, anchor_date, next_index, end_date, active"

func checkRule(text string, sch Schedule) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("%w: enter what to add, e.g. 1500 Rent #rent @wharf-bank", ErrInvalidRule)
	}
	if err := sch.Validate(); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidRule, err)
	}
	return text, nil
}

// CreateRule stores a new rule. Its first occurrence is the schedule's anchor date.
func (s *Service) CreateRule(ctx context.Context, text string, sch Schedule) (Rule, error) {
	text, err := checkRule(text, sch)
	if err != nil {
		return Rule{}, err
	}
	var end any
	if sch.End != "" {
		end = sch.End
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO recurring (text, freq, every, anchor_date, end_date) VALUES (?, ?, ?, ?, ?)`,
		text, string(sch.Freq), sch.Every, sch.Anchor, end)
	if err != nil {
		return Rule{}, fmt.Errorf("create rule: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.GetRule(ctx, id)
}

// GetRule loads one rule.
func (s *Service) GetRule(ctx context.Context, id int64) (Rule, error) {
	r, err := scanRule(s.db.QueryRowContext(ctx, "SELECT "+ruleColumns+" FROM recurring WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Rule{}, ErrRuleNotFound
	}
	return r, err
}

// ListRules returns every rule, active first, then by text.
func (s *Service) ListRules(ctx context.Context) ([]Rule, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+ruleColumns+" FROM recurring ORDER BY active DESC, text COLLATE NOCASE, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Rule
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpdateRule changes a rule's text and schedule. If the schedule's shape (frequency, interval
// or start date) changes, the next occurrence restarts at the first one on or after today, so
// editing a rule never resurrects dates that have already passed.
func (s *Service) UpdateRule(ctx context.Context, id int64, text string, sch Schedule, today string) (Rule, error) {
	text, err := checkRule(text, sch)
	if err != nil {
		return Rule{}, err
	}
	old, err := s.GetRule(ctx, id)
	if err != nil {
		return Rule{}, err
	}
	next := old.NextIndex
	if old.Schedule.Freq != sch.Freq || old.Schedule.Every != sch.Every || old.Schedule.Anchor != sch.Anchor {
		next = 0
		for sch.Occurrence(next) < today {
			next++
		}
	}
	var end any
	if sch.End != "" {
		end = sch.End
	}
	_, err = s.db.ExecContext(ctx, `UPDATE recurring SET text = ?, freq = ?, every = ?, anchor_date = ?, end_date = ?, next_index = ? WHERE id = ?`,
		text, string(sch.Freq), sch.Every, sch.Anchor, end, next, id)
	if err != nil {
		return Rule{}, err
	}
	return s.GetRule(ctx, id)
}

// SetRuleActive pauses or resumes a rule. While paused it offers nothing; on resume,
// occurrences that came due in the meantime are offered (skip them if they no longer apply).
func (s *Service) SetRuleActive(ctx context.Context, id int64, active bool) error {
	flag := 0
	if active {
		flag = 1
	}
	res, err := s.db.ExecContext(ctx, "UPDATE recurring SET active = ? WHERE id = ?", flag, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrRuleNotFound
	}
	return nil
}

// DeleteRule removes a rule. Transactions already created from it stay.
func (s *Service) DeleteRule(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM recurring WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrRuleNotFound
	}
	return nil
}

// Due lists the occurrences of active rules that are due on or before today, oldest first.
func (s *Service) Due(ctx context.Context, today string) ([]DueItem, error) {
	rules, err := s.ListRules(ctx)
	if err != nil {
		return nil, err
	}
	var out []DueItem
	for _, r := range rules {
		if !r.Active {
			continue
		}
		for k := r.NextIndex; k < r.NextIndex+maxDuePerRule; k++ {
			d := r.Schedule.Occurrence(k)
			if d > today || !r.Schedule.Within(d) {
				break
			}
			out = append(out, DueItem{Rule: r, Date: d})
		}
	}
	// stable: by date, then rule order
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Date < out[j-1].Date; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}

// DueCount is how many occurrences are due (capped per rule like Due).
func (s *Service) DueCount(ctx context.Context, today string) (int, error) {
	items, err := s.Due(ctx, today)
	return len(items), err
}

// nextOccurrence checks that forDate is the rule's next occurrence, inside the DB transaction.
func nextOccurrence(ctx context.Context, tx *sql.Tx, id int64, forDate string) (Rule, error) {
	r, err := scanRule(tx.QueryRowContext(ctx, "SELECT "+ruleColumns+" FROM recurring WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Rule{}, ErrRuleNotFound
	}
	if err != nil {
		return Rule{}, err
	}
	if !r.Active || r.NextDue() != forDate {
		return r, ErrNotDue
	}
	return r, nil
}

// AcceptDue adds the transaction for a due occurrence and advances the rule, atomically. forDate
// must be the rule's next occurrence (a stale page or a double-click gets ErrNotDue / ErrAlreadyAdded
// and changes nothing). The caller builds `in` from the rule's text with that date.
func (s *Service) AcceptDue(ctx context.Context, id int64, forDate string, in TxnInput) (int64, error) {
	var txnID int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		r, err := nextOccurrence(ctx, tx, id, forDate)
		if err != nil {
			return err
		}
		if txnID, err = s.CreateIn(ctx, tx, in); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE transactions SET recurring_id = ?, recurring_for = ? WHERE id = ?", id, forDate, txnID); err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return ErrAlreadyAdded
			}
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE recurring SET next_index = ? WHERE id = ?", r.NextIndex+1, id)
		return err
	})
	return txnID, err
}

// SkipDue moves past a due occurrence without adding anything.
func (s *Service) SkipDue(ctx context.Context, id int64, forDate string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		r, err := nextOccurrence(ctx, tx, id, forDate)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE recurring SET next_index = ? WHERE id = ?", r.NextIndex+1, id)
		return err
	})
}
