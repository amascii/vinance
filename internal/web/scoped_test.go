package web_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/db/gen"
	"github.com/amascii/vinance/internal/ledger"
	"github.com/amascii/vinance/internal/testutil"
)

// scopedEntry builds the posted fields of the entry form on an account's register: the account is
// the scope (a hidden field), and a transfer names the other account (to) and a direction.
func scopedEntry(scope, kind, description, amount string, extra ...string) url.Values {
	v := url.Values{
		"scope": {scope}, "return": {"/transactions?account=" + scope}, "kind": {kind},
		"date": {"2026-10-01"}, "description": {description}, "amount": {amount},
	}
	for i := 0; i+1 < len(extra); i += 2 {
		v.Set(extra[i], extra[i+1])
	}
	return v
}

func TestRegisterHasTheEntryFormLockedToTheAccount(t *testing.T) {
	srv, conn := newApp(t)
	newSeeder(t, conn).spend("2026-09-01", "x", 100)

	_, d := get(t, srv, "/transactions?account=brisk")
	bar := d.Find("section#qa-panel.scoped-entry")
	require.Equal(t, 1, bar.Length())
	assert.Equal(t, "brisk", attr(bar.Find("input[name=scope]"), "value"), "the account is fixed by a hidden field")
	assert.Equal(t, "/transactions?account=brisk", attr(bar.Find("input[name=return]"), "value"))
	assert.Equal(t, 0, bar.Find("select[name=account]").Length(), "there is no account to choose")
	assert.Equal(t, "/quickadd", attr(bar.Find("form"), "hx-post"), "the same endpoint the home page uses")
	assert.Equal(t, "/quickadd/suggest/description", attr(bar.Find("input[name=description]"), "hx-post"))
	assert.Equal(t, "expense", radio(d, "kind"))

	// A transfer names the OTHER account (never this one) and says which way the money goes.
	var others []string
	bar.Find("select[name=to] option").Each(func(_ int, o *goquery.Selection) { others = append(others, attr(o, "value")) })
	assert.Equal(t, []string{"neo", "wharf-bank"}, others)
	assert.Equal(t, "in", attr(bar.Find("select[name=direction] option[selected]"), "value"), "paying a card is the usual thing to do with one")
	assert.Equal(t, "Out of BRISK to", rowTexts(bar.Find("select[name=direction] option").First()))
	assert.Equal(t, "Into BRISK from", rowTexts(bar.Find("select[name=direction] option").Last()))

	// A bank defaults the other way.
	_, d = get(t, srv, "/transactions?account=wharf-bank")
	assert.Equal(t, "out", attr(d.Find(".scoped-entry select[name=direction] option[selected]"), "value"))

	// With filters applied the form is still there and returns to the same filtered view.
	_, d = get(t, srv, "/transactions?account=brisk&tag=x")
	assert.Equal(t, "/transactions?account=brisk&tag=x", attr(d.Find(".scoped-entry input[name=return]"), "value"))

	// Not on the general list, a tag view, a built-in account, or an unknown one.
	for _, path := range []string{"/transactions", "/transactions?tag=x", "/transactions?account=expenses", "/transactions?account=nope"} {
		_, d = get(t, srv, path)
		assert.Equal(t, 0, d.Find(".scoped-entry").Length(), path)
	}
}

func TestRegisterFormStartsOnTheRememberedDate(t *testing.T) {
	srv, _ := newApp(t)
	cookie := &http.Cookie{Name: "vinance_date", Value: "2026-09-24"}
	_, d := withCookies(t, srv, http.MethodGet, "/transactions?account=brisk", nil, []*http.Cookie{cookie})
	assert.Equal(t, "2026-09-24", attr(d.Find(".scoped-entry input[name=date]"), "value"), "the day you were working in carries over")
}

func TestArchivedAccountHasNoEntryForm(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	require.NoError(t, ledger.NewService(conn).SetAccountArchived(context.Background(), s.acct["brisk"], true))
	_, d := get(t, srv, "/transactions?account=brisk")
	assert.Equal(t, 0, d.Find(".scoped-entry").Length())
	assert.Contains(t, d.Find("p.hint").First().Text(), "BRISK is archived")
}

