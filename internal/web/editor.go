package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/amascii/vinance/internal/db/gen"
	"github.com/amascii/vinance/internal/ledger"
	"github.com/amascii/vinance/internal/web/views"
)

// formRow is one split line as posted (everything is still text).
type formRow struct {
	account, amount, currency, value, memo, tags, rec string
}

// editForm is the whole editor form as posted.
type editForm struct {
	date, description, notes, currency, next string
	rows                                     []formRow
}

// parseEditForm reads the posted editor form. Line fields are named r-<n>-<field>; lines are
// ordered by <n> (the server renumbers them contiguously on every render).
func parseEditForm(r *http.Request) editForm {
	f := editForm{
		date: r.PostFormValue("date"), description: r.PostFormValue("description"), notes: r.PostFormValue("notes"),
		currency: strings.ToUpper(strings.TrimSpace(r.PostFormValue("currency"))), next: safeNext(r.PostFormValue("next")),
	}
	byIndex := map[int]*formRow{}
	for key, vals := range r.PostForm {
		parts := strings.SplitN(key, "-", 3)
		if len(parts) != 3 || parts[0] != "r" || len(vals) == 0 {
			continue
		}
		n, err := strconv.Atoi(parts[1])
		if err != nil || n < 0 || n > 999 {
			continue
		}
		row := byIndex[n]
		if row == nil {
			row = &formRow{}
			byIndex[n] = row
		}
		switch parts[2] {
		case "account":
			row.account = vals[0]
		case "amount":
			row.amount = vals[0]
		case "currency":
			row.currency = strings.ToUpper(strings.TrimSpace(vals[0]))
		case "value":
			row.value = vals[0]
		case "memo":
			row.memo = vals[0]
		case "tags":
			row.tags = vals[0]
		case "rec":
			row.rec = vals[0]
		}
	}
	idx := make([]int, 0, len(byIndex))
	for n := range byIndex {
		idx = append(idx, n)
	}
	sort.Ints(idx)
	for _, n := range idx {
		f.rows = append(f.rows, *byIndex[n])
	}
	return f
}

// safeNext only lets through local paths, so "next" can't become an open redirect.
func safeNext(s string) string {
	if strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "//") && !strings.ContainsAny(s, "\\\r\n") {
		return s
	}
	return ""
}

func formFromTxn(t ledger.Transaction, next string) editForm {
	f := editForm{date: t.Date, description: t.Description, notes: t.Notes, currency: t.Currency, next: next}
	for _, sp := range t.Splits {
		tags := make([]string, len(sp.Tags))
		for i, tag := range sp.Tags {
			tags[i] = "#" + tag
		}
		f.rows = append(f.rows, formRow{
			account: strconv.FormatInt(sp.AccountID, 10), amount: ledger.FormatInput(sp.Amount, sp.Currency),
			currency: sp.Currency, value: ledger.FormatInput(sp.Value, t.Currency), memo: sp.Memo,
			tags: strings.Join(tags, " "), rec: sp.Reconciled,
		})
	}
	return f
}

func parseTags(s string) []string {
	var out []string
	for _, raw := range strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == ',' }) {
		if tag := ledger.TagSlug(raw); tag != "" {
			out = append(out, tag)
		}
	}
	return out
}

// accountSet is the accounts the editor can reference.
type accountSet struct {
	byID map[int64]gen.Account
	all  []gen.Account
}

func (s *Server) loadAccountSet(ctx context.Context) (accountSet, error) {
	all, err := s.q.ListAccounts(ctx)
	if err != nil {
		return accountSet{}, err
	}
	set := accountSet{byID: make(map[int64]gen.Account, len(all)), all: all}
	for _, a := range all {
		set.byID[a.ID] = a
	}
	return set, nil
}

func (a accountSet) lookup(idText string) (gen.Account, bool) {
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil {
		return gen.Account{}, false
	}
	acct, ok := a.byID[id]
	return acct, ok
}

// builtinExpenseID returns the id of the built-in Expenses account.
func (a accountSet) builtinExpenseID() string {
	for _, acct := range a.all {
		if acct.Builtin == 1 && acct.Type == "expense" {
			return strconv.FormatInt(acct.ID, 10)
		}
	}
	return ""
}

// parsedLines is the result of interpreting the posted rows.
type parsedLines struct {
	splits    []ledger.SplitInput // one per row; zero value where the row had an error or is blank
	rowErrors []string            // "" when the row is fine
	valid     []bool              // parsed into a usable split
	blank     []bool              // completely empty line: ignored, and dropped on save
	malformed []bool              // an amount was typed but isn't a number: always worth saying so
}

