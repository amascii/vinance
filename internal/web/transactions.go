package web

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/amascii/vinance/internal/db/gen"
	"github.com/amascii/vinance/internal/ledger"
	"github.com/amascii/vinance/internal/web/views"
)

// parsedFilter is a transaction filter parsed from query parameters.
type parsedFilter struct {
	filter  ledger.Filter
	form    views.TxnFilterForm
	notices []string
	empty   bool         // the filter can match nothing (e.g. unknown account)
	account *gen.Account // the account being viewed, when filtering by one
}

func (s *Server) parseFilter(ctx context.Context, v url.Values) (parsedFilter, error) {
	var p parsedFilter
	p.form = views.TxnFilterForm{
		Q: strings.TrimSpace(v.Get("q")), Account: v.Get("account"), Tags: strings.TrimSpace(v.Get("tag")),
		From: v.Get("from"), To: v.Get("to"), Imbalance: v.Get("imbalance") == "1", Untagged: v.Get("untagged") == "1",
	}
	p.filter.Text = p.form.Q
	p.filter.Imbalance = p.form.Imbalance
	p.filter.Untagged = p.form.Untagged

	if p.form.Account != "" {
		a, err := s.q.GetAccountBySlug(ctx, p.form.Account)
		switch {
		case err == nil:
			p.filter.AccountID = a.ID
			p.account = &a
		case errors.Is(err, sql.ErrNoRows):
			p.notices = append(p.notices, "Unknown account: "+p.form.Account)
			p.empty = true
		default:
			return p, err
		}
	}

	seen := map[string]bool{}
	for _, raw := range strings.FieldsFunc(p.form.Tags, func(r rune) bool { return r == ' ' || r == ',' }) {
		if tag := ledger.TagSlug(raw); tag != "" && !seen[tag] {
			seen[tag] = true
			p.filter.Tags = append(p.filter.Tags, tag)
		}
	}

	for _, d := range []struct {
		name string
		val  *string
		set  string
	}{{"From", &p.filter.From, p.form.From}, {"To", &p.filter.To, p.form.To}} {
		if d.set == "" {
			continue
		}
		if _, err := time.Parse("2006-01-02", d.set); err != nil {
			p.notices = append(p.notices, d.name+" date must look like 2026-09-24; ignoring it.")
			continue
		}
		*d.val = d.set
	}
	if p.filter.From != "" && p.filter.To != "" && p.filter.From > p.filter.To {
		p.notices = append(p.notices, "The From date is after the To date.")
	}
	return p, nil
}

// query re-encodes the active filters (without a cursor) for building the next-page URL.
func (p parsedFilter) query() url.Values {
	v := url.Values{}
	set := func(k, val string) {
		if val != "" {
			v.Set(k, val)
		}
	}
	set("q", p.form.Q)
	set("account", p.form.Account)
	set("tag", p.form.Tags)
	set("from", p.filter.From)
	set("to", p.filter.To)
	if p.form.Imbalance {
		v.Set("imbalance", "1")
	}
	if p.form.Untagged {
		v.Set("untagged", "1")
	}
	return v
}

// canonicalURL is the clean, shareable URL for the active filters.
func (p parsedFilter) canonicalURL() string {
	if q := p.query().Encode(); q != "" {
		return "/transactions?" + q
	}
	return "/transactions"
}

