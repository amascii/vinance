package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/ledger"
)

// withCookies issues a request carrying cookies and returns the response and parsed body.
func withCookies(t *testing.T, srv *httptest.Server, method, path string, form url.Values, cookies []*http.Cookie) (*http.Response, *goquery.Document) {
	t.Helper()
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req, err := http.NewRequest(method, srv.URL+path, body)
	require.NoError(t, err)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("HX-Request", "true")
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := noRedirectClient.Do(req)
	require.NoError(t, err)
	status := resp.StatusCode
	d := doc(t, resp)
	resp.StatusCode = status
	return resp, d
}

func setCookies(resp *http.Response) []*http.Cookie {
	var out []*http.Cookie
	for _, c := range resp.Cookies() {
		if c.MaxAge >= 0 && c.Value != "" {
			out = append(out, c)
		}
	}
	return out
}

func cookieNamed(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func descSuggestions(d *goquery.Document) []*goquery.Selection {
	var out []*goquery.Selection
	d.Find(".desc-suggestion").Each(func(_ int, s *goquery.Selection) { out = append(out, s) })
	return out
}

func attr(s *goquery.Selection, name string) string { v, _ := s.Attr(name); return v }

// history seeds: FNDXX Dividends (income into Wharf Bank), Maple One (rent share on BRISK, twice),
// a card payment transfer, and a few accented / emoji descriptions.
func seedHistory(t *testing.T, conn interface{}, s *seeder) {
	t.Helper()
	ctx := context.Background()
	mk := func(date, desc string, splits []ledger.SplitInput) {
		_, err := s.svc.Create(ctx, ledger.TxnInput{Date: date, Description: desc, Currency: "USD", Splits: splits})
		require.NoError(t, err)
	}
	exp, inc := s.acct["expense"], s.acct["income"]
	brisk, wharf := s.acct["brisk"], s.acct["wharf-bank"]
	mk("2026-09-10", "FNDXX Dividends", []ledger.SplitInput{
		{AccountID: wharf, Currency: "USD", Amount: 4520, Value: 4520},
		{AccountID: inc, Currency: "USD", Amount: -4520, Value: -4520, Tags: []string{"dividends"}},
	})
	for _, d := range []string{"2026-09-01", "2026-09-15"} {
		mk(d, "Maple One", []ledger.SplitInput{
			{AccountID: brisk, Currency: "USD", Amount: -50000, Value: -50000},
			{AccountID: exp, Currency: "USD", Amount: 50000, Value: 50000, Tags: []string{"rent-share"}},
		})
	}
	mk("2026-09-20", "Credit Card Payment", []ledger.SplitInput{
		{AccountID: wharf, Currency: "USD", Amount: -50000, Value: -50000},
		{AccountID: brisk, Currency: "USD", Amount: 50000, Value: 50000},
	})
	mk("2026-09-21", "Café Müller", []ledger.SplitInput{
		{AccountID: brisk, Currency: "USD", Amount: -450, Value: -450},
		{AccountID: exp, Currency: "USD", Amount: 450, Value: 450, Tags: []string{"cafes"}},
	})
	mk("2026-09-22", "🍔 Burger", []ledger.SplitInput{
		{AccountID: brisk, Currency: "USD", Amount: -900, Value: -900},
		{AccountID: exp, Currency: "USD", Amount: 900, Value: 900, Tags: []string{"fast-food"}},
	})
}

// suggestDesc asks for past-transaction suggestions for what is typed in the description field.
func suggestDesc(t *testing.T, srv *httptest.Server, text, scope string) *goquery.Document {
	t.Helper()
	form := url.Values{"description": {text}}
	if scope != "" {
		form.Set("scope", scope)
	}
	_, d := withCookies(t, srv, http.MethodPost, "/quickadd/suggest/description", form, nil)
	return d
}

// fillOf returns what accepting a suggestion sets: kind, account, to, direction, amount, tags.
func fillOf(s *goquery.Selection) map[string]string {
	out := map[string]string{}
	for _, k := range []string{"kind", "account", "to", "direction", "amount", "tags", "description"} {
		out[k] = attr(s, "data-"+k)
	}
	return out
}

func TestSuggestionsFillEveryField(t *testing.T) {
	srv, conn := newApp(t)
	seedHistory(t, conn, newSeeder(t, conn))

	// An income: the type, the account it always lands in, the last amount and tags.
	d := suggestDesc(t, srv, "fnd", "")
	sugg := descSuggestions(d)
	require.Len(t, sugg, 1)
	assert.Equal(t, map[string]string{"kind": "income", "account": "wharf-bank", "to": "", "direction": "", "amount": "45.20", "tags": "dividends", "description": "FNDXX Dividends"}, fillOf(sugg[0]))
	assert.Equal(t, "FNDXX Dividends", rowTexts(sugg[0].Find(".d-main")))
	assert.Equal(t, "+$45.20 · Wharf Bank · #dividends", rowTexts(sugg[0].Find(".d-detail")))
	assert.Equal(t, "option", attr(sugg[0], "role"))
	assert.Equal(t, 1, d.Find(`[role="listbox"]`).Length())

	// Spending, used twice.
	sugg = descSuggestions(suggestDesc(t, srv, "map", ""))
	require.Len(t, sugg, 1)
	assert.Equal(t, map[string]string{"kind": "expense", "account": "brisk", "to": "", "direction": "", "amount": "500.00", "tags": "rent-share", "description": "Maple One"}, fillOf(sugg[0]))
	assert.Equal(t, "-$500.00 · BRISK · #rent-share · 2×", rowTexts(sugg[0].Find(".d-detail")))

	// A transfer fills both accounts.
	sugg = descSuggestions(suggestDesc(t, srv, "credit", ""))
	require.Len(t, sugg, 1)
	assert.Equal(t, map[string]string{"kind": "transfer", "account": "wharf-bank", "to": "brisk", "direction": "", "amount": "500.00", "tags": "", "description": "Credit Card Payment"}, fillOf(sugg[0]))
	assert.Equal(t, "$500.00 · Wharf Bank → BRISK", rowTexts(sugg[0].Find(".d-detail")))
}

func TestSuggestionsNeedThreeLettersAndMatchWordStarts(t *testing.T) {
	srv, conn := newApp(t)
	seedHistory(t, conn, newSeeder(t, conn))

	for _, text := range []string{"", "s", "sn", "se", "  sn  "} {
		assert.Empty(t, descSuggestions(suggestDesc(t, srv, text, "")), "%q is too short", text)
	}
	assert.Len(t, descSuggestions(suggestDesc(t, srv, "div", "")), 1, "the second word of FNDXX Dividends")
	assert.Len(t, descSuggestions(suggestDesc(t, srv, "maple one", "")), 1, "multi-word")
	assert.Len(t, descSuggestions(suggestDesc(t, srv, "MAP", "")), 1, "case-insensitive")
	assert.Empty(t, descSuggestions(suggestDesc(t, srv, "zzz", "")))
	assert.Empty(t, descSuggestions(suggestDesc(t, srv, "xx", "")), "must start the description or a word")
}

func TestSuggestionsSkipArchivedAccounts(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	seedHistory(t, conn, s)
	require.NoError(t, ledger.NewService(conn).SetAccountArchived(context.Background(), s.acct["wharf-bank"], true))

	sugg := descSuggestions(suggestDesc(t, srv, "fnd", ""))
	require.Len(t, sugg, 1)
	fill := fillOf(sugg[0])
	assert.Equal(t, "", fill["account"], "the archived account isn't offered; the form keeps the one chosen")
	assert.Equal(t, "income", fill["kind"])
	assert.Equal(t, "45.20", fill["amount"])
	assert.NotContains(t, rowTexts(sugg[0].Find(".d-detail")), "Wharf Bank")
}

func TestSuggestionsHandleAccentedAndEmojiText(t *testing.T) {
	srv, conn := newApp(t)
	seedHistory(t, conn, newSeeder(t, conn))
	sugg := descSuggestions(suggestDesc(t, srv, "caf", ""))
	require.Len(t, sugg, 1)
	assert.Equal(t, "Café Müller", attr(sugg[0], "data-description"))
	assert.Equal(t, "4.50", attr(sugg[0], "data-amount"))
	sugg = descSuggestions(suggestDesc(t, srv, "bur", ""))
	require.Len(t, sugg, 1)
	assert.Equal(t, "🍔 Burger", attr(sugg[0], "data-description"))
}

func TestSuggestionsOnARegisterNeverMoveTheAccount(t *testing.T) {
	srv, conn := newApp(t)
	seedHistory(t, conn, newSeeder(t, conn))

	// Income history on a BRISK register: the type carries over, but there is no account to change.
	sugg := descSuggestions(suggestDesc(t, srv, "fnd", "brisk"))
	require.Len(t, sugg, 1)
	assert.Equal(t, map[string]string{"kind": "income", "account": "", "to": "", "direction": "", "amount": "45.20", "tags": "dividends", "description": "FNDXX Dividends"}, fillOf(sugg[0]))
	// A past transfer involving this account brings the other account and the direction.
	sugg = descSuggestions(suggestDesc(t, srv, "credit", "brisk"))
	assert.Equal(t, map[string]string{"kind": "transfer", "account": "", "to": "wharf-bank", "direction": "in", "amount": "500.00", "tags": "", "description": "Credit Card Payment"}, fillOf(sugg[0]), "paid into the card from Wharf Bank")
	sugg = descSuggestions(suggestDesc(t, srv, "credit", "wharf-bank"))
	assert.Equal(t, map[string]string{"kind": "transfer", "account": "", "to": "brisk", "direction": "out", "amount": "500.00", "tags": "", "description": "Credit Card Payment"}, fillOf(sugg[0]), "the same payment seen from the bank")
	// A transfer between two OTHER accounts only lends its description, amount and tags.
	sugg = descSuggestions(suggestDesc(t, srv, "credit", "neo"))
	assert.Equal(t, map[string]string{"kind": "", "account": "", "to": "", "direction": "", "amount": "500.00", "tags": "", "description": "Credit Card Payment"}, fillOf(sugg[0]))
}

func TestSuggestionsPreserveTheUnusualDirection(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	_, err := s.svc.Create(context.Background(), ledger.TxnInput{Date: "2026-09-21", Description: "Cash Advance", Currency: "USD", Splits: []ledger.SplitInput{
		{AccountID: s.acct["brisk"], Currency: "USD", Amount: -10000, Value: -10000},
		{AccountID: s.acct["wharf-bank"], Currency: "USD", Amount: 10000, Value: 10000},
	}})
	require.NoError(t, err)

	sugg := descSuggestions(suggestDesc(t, srv, "cash", "brisk"))
	require.Len(t, sugg, 1)
	fill := fillOf(sugg[0])
	assert.Equal(t, "out", fill["direction"], "money left the card, not the usual payment into it")
	assert.Equal(t, "wharf-bank", fill["to"])

	// Posting exactly what that suggestion fills reproduces the original movement.
	form := scopedEntry("brisk", fill["kind"], "Cash Advance", fill["amount"], "to", fill["to"], "direction", fill["direction"])
	resp := postRaw(t, srv, "/quickadd", form)
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	again := txnFromDB(t, conn, 2)
	assert.Equal(t, "BRISK", again.Splits[0].AccountName)
	assert.EqualValues(t, -10000, again.Splits[0].Amount)
	assert.Equal(t, "Wharf Bank", again.Splits[1].AccountName)
}
