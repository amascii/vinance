package web_test

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/ledger"
	"github.com/amascii/vinance/internal/testutil"
)

func rowTexts(d *goquery.Selection) string { return strings.Join(strings.Fields(d.Text()), " ") }

// entry builds the posted fields of the home page's entry form. The date defaults to the test
// clock's today (2026-10-01); pass extra key/value pairs to set or override fields.
func entry(kind, account, description, amount string, extra ...string) url.Values {
	v := url.Values{"kind": {kind}, "account": {account}, "date": {"2026-10-01"}, "description": {description}, "amount": {amount}}
	for i := 0; i+1 < len(extra); i += 2 {
		v.Set(extra[i], extra[i+1])
	}
	return v
}

// radio returns the value of the checked radio button with the given name inside the form.
func radio(d *goquery.Document, name string) string {
	if sel := d.Find("form#qa-form select[name=" + name + "] option[selected]").First(); sel.Length() > 0 {
		return attr(sel, "value")
	}
	return attr(d.Find("form#qa-form input[name="+name+"][checked]").First(), "value")
}

// field returns the value attribute of a named input in the entry form.
func field(d *goquery.Document, name string) string {
	return attr(d.Find("form#qa-form input[name="+name+"]").First(), "value")
}

// chosen returns the selected option's value of a named select in the entry form.
func chosen(d *goquery.Document, name string) string {
	return attr(d.Find("form#qa-form select[name="+name+"] option[selected]").First(), "value")
}

func TestHomeListsRecentTransactions(t *testing.T) {
	srv, conn := newApp(t)
	_, err := ledger.NewService(conn).Create(context.Background(), ledger.TxnInput{
		Date: "2026-09-30", Description: "Dairy Queen", Currency: "USD",
		Splits: []ledger.SplitInput{
			{AccountID: accountID(t, conn, "brisk"), Currency: "USD", Amount: -1250, Value: -1250},
			{AccountID: 1, Currency: "USD", Amount: 1250, Value: 1250, Tags: []string{"fast-food"}},
		},
	})
	require.NoError(t, err)

	resp, d := get(t, srv, "/")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	rows := d.Find("#recent li.txn")
	require.Equal(t, 1, rows.Length())
	assert.Equal(t, "Dairy Queen", rows.Find(".desc").Text())
	assert.Equal(t, "-$12.50", rows.Find(".amount").Text())
	assert.Equal(t, "2026-09-30", rows.Find(".date").Text())
	assert.Equal(t, "BRISK", rows.Find(".chip.account").Text())
	assert.Equal(t, "#fast-food", rows.Find(".chip.tag").Text())
	assert.True(t, rows.HasClass("dir-out"))
}

func TestHomeBannerCountsImbalances(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)

	_, d := get(t, srv, "/")
	assert.Equal(t, 0, d.Find(".imbalance-banner").Length(), "no banner when nothing needs fixing")

	imbalanced := func(desc string) {
		_, err := s.svc.Create(context.Background(), ledger.TxnInput{
			Date: "2026-09-02", Description: desc, Currency: "USD",
			Splits: []ledger.SplitInput{
				{AccountID: s.acct["brisk"], Currency: "USD", Amount: -100, Value: -100},
				{AccountID: s.acct["imbalance"], Currency: "USD", Amount: 100, Value: 100},
			},
		})
		require.NoError(t, err)
	}
	imbalanced("one")
	_, d = get(t, srv, "/")
	banner := d.Find(".imbalance-banner a")
	assert.Equal(t, "1 transaction needs fixing →", rowTexts(banner))
	href, _ := banner.Attr("href")
	assert.Equal(t, "/transactions?imbalance=1", href)

	imbalanced("two")
	s.spend("2026-09-03", "fine", 100)
	_, d = get(t, srv, "/")
	assert.Equal(t, "2 transactions need fixing →", rowTexts(d.Find(".imbalance-banner a")))

	// The banner stays after a quick-add (the panel is re-rendered).
	_, d = post(t, srv, "/quickadd", entry("expense", "brisk", "gum", "5"))
	assert.Equal(t, "2 transactions need fixing →", rowTexts(d.Find(".imbalance-banner a")))
}