func TestScopedSpendAndIncome(t *testing.T) {
	srv, conn := newApp(t)

	resp := postRaw(t, srv, "/quickadd", scopedEntry("brisk", "expense", "Coffee", "4.50", "tags", "#cafes"))
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, "/transactions?account=brisk", resp.Header.Get("Location"))
	got := txnFromDB(t, conn, 1)
	assert.Equal(t, "Coffee", got.Description)
	assert.Equal(t, "2026-10-01", got.Date)
	assert.Equal(t, "BRISK", got.Splits[0].AccountName)
	assert.EqualValues(t, -450, got.Splits[0].Amount)
	assert.Equal(t, []string{"cafes"}, got.Splits[1].Tags)
	assert.Equal(t, "brisk", cookieNamed(resp, "vinance_acct").Value, "remembered for the home page too")
	assert.True(t, cookieNamed(resp, "vinance_date").MaxAge < 0)

	// The page it lands on confirms and shows the new balance and row.
	_, d := withCookies(t, srv, http.MethodGet, resp.Header.Get("Location"), nil, setCookies(resp))
	assert.Equal(t, "Added Coffee $4.50 (BRISK)", d.Find(".flash").Text())
	assert.Equal(t, "BRISK · Owed $4.50", d.Find("p.account-header").Text())
	assert.Equal(t, []string{"Coffee | -$4.50 | Owed $4.50"}, regRows(d))

	// htmx submits get an HX-Redirect so the whole register (balances included) refreshes.
	resp = postRaw(t, srv, "/quickadd", scopedEntry("brisk", "expense", "Tea", "3"), "HX-Request", "true")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "/transactions?account=brisk", resp.Header.Get("HX-Redirect"))

	// Income into the register's account.
	resp = postRaw(t, srv, "/quickadd", scopedEntry("wharf-bank", "income", "Refund", "12.50", "date", "2026-09-24"))
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	inc := txnFromDB(t, conn, 3)
	assert.Equal(t, ledger.DirIn, inc.Summary().Direction)
	assert.Equal(t, "Wharf Bank", inc.Splits[0].AccountName)
	assert.Equal(t, "2026-09-24", inc.Date)
	assert.Equal(t, "2026-09-24", cookieNamed(resp, "vinance_date").Value, "a past date is remembered, as on the home page")
}

func TestScopedAddAlwaysUsesTheRegistersAccount(t *testing.T) {
	srv, conn := newApp(t)
	// A different remembered account, and even a forged account field, can't redirect the entry.
	neo := &http.Cookie{Name: "vinance_acct", Value: "neo"}
	form := scopedEntry("brisk", "expense", "Coffee", "4.50", "account", "neo")
	resp, _ := withCookies(t, srv, http.MethodPost, "/quickadd", form, []*http.Cookie{neo})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "/transactions?account=brisk", resp.Header.Get("HX-Redirect"))
	assert.Equal(t, "BRISK", txnFromDB(t, conn, 1).Splits[0].AccountName)
}

// pesoAccounts adds Neo Credit (a MXN card) next to the default Neo (MXN bank) and charges $1,000 to the card.
func pesoAccounts(t *testing.T, conn *sql.DB) (neo, card int64) {
	t.Helper()
	neo = accountID(t, conn, "neo")
	card = testutil.SeedAccount(t, conn, "Neo Credit", "liability", "MXN")
	exp, err := gen.New(conn).GetBuiltinAccount(context.Background(), "expense")
	require.NoError(t, err)
	_, err = ledger.NewService(conn).Create(context.Background(), ledger.TxnInput{
		Date: "2026-09-20", Description: "Groceries", Currency: "MXN",
		Splits: []ledger.SplitInput{
			{AccountID: card, Currency: "MXN", Amount: -100000, Value: -100000},
			{AccountID: exp.ID, Currency: "MXN", Amount: 100000, Value: 100000, Tags: []string{"groceries"}},
		},
	})
	require.NoError(t, err)
	return neo, card
}