// interpret turns posted rows into split inputs, collecting a message per bad row.
func interpret(f editForm, accts accountSet) parsedLines {
	p := parsedLines{
		splits: make([]ledger.SplitInput, len(f.rows)), rowErrors: make([]string, len(f.rows)),
		valid: make([]bool, len(f.rows)), blank: make([]bool, len(f.rows)), malformed: make([]bool, len(f.rows)),
	}
	for i, row := range f.rows {
		fail := func(format string, a ...any) {
			p.rowErrors[i] = fmt.Sprintf("Line %d: "+format, append([]any{i + 1}, a...)...)
		}
		if strings.TrimSpace(row.amount+row.value+row.memo+row.tags) == "" {
			p.blank[i] = true // e.g. a line just added and not filled in
			continue
		}
		acct, ok := accts.lookup(row.account)
		if !ok {
			fail("choose an account.")
			continue
		}
		cur := row.currency
		if acct.Builtin == 0 {
			cur = acct.Currency.String // a real account fixes the line's currency
		}
		if cur == "" {
			cur = f.currency
		}
		if strings.TrimSpace(row.amount) == "" {
			fail("enter an amount.")
			continue
		}
		amount, err := ledger.ParseAmount(row.amount, cur)
		if err != nil {
			fail("the amount isn't valid for %s (use a number like -12.50).", cur)
			p.malformed[i] = true
			continue
		}
		value := amount
		if cur != f.currency {
			if strings.TrimSpace(row.value) == "" {
				fail("enter what this is worth in %s.", f.currency)
				continue
			}
			if value, err = ledger.ParseAmount(row.value, f.currency); err != nil {
				fail("the %s value isn't valid.", f.currency)
				p.malformed[i] = true
				continue
			}
		}
		rec := row.rec
		if rec != "c" && rec != "y" {
			rec = "n"
		}
		p.splits[i] = ledger.SplitInput{
			AccountID: acct.ID, Memo: row.memo, Currency: cur, Amount: amount, Value: value, Reconciled: rec, Tags: parseTags(row.tags),
		}
		p.valid[i] = true
	}
	return p
}

// remaining sums only the lines that parsed, in the transaction currency. Blank lines count
// as nothing; any other line that failed to parse is reported through anyErrors.
func (p parsedLines) remaining() (rem int64, anyErrors bool) {
	for i, ok := range p.valid {
		switch {
		case ok:
			rem -= p.splits[i].Value
		case !p.blank[i]:
			anyErrors = true
		}
	}
	return rem, anyErrors
}

// effective returns the usable splits in order, leaving out blank lines.
func (p parsedLines) effective() []ledger.SplitInput {
	var out []ledger.SplitInput
	for i, ok := range p.valid {
		if ok {
			out = append(out, p.splits[i])
		}
	}
	return out
}

// firstMalformed returns the 1-based number of the first line whose amount isn't a number (0 if none).
func (p parsedLines) firstMalformed() int {
	for i, bad := range p.malformed {
		if bad {
			return i + 1
		}
	}
	return 0
}

// usedLines counts the lines that are not blank.
func (p parsedLines) usedLines() int {
	n := 0
	for _, b := range p.blank {
		if !b {
			n++
		}
	}
	return n
}

func remainingView(rem int64, anyErrors bool, rows int, cur string, badLine int) views.Remaining {
	switch {
	case badLine > 0:
		// A line that isn't a number is left out of the total, so say so rather than let the
		// remaining amount blame the other lines.
		return views.Remaining{Text: fmt.Sprintf("Line %d: that amount isn't a number (like -12.50 or +12.50)", badLine), State: "over"}
	case rem > 0:
		return views.Remaining{Text: ledger.Format(rem, cur) + " left to allocate", State: "under"}
	case rem < 0:
		return views.Remaining{Text: "Over-allocated by " + ledger.Format(-rem, cur), State: "over"}
	case anyErrors:
		return views.Remaining{Text: "Fix the highlighted lines", State: "under"}
	case rows < 2:
		return views.Remaining{Text: "A transaction needs at least 2 lines", State: "under"}
	}
	return views.Remaining{Text: "Balanced ✓", State: "ok", Balanced: true}
}