func TestHomeWelcomesANewUser(t *testing.T) {
	srv, conn := testutil.NewServerWithDB(t) // no accounts
	_, d := get(t, srv, "/")
	assert.Equal(t, 1, d.Find(".first-run").Length())
	href, _ := d.Find(".first-run a").Attr("href")
	assert.Equal(t, "/accounts/manage", href)

	testutil.SeedDefaultAccounts(t, conn)
	_, d = get(t, srv, "/")
	assert.Equal(t, 0, d.Find(".first-run").Length(), "gone as soon as an account exists")
}

func TestHomeShowsTheEntryForm(t *testing.T) {
	srv, conn := newApp(t)
	newSeeder(t, conn).spend("2026-09-01", "x", 100)

	resp, d := get(t, srv, "/")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	form := d.Find("form#qa-form")
	require.Equal(t, 1, form.Length())
	assert.Equal(t, "/quickadd", attr(form, "hx-post"))
	assert.Equal(t, "#qa-panel", attr(form, "hx-target"))
	assert.Equal(t, "2026-10-01", attr(form, "data-today"), "today, for the date hint")
	assert.Equal(t, 0, d.Find("#qa-input").Length(), "the free-text bar is gone")

	var kinds []string
	form.Find("select[name=kind] option").Each(func(_ int, in *goquery.Selection) { kinds = append(kinds, attr(in, "value")) })
	assert.Equal(t, []string{"expense", "income", "transfer"}, kinds)
	assert.Equal(t, "expense", radio(d, "kind"), "Spend is the default")

	var accounts []string
	form.Find("select[name=account] option").Each(func(_ int, o *goquery.Selection) { accounts = append(accounts, o.Text()) })
	assert.Equal(t, []string{"BRISK", "Neo", "Wharf Bank"}, accounts, "real accounts only, no built-ins")
	assert.Equal(t, "MXN", attr(form.Find("select[name=account] option[value=neo]"), "data-currency"))
	assert.Equal(t, "brisk", chosen(d, "account"), "the account most recently spent from")
	assert.Equal(t, 1, form.Find("select[name=to]").Length(), "a transfer's other account (shown only for Transfer)")

	date := form.Find("input[name=date]")
	assert.Equal(t, "date", attr(date, "type"), "a calendar picker, not typed text")
	assert.Equal(t, "2026-10-01", attr(date, "value"))
	assert.Equal(t, 0, form.Find("[data-date-step], [data-date-today]").Length(), "just the picker, no day-step or Today buttons")

	desc := form.Find("input[name=description]")
	for _, a := range []string{"required", "autofocus"} {
		_, has := desc.Attr(a)
		assert.True(t, has, a)
	}
	assert.Equal(t, "/quickadd/suggest/description", attr(desc, "hx-post"))
	assert.Equal(t, "#qa-desc-suggestions", attr(desc, "hx-target"), "nested htmx elements set their own target (they inherit the form's otherwise)")
	assert.Equal(t, "innerHTML", attr(desc, "hx-swap"))
	assert.Equal(t, "decimal", attr(form.Find("input[name=amount]"), "inputmode"))
	tags := form.Find("input[name=tags]")
	assert.Equal(t, "/quickadd/suggest/tags", attr(tags, "hx-post"))
	assert.Equal(t, "#qa-tag-suggestions", attr(tags, "hx-target"))
	assert.Equal(t, 0, form.Find("input[name=scope]").Length(), "the home form is not locked to an account")
}

