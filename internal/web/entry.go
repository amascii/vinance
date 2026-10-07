package web

import (
	"net/http"
	"strings"

	"github.com/amascii/vinance/internal/ledger"
	"github.com/amascii/vinance/internal/quickadd"
	"github.com/amascii/vinance/internal/web/views"
)

// The entry form remembers, per browser, the account and the date of the last entry, because
// people work through a statement one day at a time and keep adding to the same account.
const (
	acctCookie   = "vinance_acct"
	dateCookie   = "vinance_date"
	stickyMaxAge = 365 * 24 * 60 * 60
)

func setSticky(w http.ResponseWriter, name, value string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: stickyMaxAge, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func clearSticky(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func cookieValue(r *http.Request, name string) string {
	if c, err := r.Cookie(name); err == nil {
		return c.Value
	}
	return ""
}

// loadAccountsFor loads the accounts for the entry form and works out the default account: the one
// remembered from the last entry if it is still usable, otherwise the one most recently spent from.
func (s *Server) loadAccountsFor(r *http.Request) (accts []quickadd.Account, defID int64, defSlug string, err error) {
	accts, defID, err = s.loadAccounts(r.Context())
	if err != nil {
		return nil, 0, "", err
	}
	if slug := cookieValue(r, acctCookie); slug != "" {
		for _, a := range accts {
			if a.Slug == slug && !a.Builtin && !a.Archived {
				defID = a.ID
			}
		}
	}
	for _, a := range accts {
		if a.ID == defID {
			defSlug = a.Slug
		}
	}
	return accts, defID, defSlug, nil
}

// pastFrom describes an earlier transaction as a quick-add completion: which accounts, what
// amount, whether it was income, its tags, and a one-line summary for the suggestion list.
func pastFrom(t ledger.Transaction, accts []quickadd.Account) (quickadd.Past, string) {
	sum := t.Summary()
	slugs := map[int64]quickadd.Account{}
	for _, a := range accts {
		if !a.Builtin && !a.Archived {
			slugs[a.ID] = a
		}
	}
	var from, to *ledger.Split
	for i := range t.Splits {
		sp := &t.Splits[i]
		if sp.AccountType != "asset" && sp.AccountType != "liability" {
			continue
		}
		if sp.Value < 0 && from == nil {
			from = sp
		}
		if sp.Value > 0 && to == nil {
			to = sp
		}
	}

	past := quickadd.Past{Description: t.Description, Income: sum.Direction == ledger.DirIn, Tags: sum.Tags}
	var primary *ledger.Split
	var used []*ledger.Split
	switch sum.Direction {
	case ledger.DirOut:
		primary, used = from, []*ledger.Split{from}
	case ledger.DirIn:
		primary, used = to, []*ledger.Split{to}
	case ledger.DirTransfer:
		primary, used = from, []*ledger.Split{from, to}
	}
	var names []string
	known := len(used) > 0
	for _, sp := range used {
		var a quickadd.Account
		ok := false
		if sp != nil {
			a, ok = slugs[sp.AccountID]
		}
		if !ok {
			known = false
			break
		}
		past.Accounts = append(past.Accounts, a.Slug)
		names = append(names, a.Name)
	}
	if !known {
		past.Accounts, names = nil, nil
	}

	var amount string
	if primary != nil {
		abs := primary.Amount
		if abs < 0 {
			abs = -abs
		}
		past.Amount = ledger.FormatInput(abs, primary.Currency)
		amount = ledger.Format(abs, primary.Currency)
		switch sum.Direction {
		case ledger.DirOut:
			amount = "-" + amount
		case ledger.DirIn:
			amount = "+" + amount
		}
	}
	parts := []string{}
	if amount != "" {
		parts = append(parts, amount)
	}
	if len(names) > 0 {
		parts = append(parts, strings.Join(names, " → "))
	}
	for _, tag := range sum.Tags {
		parts = append(parts, "#"+tag)
	}
	return past, strings.Join(parts, " · ")
}

// descSuggestion turns a past transaction into a suggestion: what it looks like in the list and what
// accepting it fills in. On a register (scope != nil) the account is fixed, so history never changes
// it; a past transfer with the register's account brings the other account and the direction.
func descSuggestion(e ledger.PastEntry, accts []quickadd.Account, scope *quickadd.Account) views.DescSuggestion {
	past, detail := pastFrom(e.Last, accts)
	if e.Count > 1 {
		detail += " · " + views.Thousands(e.Count) + "×"
	}
	fill := views.DescFill{Amount: past.Amount, Tags: strings.Join(past.Tags, " ")}
	kind := "expense"
	switch {
	case len(past.Accounts) == 2:
		kind = "transfer"
	case past.Income:
		kind = "income"
	}

	if scope == nil {
		fill.Kind = kind
		if len(past.Accounts) > 0 {
			fill.Account = past.Accounts[0]
		}
		if kind == "transfer" {
			fill.To = past.Accounts[1]
		}
	} else {
		switch {
		case kind != "transfer":
			fill.Kind = kind
		case past.Accounts[0] == scope.Slug:
			fill.Kind, fill.To, fill.Direction = "transfer", past.Accounts[1], "out"
		case past.Accounts[1] == scope.Slug:
			fill.Kind, fill.To, fill.Direction = "transfer", past.Accounts[0], "in"
		}
		// a transfer between two other accounts: only the description, amount and tags carry over
	}
	return views.DescSuggestion{Description: e.Description, Detail: strings.TrimPrefix(detail, " · "), Fill: fill}
}