// buildEditor renders the form model. showRowErrors adds per-line messages (after a failed save).
func (s *Server) buildEditor(id int64, f editForm, accts accountSet, t ledger.Transaction, showRowErrors bool) views.Editor {
	parsed := interpret(f, accts)
	rem, anyErr := parsed.remaining()

	ed := views.Editor{
		ID: id, Date: f.date, Description: f.description, Notes: f.notes, Currency: f.currency, Next: f.next,
		Remaining: remainingView(rem, anyErr, parsed.usedLines(), f.currency, parsed.firstMalformed()),
	}
	used := map[string]bool{}
	currencies := map[string]bool{f.currency: true, "USD": true, "MXN": true, "JPY": true, "KRW": true}
	for i, row := range f.rows {
		used[row.account] = true
		er := views.EditRow{Account: row.account, Amount: row.amount, Currency: row.currency, Value: row.value,
			Memo: row.memo, Tags: row.tags, Reconciled: row.rec}
		if acct, ok := accts.lookup(row.account); ok {
			if acct.Builtin == 0 {
				er.Currency = acct.Currency.String
			} else {
				er.Builtin = true
			}
			er.Warn = acct.Type == "imbalance"
			ed.Imbalance = ed.Imbalance || er.Warn
		}
		if er.Currency == "" {
			er.Currency = f.currency
		}
		er.NeedsValue = er.Currency != f.currency
		currencies[er.Currency] = true
		if showRowErrors || parsed.malformed[i] {
			er.Error = parsed.rowErrors[i] // a malformed amount is flagged as soon as it is typed
		}
		ed.Rows = append(ed.Rows, er)
	}
	for _, a := range accts.all {
		opt := views.AccountOpt{ID: strconv.FormatInt(a.ID, 10), Name: a.Name}
		switch {
		case a.Builtin == 1:
			ed.Categories = append(ed.Categories, opt)
		case a.Archived == 0 || used[opt.ID]:
			ed.Accounts = append(ed.Accounts, opt)
			if a.Currency.Valid {
				currencies[a.Currency.String] = true
			}
		}
	}
	for c := range currencies {
		ed.Currencies = append(ed.Currencies, c)
	}
	sort.Strings(ed.Currencies)

	var meta []string
	if t.GnucashID != "" {
		meta = append(meta, "Imported from GnuCash")
	}
	if len(t.CreatedAt) >= 10 {
		meta = append(meta, "created "+t.CreatedAt[:10])
	}
	if len(t.UpdatedAt) >= 10 && t.UpdatedAt[:10] != t.CreatedAt[:min(10, len(t.CreatedAt))] {
		meta = append(meta, "edited "+t.UpdatedAt[:10])
	}
	ed.Meta = strings.Join(meta, " · ")
	return ed
}

func (s *Server) txnID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return 0, false
	}
	return id, true
}

// loadTxn fetches the transaction, answering 404 itself when it doesn't exist.
func (s *Server) loadTxn(w http.ResponseWriter, r *http.Request, id int64) (ledger.Transaction, bool) {
	t, err := s.ledger.Get(r.Context(), id)
	switch {
	case errors.Is(err, ledger.ErrNotFound):
		http.NotFound(w, r)
		return t, false
	case err != nil:
		s.serverError(w, r, err)
		return t, false
	}
	return t, true
}