func TestSubmitSpendCreatesTheTransaction(t *testing.T) {
	srv, conn := newApp(t)
	resp, d := post(t, srv, "/quickadd", entry("expense", "brisk", "McDonald's", "12.50", "tags", "#fast-food"))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	assert.Equal(t, 1, count(t, conn, "transactions"))
	got := txnFromDB(t, conn, 1)
	assert.Equal(t, "McDonald's", got.Description)
	assert.Equal(t, "2026-10-01", got.Date)
	assert.Equal(t, "BRISK", got.Splits[0].AccountName)
	assert.EqualValues(t, -1250, got.Splits[0].Amount)
	assert.Equal(t, "Expenses", got.Splits[1].AccountName)
	assert.EqualValues(t, 1250, got.Splits[1].Amount)
	assert.Equal(t, []string{"fast-food"}, got.Splits[1].Tags)

	// The panel comes back with a confirmation, the new row on top, and a form ready for the next one.
	assert.Equal(t, "Added McDonald's $12.50 (BRISK)", d.Find(".flash").Text())
	assert.Equal(t, "McDonald's", d.Find("#recent li.txn").First().Find(".desc").Text())
	assert.Equal(t, "-$12.50", d.Find("#recent li.txn").First().Find(".amount").Text())
	assert.Equal(t, "", field(d, "description"))
	assert.Equal(t, "", field(d, "amount"))
	assert.Equal(t, "", field(d, "tags"))
	assert.Equal(t, "brisk", chosen(d, "account"), "the account just used stays selected")
	assert.Equal(t, "expense", radio(d, "kind"))
	assert.Equal(t, "brisk", cookieNamed(resp, "vinance_acct").Value)
	assert.True(t, cookieNamed(resp, "vinance_date").MaxAge < 0, "dated today: nothing to remember")
}

func TestSubmitIncomeAndTransfer(t *testing.T) {
	srv, conn := newApp(t)
	svc := ledger.NewService(conn)

	resp, d := post(t, srv, "/quickadd", entry("income", "wharf-bank", "Employer paycheck", "2,000.00", "tags", "salary"))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	inc := txnFromDB(t, conn, 1)
	assert.Equal(t, ledger.DirIn, inc.Summary().Direction)
	assert.EqualValues(t, 200000, inc.Summary().Net)
	assert.Equal(t, "Wharf Bank", inc.Splits[0].AccountName)
	assert.Equal(t, "Income", inc.Splits[1].AccountName)
	assert.Equal(t, []string{"salary"}, inc.Splits[1].Tags)
	assert.Equal(t, "+$2,000.00", d.Find("#recent li.txn").First().Find(".amount").Text())

	resp, d = post(t, srv, "/quickadd", entry("transfer", "wharf-bank", "Card payment", "500", "to", "brisk"))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	tr := txnFromDB(t, conn, 2)
	assert.Equal(t, ledger.DirTransfer, tr.Summary().Direction)
	assert.Equal(t, "Wharf Bank", tr.Splits[0].AccountName)
	assert.EqualValues(t, -50000, tr.Splits[0].Amount, "out of the account")
	assert.Equal(t, "BRISK", tr.Splits[1].AccountName)
	assert.EqualValues(t, 50000, tr.Splits[1].Amount, "into the card: what is owed goes down")
	assert.Equal(t, "Added Card payment $500.00 (Wharf Bank → BRISK)", d.Find(".flash").Text())
	assert.Equal(t, "wharf-bank", cookieNamed(resp, "vinance_acct").Value, "a transfer remembers the account it came from")

	bs, err := svc.BalanceSheet(context.Background())
	require.NoError(t, err)
	assert.EqualValues(t, 1500_00, byName(bs.Assets)["Wharf Bank"].Balance)
}

func TestSubmitsAreInTheAccountsOwnCurrency(t *testing.T) {
	srv, conn := newApp(t)
	post(t, srv, "/quickadd", entry("expense", "neo", "Internet bill", "500"))
	got := txnFromDB(t, conn, 1)
	assert.Equal(t, "MXN", got.Currency)
	assert.EqualValues(t, -50000, got.Splits[0].Amount)
}