func TestPayingACardFromTheCardsRegister(t *testing.T) {
	srv, conn := newApp(t)
	pesoAccounts(t, conn)

	// "Neo Credit Card Payment 300.00" on 2026-10-03, typed on the Neo Credit register: money comes INTO the card from Neo.
	form := scopedEntry("neo-credit", "transfer", "Neo Credit Card Payment", "300.00", "to", "neo", "direction", "in", "date", "2026-10-03")
	resp := postRaw(t, srv, "/quickadd", form)
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)

	got := txnFromDB(t, conn, 2)
	assert.Equal(t, "2026-10-03", got.Date)
	assert.Equal(t, ledger.DirTransfer, got.Summary().Direction, "a transfer, not an expense")
	assert.Equal(t, "Neo", got.Splits[0].AccountName)
	assert.EqualValues(t, -30000, got.Splits[0].Amount, "money leaves the bank")
	assert.Equal(t, "Neo Credit", got.Splits[1].AccountName)
	assert.EqualValues(t, 30000, got.Splits[1].Amount, "and arrives in the card: what is owed goes DOWN")
	assert.Equal(t, "neo-credit", cookieNamed(resp, "vinance_acct").Value, "the register's account is the remembered one, not the other")

	_, d := withCookies(t, srv, http.MethodGet, "/transactions?account=neo-credit", nil, setCookies(resp))
	assert.Equal(t, "Added Neo Credit Card Payment MX$300.00 (Neo → Neo Credit)", d.Find(".flash").Text())
	assert.Equal(t, "Neo Credit · Owed MX$700.00", d.Find("p.account-header").Text(), "$1,000.00 charged, $300.00 paid")
	assert.Equal(t, "Neo Credit Card Payment | +MX$300.00 | Owed MX$700.00", regRows(d)[0])
	_, d = get(t, srv, "/transactions?account=neo")
	assert.Equal(t, "Neo Credit Card Payment | -MX$300.00 | Balance -MX$300.00", regRows(d)[0])
	rep, err := ledger.NewService(conn).TagReport(context.Background(), ledger.ReportFilter{})
	require.NoError(t, err)
	assert.EqualValues(t, 1, rep.Total.Splits, "only the $1,000 grocery charge is spending")
}

func TestPayingTheSameCardFromTheBanksRegister(t *testing.T) {
	srv, conn := newApp(t)
	pesoAccounts(t, conn)
	resp := postRaw(t, srv, "/quickadd", scopedEntry("neo", "transfer", "Neo Credit Card Payment", "300.00", "to", "neo-credit", "direction", "out"))
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	got := txnFromDB(t, conn, 2)
	assert.Equal(t, "Neo", got.Splits[0].AccountName)
	assert.EqualValues(t, -30000, got.Splits[0].Amount, "out of this account")
	assert.Equal(t, "Neo Credit", got.Splits[1].AccountName)
	assert.EqualValues(t, 30000, got.Splits[1].Amount, "identical to entering it on the card's page")
}

func TestDirectionDecidesWhichWayAScopedTransferGoes(t *testing.T) {
	srv, conn := newApp(t)
	pesoAccounts(t, conn)

	// On the card, "Out of Neo Credit" is a cash advance: owed goes up, the bank receives.
	postRaw(t, srv, "/quickadd", scopedEntry("neo-credit", "transfer", "Cash advance", "500", "to", "neo", "direction", "out"))
	adv := txnFromDB(t, conn, 2)
	assert.Equal(t, "Neo Credit", adv.Splits[0].AccountName)
	assert.EqualValues(t, -50000, adv.Splits[0].Amount)
	assert.Equal(t, "Neo", adv.Splits[1].AccountName)
	assert.EqualValues(t, 50000, adv.Splits[1].Amount)

	// On the bank, "Into Neo" means the money arrives here from the other account.
	postRaw(t, srv, "/quickadd", scopedEntry("neo", "transfer", "Transfer in", "200", "to", "neo-credit", "direction", "in"))
	in := txnFromDB(t, conn, 3)
	assert.Equal(t, "Neo Credit", in.Splits[0].AccountName)
	assert.EqualValues(t, -20000, in.Splits[0].Amount)
	assert.Equal(t, "Neo", in.Splits[1].AccountName)
	assert.EqualValues(t, 20000, in.Splits[1].Amount)

	// No direction sent means out of this account.
	postRaw(t, srv, "/quickadd", scopedEntry("neo", "transfer", "Default", "1", "to", "neo-credit"))
	def := txnFromDB(t, conn, 4)
	assert.Equal(t, "Neo", def.Splits[0].AccountName)
	assert.EqualValues(t, -100, def.Splits[0].Amount)
}

func TestScopedTransferBetweenTwoBanks(t *testing.T) {
	srv, conn := newApp(t)
	testutil.SeedAccount(t, conn, "Rainy Day Fund", "asset", "USD")
	postRaw(t, srv, "/quickadd", scopedEntry("wharf-bank", "transfer", "Move to savings", "300", "to", "rainy-day-fund", "direction", "out", "tags", "savings"))
	got := txnFromDB(t, conn, 1)
	assert.Equal(t, "Wharf Bank", got.Splits[0].AccountName)
	assert.EqualValues(t, -30000, got.Splits[0].Amount)
	assert.Equal(t, "Rainy Day Fund", got.Splits[1].AccountName)
	assert.Equal(t, []string{"savings"}, got.Splits[1].Tags)
}

