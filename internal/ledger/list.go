package ledger

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Filter selects transactions for listing. The zero value matches everything.
type Filter struct {
	Text      string   // case-insensitive substring of description, notes or any split memo
	AccountID int64    // some split is in this account (0 = any)
	Tags      []string // every listed tag appears on some split (kebab-case names, no '#')
	From, To  string   // inclusive YYYY-MM-DD bounds ("" = open)
	Imbalance bool     // some split is in the Imbalance account (needs fixing)
	Untagged  bool     // some Expenses line has no tag at all
	Before    *Cursor  // keyset position: only transactions older than this
	Limit     int      // page size (0 = DefaultPageSize)
}

// DefaultPageSize is used when Filter.Limit is 0.
const DefaultPageSize = 50

// Cursor is a keyset position in the (date DESC, id DESC) ordering.
type Cursor struct {
	Date string
	ID   int64
}

func (c Cursor) String() string { return c.Date + ":" + strconv.FormatInt(c.ID, 10) }

// ParseCursor parses the output of Cursor.String.
func ParseCursor(s string) (Cursor, error) {
	date, id, ok := strings.Cut(s, ":")
	if _, err := time.Parse("2006-01-02", date); !ok || err != nil {
		return Cursor{}, fmt.Errorf("invalid cursor %q", s)
	}
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return Cursor{}, fmt.Errorf("invalid cursor %q", s)
	}
	return Cursor{Date: date, ID: n}, nil
}

// Page is one page of results. Next is nil on the last page.
type Page struct {
	Transactions []Transaction
	Next         *Cursor
}