func TestSubmitWithAPastDateIsRememberedUntilYouWorkOnToday(t *testing.T) {
	srv, conn := newApp(t)

	resp, d := post(t, srv, "/quickadd", entry("expense", "wharf-bank", "Coffee beans", "12.50", "date", "2026-09-24"))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "2026-09-24", txnFromDB(t, conn, 1).Date)
	assert.Equal(t, "2026-09-24", field(d, "date"), "the form stays on the day you are working through")
	assert.Equal(t, "2026-09-24", cookieNamed(resp, "vinance_date").Value)

	// A later visit (a reload, a new tab) starts from the same account and day.
	cookies := setCookies(resp)
	_, home := withCookies(t, srv, http.MethodGet, "/", nil, cookies)
	assert.Equal(t, "2026-09-24", field(home, "date"))
	assert.Equal(t, "wharf-bank", chosen(home, "account"))

	// Moving on to the next day is just picking another date.
	resp, d = withCookies(t, srv, http.MethodPost, "/quickadd", entry("expense", "wharf-bank", "Dinner", "30", "date", "2026-09-25"), cookies)
	assert.Equal(t, "2026-09-25", field(d, "date"))
	assert.Equal(t, "2026-09-25", cookieNamed(resp, "vinance_date").Value)

	// Entering something dated today forgets the remembered date, so tomorrow starts on tomorrow.
	resp, d = withCookies(t, srv, http.MethodPost, "/quickadd", entry("expense", "brisk", "Gum", "2"), setCookies(resp))
	assert.Equal(t, "2026-10-01", field(d, "date"))
	assert.True(t, cookieNamed(resp, "vinance_date").MaxAge < 0)
	assert.Equal(t, "brisk", cookieNamed(resp, "vinance_acct").Value, "the account moves with the last entry")
}

func TestRememberedDateCookie(t *testing.T) {
	srv, _ := newApp(t)
	_, d := withCookies(t, srv, http.MethodGet, "/", nil, []*http.Cookie{{Name: "vinance_date", Value: "2026-09-28"}})
	assert.Equal(t, "2026-09-28", field(d, "date"))
	_, d = withCookies(t, srv, http.MethodGet, "/", nil, []*http.Cookie{{Name: "vinance_date", Value: "not-a-date"}})
	assert.Equal(t, "2026-10-01", field(d, "date"), "a garbage date cookie is ignored")
}

func TestSubmitRejectsBadEntriesAndKeepsWhatWasEntered(t *testing.T) {
	srv, conn := newApp(t)
	tests := []struct {
		name string
		form url.Values
		want string
	}{
		{"no description", entry("expense", "brisk", "  ", "5"), "Enter a description."},
		{"no amount", entry("expense", "brisk", "Coffee", ""), "Enter an amount"},
		{"amount isn't a number", entry("expense", "brisk", "Coffee", "lots"), "amount"},
		{"too many decimals", entry("expense", "brisk", "Coffee", "1.234"), "decimal places"},
		{"zero", entry("expense", "brisk", "Coffee", "0"), "greater than zero"},
		{"negative: the type decides the direction", entry("expense", "brisk", "Coffee", "-5"), "greater than zero"},
		{"no date", entry("expense", "brisk", "Coffee", "5", "date", ""), "Pick a date."},
		{"impossible date", entry("expense", "brisk", "Coffee", "5", "date", "2026-13-45"), "Pick a date."},
		{"unknown account", entry("expense", "nope", "Coffee", "5"), "Choose an account."},
		{"a built-in account", entry("expense", "expenses", "Coffee", "5"), "Choose an account."},
		{"unknown type", entry("barter", "brisk", "Coffee", "5"), "Choose Spend, Income or Transfer."},
		{"transfer to the same account", entry("transfer", "brisk", "Pay", "5", "to", "brisk"), "two different accounts"},
		{"transfer with no destination", entry("transfer", "brisk", "Pay", "5", "to", ""), "two different accounts"},
		{"transfer between currencies", entry("transfer", "brisk", "Pay", "5", "to", "neo"), "currency conversion"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, d := post(t, srv, "/quickadd", tc.form)
			require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
			assert.Contains(t, d.Find(".problems").Text(), tc.want)
			assert.Equal(t, strings.TrimSpace(tc.form.Get("description")), field(d, "description"), "what was typed is kept")
			assert.Equal(t, tc.form.Get("amount"), field(d, "amount"))
			assert.Equal(t, 0, d.Find(".flash").Length())
			assert.Nil(t, cookieNamed(resp, "vinance_acct"), "a refused entry is not remembered")
		})
	}
	assert.Equal(t, 0, count(t, conn, "transactions"))

	// The choices made are kept too, so fixing one field doesn't mean redoing the rest.
	_, d := post(t, srv, "/quickadd", entry("transfer", "wharf-bank", "Pay", "", "to", "brisk", "date", "2026-09-24", "tags", "#a #b"))
	assert.Equal(t, "transfer", radio(d, "kind"))
	assert.Equal(t, "wharf-bank", chosen(d, "account"))
	assert.Equal(t, "brisk", chosen(d, "to"))
	assert.Equal(t, "2026-09-24", field(d, "date"))
	assert.Equal(t, "#a #b", field(d, "tags"))
}

