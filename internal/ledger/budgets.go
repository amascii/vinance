package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Errors returned by budget operations.
var (
	ErrBudgetNotFound = errors.New("budget not found")
	ErrInvalidBudget  = errors.New("invalid budget")
)

// Budget is a monthly spending limit, in USD cents, for one tag.
type Budget struct {
	ID         int64
	Tag        string
	MonthlyUSD int64
}

// ListBudgets returns every budget by tag name.
func (s *Service) ListBudgets(ctx context.Context) ([]Budget, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT b.id, t.name, b.monthly_usd FROM budgets b JOIN tags t ON t.id = b.tag_id ORDER BY t.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Budget
	for rows.Next() {
		var b Budget
		if err := rows.Scan(&b.ID, &b.Tag, &b.MonthlyUSD); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// SetBudget creates or changes the monthly budget (USD cents, > 0) for a tag. The tag name is
// normalised; a tag that doesn't exist yet is created, so a budget can precede its first use.
func (s *Service) SetBudget(ctx context.Context, tag string, monthlyUSD int64) (Budget, error) {
	name := TagSlug(tag)
	if name == "" {
		return Budget{}, fmt.Errorf("%w: choose a tag", ErrInvalidBudget)
	}
	if monthlyUSD <= 0 {
		return Budget{}, fmt.Errorf("%w: the monthly amount must be greater than zero", ErrInvalidBudget)
	}
	var b Budget
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var tagID int64
		err := tx.QueryRowContext(ctx, `INSERT INTO tags (name) VALUES (?) ON CONFLICT (name) DO UPDATE SET name = excluded.name RETURNING id`, name).Scan(&tagID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO budgets (tag_id, monthly_usd) VALUES (?, ?)
			ON CONFLICT (tag_id) DO UPDATE SET monthly_usd = excluded.monthly_usd`, tagID, monthlyUSD); err != nil {
			return err
		}
		b.Tag, b.MonthlyUSD = name, monthlyUSD
		return tx.QueryRowContext(ctx, "SELECT id FROM budgets WHERE tag_id = ?", tagID).Scan(&b.ID)
	})
	return b, err
}

// DeleteBudget removes the budget for a tag (the tag itself stays).
func (s *Service) DeleteBudget(ctx context.Context, tag string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM budgets WHERE tag_id = (SELECT id FROM tags WHERE name = ?)`, tag)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrBudgetNotFound
	}
	return nil
}

// BudgetLine is a budget measured against one month's spending.
type BudgetLine struct {
	Budget
	SpentUSD int64
	Items    int // lines (splits) counted
}

// Remaining is the budget left (negative when over).
func (l BudgetLine) Remaining() int64 { return l.MonthlyUSD - l.SpentUSD }

// Percent is how much of the budget is used, e.g. 87; it exceeds 100 when over budget and is
// never negative (refunds can make spending negative).
func (l BudgetLine) Percent() int {
	if l.SpentUSD <= 0 || l.MonthlyUSD <= 0 {
		return 0
	}
	return int(l.SpentUSD * 100 / l.MonthlyUSD)
}

// BudgetStatus is every budget measured against one month.
type BudgetStatus struct {
	Month         string       // YYYY-MM
	From, To      string       // first and last day of the month
	Lines         []BudgetLine // most used first (by percent), then by tag
	TotalBudget   int64        // sum of the budgets
	TotalSpending int64        // all spending in the month, budgeted or not (USD)
	Missing       []string     // currencies without an exchange rate (left out of USD figures)
}

// MonthBounds returns the first and last day of a YYYY-MM month.
func MonthBounds(month string) (from, to string, err error) {
	first, err := time.Parse("2006-01", month)
	if err != nil {
		return "", "", fmt.Errorf("month must look like 2026-10")
	}
	return first.Format("2006-01-02"), first.AddDate(0, 1, -1).Format("2006-01-02"), nil
}

// BudgetStatus measures every budget against the spending of the given month, using the same
// aggregation (and exchange-rate rules) as TagReport.
func (s *Service) BudgetStatus(ctx context.Context, month string) (BudgetStatus, error) {
	from, to, err := MonthBounds(month)
	if err != nil {
		return BudgetStatus{}, err
	}
	budgets, err := s.ListBudgets(ctx)
	if err != nil {
		return BudgetStatus{}, err
	}
	rep, err := s.TagReport(ctx, ReportFilter{From: from, To: to})
	if err != nil {
		return BudgetStatus{}, err
	}
	spent := map[string]TagLine{}
	for _, l := range rep.Lines {
		spent[l.Tag] = l
	}
	st := BudgetStatus{Month: month, From: from, To: to, TotalSpending: rep.Total.USD, Missing: rep.Missing}
	for _, b := range budgets {
		line := BudgetLine{Budget: b, SpentUSD: spent[b.Tag].USD, Items: spent[b.Tag].Splits}
		st.Lines = append(st.Lines, line)
		st.TotalBudget += b.MonthlyUSD
	}
	for i := 1; i < len(st.Lines); i++ { // insertion sort: percent desc, then tag
		for j := i; j > 0; j-- {
			a, b := st.Lines[j-1], st.Lines[j]
			if a.Percent() > b.Percent() || (a.Percent() == b.Percent() && a.Tag < b.Tag) {
				break
			}
			st.Lines[j-1], st.Lines[j] = b, a
		}
	}
	return st, nil
}

// MonthProgress is how far through the month `today` is, as a fraction in [0,1]: 0 for a month
// that hasn't started, 1 for one that has ended, otherwise day-of-month / days-in-month.
func MonthProgress(month, today string) float64 {
	from, to, err := MonthBounds(month)
	if err != nil {
		return 0
	}
	switch {
	case today < from:
		return 0
	case today > to:
		return 1
	}
	first, _ := time.Parse("2006-01-02", from)
	t, _ := time.Parse("2006-01-02", today)
	return float64(t.Day()) / float64(daysIn(first.Year(), first.Month()))
}