// likeEscape escapes LIKE wildcards so user text is matched literally (with ESCAPE '\').
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// where builds the WHERE clause (with leading " WHERE") and args for the filter, excluding
// the keyset cursor so Count can share it.
func (f Filter) where() (string, []any) {
	var conds []string
	var args []any
	add := func(cond string, a ...any) {
		conds = append(conds, cond)
		args = append(args, a...)
	}
	if f.From != "" {
		add("t.date >= ?", f.From)
	}
	if f.To != "" {
		add("t.date <= ?", f.To)
	}
	if text := strings.TrimSpace(f.Text); text != "" {
		pat := "%" + likeEscape(text) + "%"
		add(`(t.description LIKE ? ESCAPE '\' OR t.notes LIKE ? ESCAPE '\' OR EXISTS (
			SELECT 1 FROM splits s WHERE s.transaction_id = t.id AND s.memo LIKE ? ESCAPE '\'))`, pat, pat, pat)
	}
	if f.AccountID != 0 {
		add("EXISTS (SELECT 1 FROM splits s WHERE s.transaction_id = t.id AND s.account_id = ?)", f.AccountID)
	}
	for _, tag := range f.Tags {
		add(`EXISTS (SELECT 1 FROM splits s
			JOIN split_tags st ON st.split_id = s.id JOIN tags tg ON tg.id = st.tag_id
			WHERE s.transaction_id = t.id AND tg.name = ?)`, tag)
	}
	if f.Imbalance {
		add(`EXISTS (SELECT 1 FROM splits s JOIN accounts a ON a.id = s.account_id
			WHERE s.transaction_id = t.id AND a.type = 'imbalance')`)
	}
	if f.Untagged {
		add(`EXISTS (SELECT 1 FROM splits s JOIN accounts a ON a.id = s.account_id AND a.builtin = 1 AND a.type = 'expense'
			WHERE s.transaction_id = t.id AND NOT EXISTS (SELECT 1 FROM split_tags st WHERE st.split_id = s.id))`)
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// Count returns how many transactions match the filter (ignoring Before and Limit).
func (s *Service) Count(ctx context.Context, f Filter) (int, error) {
	where, args := f.where()
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM transactions t"+where, args...).Scan(&n)
	return n, err
}

// CurrencyTotal is the net movement of the user's real accounts in one currency.
type CurrencyTotal struct {
	Currency string
	Net      int64 // minor units; negative = money went out
}

// Totals sums, per transaction currency, how the matching transactions moved the real (asset and
// liability) accounts, whichever account that was: the same net each row's Summary shows, added up
// over the whole result set (ignoring Before and Limit). Currencies are never mixed; they come back
// sorted by code.
func (s *Service) Totals(ctx context.Context, f Filter) ([]CurrencyTotal, error) {
	where, args := f.where()
	rows, err := s.db.QueryContext(ctx, `SELECT t.currency, SUM(rs.value)
		FROM transactions t
		JOIN splits rs ON rs.transaction_id = t.id
		JOIN accounts ra ON ra.id = rs.account_id AND ra.type IN ('asset', 'liability')`+where+`
		GROUP BY t.currency ORDER BY t.currency`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CurrencyTotal
	for rows.Next() {
		var c CurrencyTotal
		if err := rows.Scan(&c.Currency, &c.Net); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Active reports whether the filter narrows the results at all (Before and Limit don't count).
func (f Filter) Active() bool {
	where, _ := f.where()
	return where != ""
}

// List returns one page of matching transactions, newest first (date, then id), with splits
// and tags loaded.
func (s *Service) List(ctx context.Context, f Filter) (Page, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultPageSize
	}
	where, args := f.where()
	if f.Before != nil {
		cond := "(t.date < ? OR (t.date = ? AND t.id < ?))"
		if where == "" {
			where = " WHERE " + cond
		} else {
			where += " AND " + cond
		}
		args = append(args, f.Before.Date, f.Before.Date, f.Before.ID)
	}
	args = append(args, limit+1) // one extra row tells us whether there is a next page
	rows, err := s.db.QueryContext(ctx, "SELECT t.id, t.date FROM transactions t"+where+" ORDER BY t.date DESC, t.id DESC LIMIT ?", args...)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	var ids []int64
	var last Cursor
	for rows.Next() {
		var c Cursor
		if err := rows.Scan(&c.ID, &c.Date); err != nil {
			return Page{}, err
		}
		ids = append(ids, c.ID)
		if len(ids) <= limit {
			last = c
		}
	}
	if err := rows.Err(); err != nil {
		return Page{}, err
	}
	var page Page
	if len(ids) > limit {
		ids = ids[:limit]
		page.Next = &last
	}
	if page.Transactions, err = s.loadMany(ctx, ids); err != nil {
		return Page{}, err
	}
	return page, nil
}

// ListRecent returns the newest transactions (by date, then id) with their splits.
func (s *Service) ListRecent(ctx context.Context, limit int) ([]Transaction, error) {
	p, err := s.List(ctx, Filter{Limit: limit})
	return p.Transactions, err
}

func placeholders(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }

// loadMany loads transactions with their splits and tags in three queries, in the order of ids.
// Unknown ids are skipped.
func (s *Service) loadMany(ctx context.Context, ids []int64) ([]Transaction, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	in := placeholders(len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}

	byID := make(map[int64]*Transaction, len(ids))
	trows, err := s.db.QueryContext(ctx, `SELECT id, date, description, currency, notes, gnucash_id, created_at, updated_at
		FROM transactions WHERE id IN (`+in+`)`, args...)
	if err != nil {
		return nil, err
	}
	for trows.Next() {
		var t Transaction
		var gid sql.NullString
		if err := trows.Scan(&t.ID, &t.Date, &t.Description, &t.Currency, &t.Notes, &gid, &t.CreatedAt, &t.UpdatedAt); err != nil {
			trows.Close()
			return nil, err
		}
		t.GnucashID = gid.String
		byID[t.ID] = &t
	}
	if err := trows.Close(); err != nil {
		return nil, err
	}

	srows, err := s.db.QueryContext(ctx, `SELECT s.id, s.transaction_id, s.account_id, a.name, a.slug, a.type,
			s.position, s.memo, s.currency, s.amount, s.value, s.reconciled
		FROM splits s JOIN accounts a ON a.id = s.account_id
		WHERE s.transaction_id IN (`+in+`) ORDER BY s.transaction_id, s.position, s.id`, args...)
	if err != nil {
		return nil, err
	}
	splitAt := map[int64][2]int{} // split id -> (txn id, index) for tag attachment
	for srows.Next() {
		var sp Split
		var txnID int64
		if err := srows.Scan(&sp.ID, &txnID, &sp.AccountID, &sp.AccountName, &sp.AccountSlug, &sp.AccountType,
			&sp.Position, &sp.Memo, &sp.Currency, &sp.Amount, &sp.Value, &sp.Reconciled); err != nil {
			srows.Close()
			return nil, err
		}
		t := byID[txnID]
		splitAt[sp.ID] = [2]int{int(txnID), len(t.Splits)}
		t.Splits = append(t.Splits, sp)
	}
	if err := srows.Close(); err != nil {
		return nil, err
	}

	grows, err := s.db.QueryContext(ctx, `SELECT st.split_id, tg.name
		FROM split_tags st JOIN splits s ON s.id = st.split_id JOIN tags tg ON tg.id = st.tag_id
		WHERE s.transaction_id IN (`+in+`) ORDER BY tg.name`, args...)
	if err != nil {
		return nil, err
	}
	for grows.Next() {
		var splitID int64
		var name string
		if err := grows.Scan(&splitID, &name); err != nil {
			grows.Close()
			return nil, err
		}
		at := splitAt[splitID]
		t := byID[int64(at[0])]
		t.Splits[at[1]].Tags = append(t.Splits[at[1]].Tags, name)
	}
	if err := grows.Close(); err != nil {
		return nil, err
	}

	out := make([]Transaction, 0, len(ids))
	for _, id := range ids {
		if t, ok := byID[id]; ok {
			out = append(out, *t)
		}
	}
	return out, nil
}

// LastLike returns the most recent transaction whose description equals the given one
// (ignoring case) and that moved money the same way: out of the real accounts for an expense,
// into them for income. It returns (nil, nil) when there is none. Quick-add uses it to
// suggest the tags and account you used last time.
func (s *Service) LastLike(ctx context.Context, description string, income bool) (*Transaction, error) {
	description = strings.TrimSpace(description)
	if description == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM transactions
		WHERE description = ? COLLATE NOCASE ORDER BY date DESC, id DESC LIMIT 25`, description)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	txns, err := s.loadMany(ctx, ids)
	if err != nil {
		return nil, err
	}
	want := DirOut
	if income {
		want = DirIn
	}
	for i := range txns {
		if txns[i].Summary().Direction == want {
			return &txns[i], nil
		}
	}
	return nil, nil
}

// PastEntry is a distinct description from history with its most recent transaction.
type PastEntry struct {
	Description string // as last written (case from the newest transaction)
	Count       int    // how many transactions use it
	Last        Transaction
}

// SuggestDescriptions finds past descriptions matching what the user has typed so far: those
// that start with it come first ("fnd" -> "FNDXX Dividends"), then those with a word that does
// ("div" -> "FNDXX Dividends"). Matching ignores case and treats % and _ literally. Within each
// group the most-used description comes first, then the most recent. Each result carries its
// newest transaction so quick-add can reuse its account, tags and amount.
func (s *Service) SuggestDescriptions(ctx context.Context, query string, limit int) ([]PastEntry, error) {
	query = strings.TrimSpace(query)
	if query == "" || limit <= 0 {
		return nil, nil
	}
	esc := likeEscape(query)
	rows, err := s.db.QueryContext(ctx, `SELECT COUNT(*) AS n, MAX(date) AS last_date,
			MIN(CASE WHEN description LIKE ? ESCAPE '\' THEN 0 ELSE 1 END) AS rank,
			(SELECT t2.id FROM transactions t2 WHERE t2.description = t.description COLLATE NOCASE ORDER BY t2.date DESC, t2.id DESC LIMIT 1) AS last_id
		FROM transactions t
		WHERE description LIKE ? ESCAPE '\' OR description LIKE ? ESCAPE '\'
		GROUP BY description COLLATE NOCASE
		ORDER BY rank, n DESC, last_date DESC, description COLLATE NOCASE
		LIMIT ?`, esc+"%", esc+"%", "% "+esc+"%", limit)
	if err != nil {
		return nil, err
	}
	type hit struct {
		n    int
		last int64
	}
	var hits []hit
	for rows.Next() {
		var h hit
		var lastDate string
		var rank int
		if err := rows.Scan(&h.n, &lastDate, &rank, &h.last); err != nil {
			rows.Close()
			return nil, err
		}
		hits = append(hits, h)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	ids := make([]int64, len(hits))
	for i, h := range hits {
		ids[i] = h.last
	}
	txns, err := s.loadMany(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := map[int64]Transaction{}
	for _, t := range txns {
		byID[t.ID] = t
	}
	out := make([]PastEntry, 0, len(hits))
	for _, h := range hits {
		t := byID[h.last]
		out = append(out, PastEntry{Description: t.Description, Count: h.n, Last: t})
	}
	return out, nil
}