func TestSubmitWithoutHTMXReturnsFullPages(t *testing.T) {
	srv, conn := newApp(t)
	post0 := func(form url.Values) (*http.Response, *goquery.Document) {
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/quickadd", strings.NewReader(form.Encode()))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded") // no HX-Request header
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		status := resp.StatusCode
		d := doc(t, resp)
		resp.StatusCode = status
		return resp, d
	}
	resp, d := post0(entry("expense", "brisk", "McDonald's", "12.50"))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "vinance", d.Find("title").Text(), "a complete page, not a fragment")
	assert.Equal(t, 1, d.Find("#recent li.txn").Length())
	assert.Equal(t, 1, count(t, conn, "transactions"))

	resp, d = post0(entry("expense", "brisk", "", "12.50"))
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	assert.Equal(t, "vinance", d.Find("title").Text())
	assert.Contains(t, d.Find(".problems").Text(), "Enter a description.")

	_, home := get(t, srv, "/")
	assert.Equal(t, "post", attr(home.Find("form#qa-form"), "method"), "the form also works as a plain POST")
	assert.Equal(t, "/quickadd", attr(home.Find("form#qa-form"), "action"))
}

func TestSubmitRejectsCrossOrigin(t *testing.T) {
	srv, conn := newApp(t)
	resp := postRaw(t, srv, "/quickadd", entry("expense", "brisk", "x", "5"), "Sec-Fetch-Site", "cross-site")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, 0, count(t, conn, "transactions"))
	resp = postRaw(t, srv, "/quickadd", entry("expense", "brisk", "x", "5"), "Sec-Fetch-Site", "same-origin")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestTheFormOnlyOffersUsableAccounts(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	require.NoError(t, ledger.NewService(conn).SetAccountArchived(context.Background(), s.acct["neo"], true))

	_, d := get(t, srv, "/")
	var offered []string
	d.Find("form#qa-form select[name=account] option").Each(func(_ int, o *goquery.Selection) { offered = append(offered, attr(o, "value")) })
	assert.Equal(t, []string{"brisk", "wharf-bank"}, offered, "an archived account is not offered")

	resp, _ := post(t, srv, "/quickadd", entry("expense", "neo", "x", "5"))
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, "and can't be posted to either")
	assert.Equal(t, 0, count(t, conn, "transactions"))
}