func TestScopedTransferMistakesAreRefused(t *testing.T) {
	srv, conn := newApp(t) // BRISK (USD card), Wharf Bank (USD bank), Neo (MXN bank)
	tests := []struct {
		name string
		form url.Values
		want string
	}{
		{"the same account", scopedEntry("brisk", "transfer", "Pay", "5", "to", "brisk"), "Choose the other account"},
		{"no other account", scopedEntry("brisk", "transfer", "Pay", "5", "to", ""), "Choose the other account"},
		{"an unknown account", scopedEntry("brisk", "transfer", "Pay", "5", "to", "nope"), "Choose the other account"},
		{"a built-in account", scopedEntry("brisk", "transfer", "Pay", "5", "to", "expenses"), "Choose the other account"},
		{"between currencies", scopedEntry("brisk", "transfer", "Pay", "5", "to", "neo"), "currency conversion"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, d := post(t, srv, "/quickadd", tc.form)
			require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
			assert.Contains(t, d.Find(".problems").Text(), tc.want)
			assert.Equal(t, "Pay", field(d, "description"), "what was entered is kept")
			assert.Equal(t, "transfer", radio(d, "kind"))
			assert.Equal(t, "brisk", attr(d.Find("input[name=scope]"), "value"))
			assert.Equal(t, 0, d.Find("title").Length(), "an htmx error is just the form, not a whole page")
		})
	}
	assert.Equal(t, 0, count(t, conn, "transactions"))
}

func TestScopedValidationErrorsAndFallback(t *testing.T) {
	srv, conn := newApp(t)
	tests := []struct {
		form url.Values
		want string
	}{
		{scopedEntry("brisk", "expense", "Coffee", ""), "Enter an amount"},
		{scopedEntry("brisk", "expense", "", "4.50"), "Enter a description."},
		{scopedEntry("brisk", "expense", "Coffee", "4.50", "date", ""), "Pick a date."},
		{scopedEntry("brisk", "expense", "Coffee", "0"), "greater than zero"},
	}
	for _, tc := range tests {
		resp, d := post(t, srv, "/quickadd", tc.form)
		require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, tc.want)
		assert.Contains(t, d.Find(".problems").Text(), tc.want)
	}

	// Without htmx the rejection is a complete page with a way back.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/quickadd", stringsReader(scopedEntry("brisk", "expense", "Coffee", "").Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := noRedirectClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	d := doc(t, resp)
	assert.Equal(t, "Add to BRISK · vinance", d.Find("title").Text())
	assert.Contains(t, d.Find(".problems").Text(), "Enter an amount")
	assert.Equal(t, "/transactions?account=brisk", attr(d.Find("main > p > a"), "href"))
	assert.Equal(t, 0, count(t, conn, "transactions"))
}

func TestScopedAddOnlyForRealActiveAccounts(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	require.NoError(t, ledger.NewService(conn).SetAccountArchived(context.Background(), s.acct["neo"], true))
	for _, slug := range []string{"expenses", "income", "no-such-account", "neo"} {
		resp := postRaw(t, srv, "/quickadd", scopedEntry(slug, "expense", "Coffee", "4.50"))
		assert.Equal(t, http.StatusNotFound, resp.StatusCode, slug)
	}
	assert.Equal(t, 0, count(t, conn, "transactions"))
}

func TestScopedReturnURLCannotBeAnOpenRedirect(t *testing.T) {
	srv, _ := newApp(t)
	for _, evil := range []string{"//evil.example", "https://evil.example", `/\evil.example`, ""} {
		form := scopedEntry("brisk", "expense", "Coffee", "4.50")
		form.Set("return", evil)
		resp := postRaw(t, srv, "/quickadd", form)
		assert.Equal(t, "/transactions?account=brisk", resp.Header.Get("Location"), "return=%q", evil)
	}
}

func TestScopedAddRejectsCrossOrigin(t *testing.T) {
	srv, conn := newApp(t)
	resp := postRaw(t, srv, "/quickadd", scopedEntry("brisk", "expense", "Coffee", "4.50"), "Sec-Fetch-Site", "cross-site")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, 0, count(t, conn, "transactions"))
}
