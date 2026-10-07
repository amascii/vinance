package web

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/amascii/vinance/internal/db/gen"
	"github.com/amascii/vinance/internal/ledger"
	"github.com/amascii/vinance/internal/quickadd"
	"github.com/amascii/vinance/internal/web/views"
)

const recentLimit = 20

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	m, err := s.quickAddModel(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	accts, _, defSlug, err := s.loadAccountsFor(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	entry := s.newEntryModel(r, accts, defSlug, nil)
	m.Form, m.Accounts, m.Today = entry.Form, entry.Accounts, entry.Today
	s.render(w, r, http.StatusOK, views.Home(m))
}

// quickAddModel is the home panel without its form: recent transactions and the banners.
func (s *Server) quickAddModel(ctx context.Context) (views.QuickAdd, error) {
	recent, err := s.ledger.ListRecent(ctx, recentLimit)
	if err != nil {
		return views.QuickAdd{}, err
	}
	needsFixing, err := s.ledger.Count(ctx, ledger.Filter{Imbalance: true})
	if err != nil {
		return views.QuickAdd{}, err
	}
	accounts, err := s.ledger.ListAccounts(ctx)
	if err != nil {
		return views.QuickAdd{}, err
	}
	dueCount, err := s.ledger.DueCount(ctx, s.now().Format(isoDate))
	if err != nil {
		return views.QuickAdd{}, err
	}
	return views.QuickAdd{Recent: rowsFor(recent, "/"), NeedsFixing: needsFixing, NoAccounts: len(accounts) == 0, DueCount: dueCount}, nil
}

// loadAccounts returns all accounts in the shape the resolver wants, plus the default account id.
func (s *Server) loadAccounts(ctx context.Context) ([]quickadd.Account, int64, error) {
	rows, err := s.q.ListAccounts(ctx)
	if err != nil {
		return nil, 0, err
	}
	accts := make([]quickadd.Account, len(rows))
	for i, a := range rows {
		accts[i] = quickadd.Account{
			ID: a.ID, Slug: a.Slug, Name: a.Name, Type: a.Type, Currency: a.Currency.String,
			Builtin: a.Builtin == 1, Archived: a.Archived == 1,
		}
	}
	def, err := s.q.LastSpentFromAccountID(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, 0, err
	}
	return accts, def, nil
}

// panel renders the quick-add panel for htmx requests, or the whole page for a plain form
// post (JavaScript disabled or not yet initialised), so submitting always works.
func (s *Server) panel(w http.ResponseWriter, r *http.Request, status int, m views.QuickAdd) {
	if r.Header.Get("HX-Request") == "true" {
		s.render(w, r, status, views.QuickAddPanel(m))
		return
	}
	s.render(w, r, status, views.Home(m))
}

// ---- the entry form ----

// entryAccounts are the accounts offered in the form: real and unarchived.
func entryAccounts(accts []quickadd.Account) []views.EntryAccount {
	var out []views.EntryAccount
	for _, a := range accts {
		if !a.Builtin && !a.Archived {
			out = append(out, views.EntryAccount{Slug: a.Slug, Name: a.Name, Currency: a.Currency, Type: a.Type})
		}
	}
	return out
}

// newEntryModel builds a fresh form: Spend, the remembered account and date, nothing typed. scope
// is the account of the register the form sits on (nil on the home page).
func (s *Server) newEntryModel(r *http.Request, accts []quickadd.Account, defSlug string, scope *quickadd.Account) views.QuickAdd {
	m := views.QuickAdd{Accounts: entryAccounts(accts), Today: s.now().Format(isoDate)}
	m.Form = views.EntryForm{Kind: "expense", Account: defSlug, Direction: "out"}
	if m.Form.Account == "" && len(m.Accounts) > 0 {
		m.Form.Account = m.Accounts[0].Slug // what the browser would show selected anyway
	}
	today := s.now()
	m.Form.Date = rememberedDate(r, today)
	if scope != nil {
		m.Scope = &views.Scope{Slug: scope.Slug, Name: scope.Name, Type: scope.Type, Currency: scope.Currency}
		m.Form.Account = scope.Slug
		if scope.Type == "liability" {
			m.Form.Direction = "in" // paying a card is the usual thing to do with one
		}
	}
	// A transfer's "to" starts on the first account that isn't the "from".
	for _, a := range m.Accounts {
		if a.Slug != m.Form.Account {
			m.Form.To = a.Slug
			break
		}
	}
	return m
}

// rememberedDate is the date the form starts on: the one from the last entry when that was not
// today, otherwise today.
func rememberedDate(r *http.Request, today time.Time) string {
	if v := cookieValue(r, dateCookie); v != "" {
		if _, err := time.Parse(isoDate, v); err == nil {
			return v
		}
	}
	return today.Format(isoDate)
}

// entryFromForm reads the posted entry and turns it into what Resolve understands. Problems are
// things the person can fix; the form is returned with exactly what they submitted.
func (s *Server) entryFromForm(r *http.Request, accts []quickadd.Account, scope *quickadd.Account) (quickadd.Parsed, views.EntryForm, []string) {
	v := func(k string) string { return strings.TrimSpace(r.PostFormValue(k)) }
	form := views.EntryForm{
		Kind: v("kind"), Account: v("account"), To: v("to"), Direction: v("direction"),
		Date: v("date"), Description: v("description"), Amount: v("amount"), Tags: v("tags"),
	}
	var problems []string
	add := func(msg string) { problems = append(problems, msg) }

	if form.Kind != "expense" && form.Kind != "income" && form.Kind != "transfer" {
		add("Choose Spend, Income or Transfer.")
		form.Kind = "expense"
	}
	if _, err := time.Parse(isoDate, form.Date); err != nil {
		add("Pick a date.")
	}
	if form.Description == "" {
		add("Enter a description.")
	}
	if form.Amount == "" {
		add("Enter an amount, like 12.50.")
	}

	usable := map[string]bool{}
	for _, a := range accts {
		if !a.Builtin && !a.Archived {
			usable[a.Slug] = true
		}
	}
	var accounts []string
	switch {
	case scope != nil && form.Kind == "transfer":
		form.Account = scope.Slug
		if !usable[form.To] || form.To == scope.Slug {
			add("Choose the other account for the transfer.")
		} else if form.Direction == "in" {
			accounts = []string{form.To, scope.Slug}
		} else {
			form.Direction = "out"
			accounts = []string{scope.Slug, form.To}
		}
	case scope != nil:
		form.Account = scope.Slug
		accounts = []string{scope.Slug}
	case !usable[form.Account]:
		add("Choose an account.")
	case form.Kind == "transfer":
		if !usable[form.To] || form.To == form.Account {
			add("Choose two different accounts for a transfer.")
		} else {
			accounts = []string{form.Account, form.To}
		}
	default:
		accounts = []string{form.Account}
	}

	parsed := quickadd.Parsed{
		Income: form.Kind == "income", Amount: form.Amount, Description: form.Description,
		Tags: parseTags(form.Tags), Accounts: accounts, Date: form.Date, DateGiven: true,
	}
	return parsed, form, problems
}

// quickAddSubmit adds the entered transaction. On a register (a "scope" field) the account is the
// register's and the page reloads to show it; on the home page the panel is swapped.
func (s *Server) quickAddSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accts, _, defSlug, err := s.loadAccountsFor(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var scope *quickadd.Account
	if slug := strings.TrimSpace(r.PostFormValue("scope")); slug != "" {
		if scope = findAccount(accts, slug); scope == nil || scope.Archived {
			http.NotFound(w, r)
			return
		}
	}
	back := safeNext(r.PostFormValue("return"))
	if scope != nil && back == "" {
		back = "/transactions?account=" + url.QueryEscape(scope.Slug)
	}

	reject := func(form views.EntryForm, problems []string) {
		m, err := s.rejectedModel(r, accts, defSlug, scope, form, problems, back)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		switch {
		case scope != nil && r.Header.Get("HX-Request") == "true":
			s.render(w, r, http.StatusUnprocessableEntity, views.ScopedPanel(m))
		case scope != nil:
			s.render(w, r, http.StatusUnprocessableEntity, views.ScopedEntryPage(m))
		default:
			s.panel(w, r, http.StatusUnprocessableEntity, m)
		}
	}

	parsed, form, problems := s.entryFromForm(r, accts, scope)
	var res quickadd.Result
	if len(problems) == 0 {
		if res, err = quickadd.Resolve(parsed, accts, 0); err != nil {
			var qe *quickadd.Error
			if !errors.As(err, &qe) {
				s.serverError(w, r, err)
				return
			}
			problems = qe.Problems
		}
	}
	if len(problems) > 0 {
		reject(form, problems)
		return
	}

	id, err := s.ledger.Create(ctx, res.Input)
	if err != nil {
		var ve *ledger.ValidationError
		if errors.As(err, &ve) { // shouldn't happen: Resolve builds balanced input
			reject(form, ve.Problems)
			return
		}
		s.serverError(w, r, err)
		return
	}
	source := "quickadd"
	if scope != nil {
		source = "register"
	}
	s.logger.Info("transaction created", "id", id, "source", source, "kind", string(res.Kind),
		"date", res.Input.Date, "currency", res.Currency, "amount", res.Amount)

	// Remember where the person is working: the account (the register's, or the first one used) and
	// the date, unless it was today (then tomorrow starts on tomorrow).
	slug := res.Accounts[0].Slug
	if scope != nil {
		slug = scope.Slug
	}
	setSticky(w, acctCookie, slug)
	if form.Date != s.now().Format(isoDate) {
		setSticky(w, dateCookie, form.Date)
	} else {
		clearSticky(w, dateCookie)
	}
	flash := "Added " + res.Input.Description + " " + ledger.Format(res.Amount, res.Currency) + " (" + accountsLabel(res.Accounts) + ")"

	if scope != nil {
		setFlash(w, flash)
		s.redirect(w, r, back)
		return
	}
	m, err := s.quickAddModel(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	entry := s.newEntryModel(r, accts, slug, nil)
	entry.Form.Date = form.Date // the form keeps working on the day just used
	m.Form, m.Accounts, m.Today, m.Flash = entry.Form, entry.Accounts, entry.Today, flash
	s.panel(w, r, http.StatusOK, m)
}

// rejectedModel rebuilds the panel for a refused entry, keeping what was submitted.
func (s *Server) rejectedModel(r *http.Request, accts []quickadd.Account, defSlug string, scope *quickadd.Account, form views.EntryForm, problems []string, back string) (views.QuickAdd, error) {
	entry := s.newEntryModel(r, accts, defSlug, scope)
	if form.Date == "" {
		form.Date = entry.Form.Date
	}
	if scope != nil {
		entry.Problems, entry.Form, entry.Return = problems, form, back
		return entry, nil
	}
	m, err := s.quickAddModel(r.Context())
	if err != nil {
		return m, err
	}
	m.Form, m.Accounts, m.Today, m.Problems = form, entry.Accounts, entry.Today, problems
	return m, nil
}

// ---- suggestions ----

const (
	maxSuggestion = 6
	minSuggestLen = 3 // letters typed before past descriptions are suggested
)

// suggestDescription lists past transactions matching what is typed in the description field.
func (s *Server) suggestDescription(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	typed := strings.TrimSpace(r.PostFormValue("description"))
	if len([]rune(typed)) < minSuggestLen {
		s.render(w, r, http.StatusOK, views.DescSuggestions(nil))
		return
	}
	accts, _, _, err := s.loadAccountsFor(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var scope *quickadd.Account
	if slug := strings.TrimSpace(r.PostFormValue("scope")); slug != "" {
		scope = findAccount(accts, slug)
	}
	entries, err := s.ledger.SuggestDescriptions(ctx, typed, maxSuggestion)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	items := make([]views.DescSuggestion, 0, len(entries))
	for _, e := range entries {
		items = append(items, descSuggestion(e, accts, scope))
	}
	s.render(w, r, http.StatusOK, views.DescSuggestions(items))
}

// suggestTags lists tags for the word being typed in the tags field (the most used ones when it is empty).
func (s *Server) suggestTags(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	text := r.PostFormValue("tags")
	pos, err := strconv.Atoi(r.PostFormValue("pos"))
	if err != nil {
		pos = len(utf16.Encode([]rune(text)))
	}
	prefix := sanitizePrefix(strings.ToLower(strings.TrimPrefix(tokenAt(text, pos), "#")))
	rows, err := s.q.SuggestTags(ctx, gen.SuggestTagsParams{Prefix: sql.NullString{String: prefix, Valid: true}, MaxRows: maxSuggestions + 4})
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	already := map[string]bool{}
	for _, t := range parseTags(text) {
		already[t] = true
	}
	var items []views.Suggestion
	for _, t := range rows {
		if !already[t.Name] && len(items) < maxSuggestions {
			items = append(items, views.Suggestion{Insert: t.Name, Label: "#" + t.Name})
		}
	}
	s.render(w, r, http.StatusOK, views.TagSuggestions(items))
}

const maxSuggestions = 8

func previewOK(res quickadd.Result, parsed quickadd.Parsed) *views.PreviewOK {
	kind := map[quickadd.Kind]string{quickadd.KindExpense: "Expense", quickadd.KindIncome: "Income", quickadd.KindTransfer: "Transfer"}[res.Kind]
	names := make([]string, len(res.Accounts))
	for i, a := range res.Accounts {
		names[i] = a.Name
	}
	if res.Kind == quickadd.KindTransfer && len(names) == 2 {
		names = []string{names[0] + " → " + names[1]}
	}
	ok := &views.PreviewOK{
		Kind: kind, Accounts: names, Amount: ledger.Format(res.Amount, res.Currency),
		Description: res.Input.Description, Date: res.Input.Date, Tags: parsed.Tags,
	}
	if !parsed.DateGiven {
		ok.DateNote = "today"
	}
	return ok
}

// tokenAt returns the whitespace-delimited token containing UTF-16 offset pos in text.
func tokenAt(text string, pos int) string {
	units := utf16.Encode([]rune(text))
	if pos < 0 {
		pos = 0
	}
	if pos > len(units) {
		pos = len(units)
	}
	start, end := pos, pos
	for start > 0 && !isSpace(units[start-1]) {
		start--
	}
	for end < len(units) && !isSpace(units[end]) {
		end++
	}
	return string(utf16.Decode(units[start:end]))
}

func isSpace(u uint16) bool { return u == ' ' || u == '\t' || u == '\n' || u == '\r' }

// sanitizePrefix keeps only characters that can appear in a slug or tag, so the value is
// safe to use in a LIKE pattern (no %, _ or \ wildcards).
func sanitizePrefix(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r > 127 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// findAccount returns the real account with the slug, or nil.
func findAccount(accts []quickadd.Account, slug string) *quickadd.Account {
	for i := range accts {
		if accts[i].Slug == slug && !accts[i].Builtin {
			return &accts[i]
		}
	}
	return nil
}

// accountsLabel names the accounts of an entry: "BRISK", or "Neo → Neo Credit" for a transfer.
func accountsLabel(accts []quickadd.Account) string {
	names := make([]string, len(accts))
	for i, a := range accts {
		names[i] = a.Name
	}
	return strings.Join(names, " → ")
}