func TestNewAccountsAreUsableImmediately(t *testing.T) {
	srv, conn := newApp(t)
	testutil.SeedAccount(t, conn, "Prime Card", "liability", "USD")
	resp, _ := post(t, srv, "/quickadd", entry("expense", "prime-card", "Lunch", "12.50", "tags", "food"))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "Prime Card", txnFromDB(t, conn, 1).Splits[0].AccountName)
}

func TestDefaultAccountForTheForm(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)

	_, d := get(t, srv, "/")
	assert.Equal(t, "brisk", chosen(d, "account"), "with no history the first account is selected")

	s.spend("2026-09-01", "x", 100)
	_, d = get(t, srv, "/")
	assert.Equal(t, "brisk", chosen(d, "account"), "the account most recently spent from")

	// A remembered account beats it; an unusable remembered account is ignored.
	neo := &http.Cookie{Name: "vinance_acct", Value: "neo"}
	_, d = withCookies(t, srv, http.MethodGet, "/", nil, []*http.Cookie{neo})
	assert.Equal(t, "neo", chosen(d, "account"))
	for _, slug := range []string{"deleted-account", "expenses"} {
		_, d = withCookies(t, srv, http.MethodGet, "/", nil, []*http.Cookie{{Name: "vinance_acct", Value: slug}})
		assert.Equal(t, "brisk", chosen(d, "account"), "cookie %q is not usable", slug)
	}
	require.NoError(t, ledger.NewService(conn).SetAccountArchived(context.Background(), s.acct["neo"], true))
	_, d = withCookies(t, srv, http.MethodGet, "/", nil, []*http.Cookie{neo})
	assert.Equal(t, "brisk", chosen(d, "account"), "archived")
}

func TestTransferDestinationStartsOnADifferentAccount(t *testing.T) {
	srv, _ := newApp(t)
	_, d := get(t, srv, "/")
	assert.Equal(t, "brisk", chosen(d, "account"))
	assert.Equal(t, "neo", chosen(d, "to"), "so a transfer isn't refused for naming one account twice")
}

func TestTagSuggestions(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	s.spend("2026-09-01", "a", 100, "snacks")
	s.spend("2026-09-02", "b", 100, "snacks")
	s.spend("2026-09-03", "c", 100, "snacks", "snack-bar")
	s.spend("2026-09-04", "d", 100, "stationary")

	suggest := func(text string, pos int) []string {
		_, d := post(t, srv, "/quickadd/suggest/tags", url.Values{"tags": {text}, "pos": {strconv.Itoa(pos)}})
		var out []string
		d.Find("button.tag-suggestion").Each(func(_ int, b *goquery.Selection) { out = append(out, attr(b, "data-tag")) })
		return out
	}
	assert.Equal(t, []string{"snacks", "snack-bar"}, suggest("sn", 2), "most used first")
	assert.Equal(t, []string{"snacks", "snack-bar"}, suggest("#sn", 3), "a leading # is fine")
	assert.Equal(t, []string{"snacks", "snack-bar", "stationary"}, suggest("S", 1), "case-insensitive")
	assert.Equal(t, []string{"snack-bar"}, suggest("snacks sn", 9), "only the word at the caret, and tags already entered aren't offered again")
	assert.Equal(t, []string{"snacks", "snack-bar", "stationary"}, suggest("", 0), "an empty field offers the most used tags")
	assert.Equal(t, []string{"snack-bar", "stationary"}, suggest("snacks ", 7), "after a finished tag, the others")
	assert.Equal(t, []string{"snacks", "snack-bar"}, suggest("sn x", 2), "the caret decides which word")
	assert.Empty(t, suggest("zzz", 3))
	assert.Empty(t, suggest("100%", 4), "LIKE wildcards are neutralised")
	assert.Empty(t, suggest("_nacks", 6), "an unescaped _ would have matched snacks")
}

func byName(list []ledger.AccountBalance) map[string]ledger.AccountBalance {
	m := map[string]ledger.AccountBalance{}
	for _, a := range list {
		m[a.Name] = a
	}
	return m
}