func (s *Server) editorGet(w http.ResponseWriter, r *http.Request) {
	id, ok := s.txnID(w, r)
	if !ok {
		return
	}
	t, ok := s.loadTxn(w, r, id)
	if !ok {
		return
	}
	accts, err := s.loadAccountSet(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	next := safeNext(r.URL.Query().Get("next"))
	s.render(w, r, http.StatusOK, views.EditorPage(s.buildEditor(id, formFromTxn(t, next), accts, t, false)))
}

// editorPost handles every button in the editor form: save, add, remove-N, up-N, down-N, recalc.
func (s *Server) editorPost(w http.ResponseWriter, r *http.Request) {
	id, ok := s.txnID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	t, ok := s.loadTxn(w, r, id)
	if !ok {
		return
	}
	accts, err := s.loadAccountSet(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	f := parseEditForm(r)
	if f.currency == "" {
		f.currency = t.Currency
	}
	action := r.PostFormValue("action")

	if action == "save" {
		s.save(w, r, id, f, accts, t)
		return
	}
	switch {
	case action == "add":
		f.rows = append(f.rows, s.newRow(f, accts))
	case strings.HasPrefix(action, "remove-"):
		if i, ok := rowIndex(action, "remove-", len(f.rows)); ok {
			f.rows = append(f.rows[:i], f.rows[i+1:]...)
		}
	case strings.HasPrefix(action, "up-"):
		if i, ok := rowIndex(action, "up-", len(f.rows)); ok && i > 0 {
			f.rows[i-1], f.rows[i] = f.rows[i], f.rows[i-1]
		}
	case strings.HasPrefix(action, "down-"):
		if i, ok := rowIndex(action, "down-", len(f.rows)); ok && i < len(f.rows)-1 {
			f.rows[i+1], f.rows[i] = f.rows[i], f.rows[i+1]
		}
	}
	ed := s.buildEditor(id, f, accts, t, false)
	ed.FocusLast = action == "add"
	s.renderEditor(w, r, http.StatusOK, ed)
}

func rowIndex(action, prefix string, n int) (int, bool) {
	i, err := strconv.Atoi(strings.TrimPrefix(action, prefix))
	return i, err == nil && i >= 0 && i < n
}

// newRow is the line "+ Add line" appends: it takes whatever is left to allocate, in the same
// category as the line above (or Expenses), so splitting a receipt needs no arithmetic.
func (s *Server) newRow(f editForm, accts accountSet) formRow {
	row := formRow{currency: f.currency, account: accts.builtinExpenseID()}
	if n := len(f.rows); n > 0 {
		if last, ok := accts.lookup(f.rows[n-1].account); ok && last.Builtin == 1 && last.Type != "imbalance" {
			row.account = f.rows[n-1].account
		}
	}
	rem, _ := interpret(f, accts).remaining()
	if rem != 0 {
		row.amount = ledger.FormatInput(rem, f.currency)
	}
	return row
}

// editor renders the form; problems are form-level messages.
func (s *Server) editor(w http.ResponseWriter, r *http.Request, status int, id int64, f editForm, accts accountSet, t ledger.Transaction, showRowErrors bool, problems []string) {
	ed := s.buildEditor(id, f, accts, t, showRowErrors)
	ed.Problems = problems
	s.renderEditor(w, r, status, ed)
}

func (s *Server) renderEditor(w http.ResponseWriter, r *http.Request, status int, ed views.Editor) {
	if r.Header.Get("HX-Request") == "true" {
		s.render(w, r, status, views.EditorForm(ed))
		return
	}
	s.render(w, r, status, views.EditorPage(ed))
}

func (s *Server) save(w http.ResponseWriter, r *http.Request, id int64, f editForm, accts accountSet, t ledger.Transaction) {
	parsed := interpret(f, accts)
	var rowProblems bool
	for _, e := range parsed.rowErrors {
		rowProblems = rowProblems || e != ""
	}
	if rowProblems {
		s.editor(w, r, http.StatusUnprocessableEntity, id, f, accts, t, true, []string{"Some lines need attention."})
		return
	}
	in := ledger.TxnInput{Date: f.date, Description: f.description, Currency: f.currency, Notes: f.notes, Splits: parsed.effective()}
	if err := s.ledger.Update(r.Context(), id, in); err != nil {
		var ve *ledger.ValidationError
		if errors.As(err, &ve) {
			s.editor(w, r, http.StatusUnprocessableEntity, id, f, accts, t, true, ve.Problems)
			return
		}
		if errors.Is(err, ledger.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		s.serverError(w, r, err)
		return
	}
	s.logger.Info("transaction updated", "id", id, "source", "editor", "lines", len(in.Splits))
	s.redirect(w, r, nonEmptyStr(f.next, "/transactions"))
}

// redirect sends the browser to a local path: an HX-Redirect for htmx, a 303 otherwise.
func (s *Server) redirect(w http.ResponseWriter, r *http.Request, to string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", to)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func nonEmptyStr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// editorRemaining answers the live indicator: the remaining amount plus the Save button state.
func (s *Server) editorRemaining(w http.ResponseWriter, r *http.Request) {
	id, ok := s.txnID(w, r)
	if !ok {
		return
	}
	t, ok := s.loadTxn(w, r, id)
	if !ok {
		return
	}
	accts, err := s.loadAccountSet(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	f := parseEditForm(r)
	if f.currency == "" {
		f.currency = t.Currency
	}
	parsed := interpret(f, accts)
	rem, anyErr := parsed.remaining()
	s.render(w, r, http.StatusOK, views.RemainingUpdate(remainingView(rem, anyErr, parsed.usedLines(), f.currency, parsed.firstMalformed())))
}

func (s *Server) editorDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := s.txnID(w, r)
	if !ok {
		return
	}
	switch err := s.ledger.Delete(r.Context(), id); {
	case errors.Is(err, ledger.ErrNotFound):
		http.NotFound(w, r)
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	s.logger.Info("transaction deleted", "id", id, "source", "editor")
	s.redirect(w, r, nonEmptyStr(safeNext(r.PostFormValue("next")), "/transactions"))
}