func (s *Server) transactions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	p, err := s.parseFilter(ctx, q)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if c := q.Get("before"); c != "" {
		cur, err := ledger.ParseCursor(c)
		if err != nil {
			http.Error(w, "bad cursor", http.StatusBadRequest)
			return
		}
		p.filter.Before = &cur
	}

	m := views.TxnList{Form: p.form, Notices: p.notices}
	if !p.empty {
		page, err := s.ledger.List(ctx, p.filter)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		m.Items = rowsFor(page.Transactions, p.canonicalURL())
		for i := range m.Items {
			m.Items[i].Pick = true
		}
		m.FilterURL = p.canonicalURL()
		if p.account != nil && p.account.Builtin == 0 {
			if err := s.addRegister(ctx, &m, page.Transactions, *p.account); err != nil {
				s.serverError(w, r, err)
				return
			}
		}
		if p.account != nil && p.account.Builtin == 0 && p.filter.Before == nil {
			if p.account.Archived == 1 {
				m.EntryNote = p.account.Name + " is archived, so new transactions can't be added here. Restore it under Accounts → Manage."
			} else {
				accts, _, defSlug, err := s.loadAccountsFor(r)
				if err != nil {
					s.serverError(w, r, err)
					return
				}
				entry := s.newEntryModel(r, accts, defSlug, findAccount(accts, p.account.Slug))
				entry.Return = p.canonicalURL()
				m.Entry = &entry
			}
		}
		if page.Next != nil {
			next := p.query()
			next.Set("before", page.Next.String())
			m.NextURL = "/transactions?" + next.Encode()
		}
		if p.filter.Before == nil {
			if m.Count, err = s.ledger.Count(ctx, p.filter); err != nil {
				s.serverError(w, r, err)
				return
			}
			if p.filter.Active() {
				totals, err := s.ledger.Totals(ctx, p.filter)
				if err != nil {
					s.serverError(w, r, err)
					return
				}
				for _, t := range totals {
					line := views.TxnTotal{Currency: t.Currency, Amount: ledger.Format(t.Net, t.Currency)}
					switch {
					case t.Net > 0:
						line.Amount, line.Direction = "+"+line.Amount, string(ledger.DirIn)
					case t.Net < 0:
						line.Direction = string(ledger.DirOut)
					}
					m.Totals = append(m.Totals, line)
				}
			}
		}
	}

	// The same URL serves three shapes, so caches must key on how it was requested.
	w.Header().Add("Vary", "HX-Request")
	htmxReq := r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-History-Restore-Request") != "true"
	switch {
	case htmxReq && p.filter.Before != nil: // infinite scroll: just the next rows
		s.render(w, r, http.StatusOK, views.TxnItems(m.Items, m.NextURL, p.filter.Before.Date))
	case htmxReq: // filter change: count + first page
		// Push the canonical URL (no empty parameters) rather than the raw form serialization.
		w.Header().Set("HX-Push-Url", p.canonicalURL())
		s.render(w, r, http.StatusOK, views.TxnResults(m))
	default:
		m.Flash = takeFlash(w, r)
		if m.Accounts, m.TagOptions, err = s.filterOptions(ctx); err != nil {
			s.serverError(w, r, err)
			return
		}
		s.render(w, r, http.StatusOK, views.TransactionsPage(m))
	}
}

// filterOptions loads the dropdown/datalist choices for the filter form.
func (s *Server) filterOptions(ctx context.Context) ([]views.AccountOption, []string, error) {
	accts, err := s.q.ListAccounts(ctx)
	if err != nil {
		return nil, nil, err
	}
	var opts []views.AccountOption
	for _, a := range accts {
		if a.Builtin == 0 {
			opts = append(opts, views.AccountOption{Slug: a.Slug, Name: a.Name})
		}
	}
	tags, err := s.q.SuggestTags(ctx, gen.SuggestTagsParams{Prefix: sql.NullString{Valid: true}, MaxRows: 200})
	if err != nil {
		return nil, nil, err
	}
	names := make([]string, len(tags))
	for i, t := range tags {
		names[i] = t.Name
	}
	return opts, names, nil
}

// addRegister turns the list into an account register: each row shows how the transaction moved
// this account (not the whole transaction) and the account's balance afterwards, and the page gets
// a header with the current balance. Balances come from the account's full history, so they are right
// under any filter and on any page of the infinite scroll.
func (s *Server) addRegister(ctx context.Context, m *views.TxnList, txns []ledger.Transaction, a gen.Account) error {
	ids := make([]int64, len(txns))
	for i, t := range txns {
		ids[i] = t.ID
	}
	moves, err := s.ledger.RunningBalances(ctx, a.ID, ids)
	if err != nil {
		return err
	}
	cur := a.Currency.String
	label := "Balance"
	flip := int64(1)
	if a.Type == "liability" {
		label, flip = "Owed", -1 // what you owe, positive, as on the Accounts page
	}
	for i, t := range txns {
		mv, ok := moves[t.ID]
		if !ok {
			continue
		}
		row := &m.Items[i]
		switch {
		case mv.Delta < 0:
			row.Amount, row.Direction = ledger.Format(mv.Delta, cur), string(ledger.DirOut)
		case mv.Delta > 0:
			row.Amount, row.Direction = "+"+ledger.Format(mv.Delta, cur), string(ledger.DirIn)
		default:
			row.Amount, row.Direction = ledger.Format(0, cur), ""
		}
		row.Running = label + " " + ledger.Format(flip*mv.Balance, cur)
	}
	now, err := s.ledger.AccountBalance(ctx, a.ID)
	if err != nil {
		return err
	}
	m.AccountHeader = a.Name + " · " + label + " " + ledger.Format(flip*now, cur)
	return nil
}
