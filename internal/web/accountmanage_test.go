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
)

func flashOf(resp *http.Response) string {
	for _, c := range resp.Cookies() {
		if c.Name == "vinance_flash" {
			v, _ := url.QueryUnescape(c.Value)
			return v
		}
	}
	return ""
}

func acctNames(d *goquery.Document, heading string) []string {
	var out []string
	d.Find("h3").Each(func(_ int, h *goquery.Selection) {
		if h.Text() == heading {
			h.NextFiltered("ul.account-list").Find("li .tag-name").Each(func(_ int, a *goquery.Selection) { out = append(out, a.Text()) })
		}
	})
	return out
}

func TestAccountManageLists(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	s.spend("2026-09-01", "x", 100)
	svc := ledger.NewService(conn)
	require.NoError(t, svc.SetAccountArchived(context.Background(), accountID(t, conn, "neo"), true))

	resp, d := get(t, srv, "/accounts/manage")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "Manage accounts · vinance", d.Find("title").Text())
	assert.Equal(t, []string{"BRISK", "Wharf Bank"}, acctNames(d, "Accounts"))
	assert.Equal(t, []string{"Neo"}, acctNames(d, "Archived"))

	brisk := d.Find("ul.account-list li").First()
	assert.Equal(t, "1 line", brisk.Find(".tag-uses").Text())
	assert.Equal(t, "liability", brisk.Find(".account-meta .chip").Eq(0).Text())
	assert.Equal(t, "USD", brisk.Find(".account-meta .chip").Eq(1).Text())
	assert.Equal(t, "@brisk", brisk.Find(".account-meta .chip").Eq(2).Text())
	href, _ := brisk.Find("a.tag-name").Attr("href")
	assert.Equal(t, "/transactions?account=brisk", href)

	_, ro := brisk.Find(`input[name=currency]`).Attr("readonly")
	assert.True(t, ro, "a used account's currency is read-only")
	assert.Equal(t, 0, brisk.Find(`form[action$="/delete"]`).Length(), "a used account can't be deleted")
	assert.Equal(t, "Archive", brisk.Find(`form[action$="/archive"] button`).Text())

	wharf := d.Find("ul.account-list li").Eq(1)
	_, ro = wharf.Find(`input[name=currency]`).Attr("readonly")
	assert.False(t, ro)
	assert.Equal(t, 1, wharf.Find(`form[action$="/delete"]`).Length(), "an unused account can be deleted")
	assert.Equal(t, "Restore", d.Find("h3:contains('Archived') + ul li").Find(`form[action$="/archive"] button`).Text())

	today, _ := d.Find("input[name=opening_date]").Attr("value")
	assert.Equal(t, "2026-10-01", today, "the opening date defaults to today")
}

func TestAccountCreate(t *testing.T) {
	srv, conn := newApp(t)

	resp := postRaw(t, srv, "/accounts/manage/create", url.Values{
		"name": {"  Rainy Day Fund "}, "type": {"asset"}, "currency": {"usd"}, "opening": {"1,500.50"}, "opening_date": {"2026-01-31"},
	})
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, "/accounts/manage", resp.Header.Get("Location"))
	assert.Equal(t, "Added Rainy Day Fund (quick-add: @rainy-day-fund).", flashOf(resp))

	svc := ledger.NewService(conn)
	bs, err := svc.BalanceSheet(context.Background())
	require.NoError(t, err)
	var found ledger.AccountBalance
	for _, a := range bs.Assets {
		if a.Name == "Rainy Day Fund" {
			found = a
		}
	}
	assert.EqualValues(t, 150050, found.Balance, "the opening balance became a real transaction")
	assert.Equal(t, "USD", found.Currency)

	// No opening balance: just the account.
	resp = postRaw(t, srv, "/accounts/manage/create", url.Values{"name": {"Visa"}, "type": {"liability"}, "currency": {"USD"}, "opening": {""}, "opening_date": {"2026-01-31"}})
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, 1, count(t, conn, "transactions"), "no opening balance, no transaction")

	// A liability's opening balance is what is owed.
	resp = postRaw(t, srv, "/accounts/manage/create", url.Values{"name": {"Amex"}, "type": {"liability"}, "currency": {"USD"}, "opening": {"250"}, "opening_date": {"2026-02-01"}})
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	bs, _ = svc.BalanceSheet(context.Background())
	for _, l := range bs.Liabilities {
		if l.Name == "Amex" {
			assert.EqualValues(t, 25000, l.Display())
		}
	}
}

func TestAccountCreateErrorsKeepTheForm(t *testing.T) {
	srv, conn := newApp(t) // has BRISK

	tests := []struct {
		name string
		form url.Values
		want string
	}{
		{"duplicate", url.Values{"name": {"brisk"}, "type": {"liability"}, "currency": {"USD"}}, "already exists"},
		{"no name", url.Values{"name": {" "}, "type": {"asset"}, "currency": {"USD"}}, "name is required"},
		{"bad currency", url.Values{"name": {"X"}, "type": {"asset"}, "currency": {"dollars"}}, "3-letter"},
		{"bad opening amount", url.Values{"name": {"X"}, "type": {"asset"}, "currency": {"USD"}, "opening": {"lots"}, "opening_date": {"2026-01-01"}}, "opening balance isn't a valid amount"},
		{"too many decimals", url.Values{"name": {"X"}, "type": {"asset"}, "currency": {"JPY"}, "opening": {"100.5"}, "opening_date": {"2026-01-01"}}, "valid amount for JPY"},
		{"opening without date", url.Values{"name": {"X"}, "type": {"asset"}, "currency": {"USD"}, "opening": {"10"}, "opening_date": {""}}, "needs a date"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := count(t, conn, "accounts")
			resp, d := post(t, srv, "/accounts/manage/create", tc.form)
			require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
			assert.Contains(t, d.Find("p.problems").First().Text(), tc.want)
			v, _ := d.Find("details.add-account input[name=name]").Attr("value")
			assert.Equal(t, strings.TrimSpace(tc.form.Get("name")), v, "what was typed is kept (trimmed)")
			_, open := d.Find("details.add-account").Attr("open")
			assert.True(t, open, "the add form stays open on error")
			assert.Equal(t, before, count(t, conn, "accounts"))
		})
	}
}

func TestAccountUpdate(t *testing.T) {
	srv, conn := newApp(t)
	seed := newSeeder(t, conn) // before any renames: it looks accounts up by slug
	id := accountID(t, conn, "neo")

	resp := postRaw(t, srv, "/accounts/manage/"+strconv.FormatInt(id, 10)+"/update", url.Values{"name": {"Nubank"}, "type": {"asset"}, "currency": {"MXN"}})
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, "Saved Nubank (quick-add: @nubank).", flashOf(resp))
	assert.Equal(t, id, accountID(t, conn, "nubank"), "same account, new slug")

	// Errors show next to the account that caused them.
	resp2, d := post(t, srv, "/accounts/manage/"+strconv.FormatInt(id, 10)+"/update", url.Values{"name": {"BRISK"}, "type": {"asset"}, "currency": {"MXN"}})
	require.Equal(t, http.StatusUnprocessableEntity, resp2.StatusCode)
	li := d.Find("ul.account-list li").FilterFunction(func(_ int, s *goquery.Selection) bool { return s.Find(".tag-name").Text() == "Nubank" })
	assert.Contains(t, li.Find("p.problems").Text(), "already exists")
	_, open := li.Find("details").Attr("open")
	assert.True(t, open)
	assert.Equal(t, 0, d.Find("details.add-account ~ p.problems, p.crumbs ~ p.problems").Length(), "not shown as an add-form error")

	// A used account can be renamed but its currency is locked.
	brisk := accountID(t, conn, "brisk")
	seed.spend("2026-09-01", "x", 100)
	resp2, d = post(t, srv, "/accounts/manage/"+strconv.FormatInt(brisk, 10)+"/update", url.Values{"name": {"BRISK"}, "type": {"liability"}, "currency": {"MXN"}})
	assert.Equal(t, http.StatusUnprocessableEntity, resp2.StatusCode)
	assert.Contains(t, d.Find("p.problems").Text(), "currency can't change")

	resp2, _ = post(t, srv, "/accounts/manage/9999/update", url.Values{"name": {"x"}, "type": {"asset"}, "currency": {"USD"}})
	assert.Equal(t, http.StatusNotFound, resp2.StatusCode)
	resp2, _ = post(t, srv, "/accounts/manage/abc/update", url.Values{})
	assert.Equal(t, http.StatusNotFound, resp2.StatusCode)
}

func accountOptions(d *goquery.Document) []string {
	var out []string
	d.Find("#qa-form select[name=account] option").Each(func(_ int, o *goquery.Selection) { out = append(out, attr(o, "value")) })
	return out
}

func TestAccountArchiveAffectsQuickAdd(t *testing.T) {
	srv, conn := newApp(t)
	id := accountID(t, conn, "brisk")
	path := "/accounts/manage/" + strconv.FormatInt(id, 10)

	_, d := get(t, srv, "/")
	assert.Contains(t, accountOptions(d), "brisk")

	resp := postRaw(t, srv, path+"/archive", url.Values{"archived": {"1"}})
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, "Archived.", flashOf(resp))

	_, d = get(t, srv, "/")
	assert.NotContains(t, accountOptions(d), "brisk", "archived accounts can't be chosen")
	resp, d = post(t, srv, "/quickadd", url.Values{"kind": {"expense"}, "date": {"2026-10-01"}, "description": {"x"}, "amount": {"5"}, "account": {"brisk"}})
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	resp = postRaw(t, srv, path+"/archive", url.Values{"archived": {"0"}})
	assert.Equal(t, "Restored.", flashOf(resp))
	_, d = get(t, srv, "/")
	assert.Contains(t, accountOptions(d), "brisk")

	resp = postRaw(t, srv, "/accounts/manage/9999/archive", url.Values{"archived": {"1"}})
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestNewAccountIsImmediatelyUsableInQuickAdd(t *testing.T) {
	srv, conn := newApp(t)
	postRaw(t, srv, "/accounts/manage/create", url.Values{"name": {"Prime Card"}, "type": {"liability"}, "currency": {"USD"}})

	resp, _ := post(t, srv, "/quickadd", url.Values{"kind": {"expense"}, "date": {"2026-10-01"}, "description": {"Lunch"}, "amount": {"12.50"}, "tags": {"#food"}, "account": {"prime-card"}})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var name string
	require.NoError(t, conn.QueryRow(`SELECT a.name FROM splits s JOIN accounts a ON a.id = s.account_id WHERE a.builtin = 0`).Scan(&name))
	assert.Equal(t, "Prime Card", name)
}

func TestAccountDelete(t *testing.T) {
	srv, conn := newApp(t)
	newSeeder(t, conn).spend("2026-09-01", "x", 100) // BRISK is used
	used := "/accounts/manage/" + strconv.FormatInt(accountID(t, conn, "brisk"), 10)
	spare := "/accounts/manage/" + strconv.FormatInt(accountID(t, conn, "wharf-bank"), 10)

	resp, d := post(t, srv, used+"/delete", url.Values{})
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Contains(t, d.Find("p.problems").Text(), "Archive it instead")
	assert.Equal(t, 3, count(t, conn, "accounts")-4, "nothing deleted")

	resp = postRaw(t, srv, spare+"/delete", url.Values{})
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, "Deleted.", flashOf(resp))
	assert.Equal(t, 2, count(t, conn, "accounts")-4)

	resp = postRaw(t, srv, spare+"/delete", url.Values{})
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "already gone")
}

func TestAccountManageCrossOriginAndLinks(t *testing.T) {
	srv, conn := newApp(t)
	resp := postRaw(t, srv, "/accounts/manage/create", url.Values{"name": {"Evil"}, "type": {"asset"}, "currency": {"USD"}}, "Sec-Fetch-Site", "cross-site")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, 3, count(t, conn, "accounts")-4)

	_, d := get(t, srv, "/accounts")
	assert.Equal(t, 1, d.Find(`a[href="/accounts/manage"]`).Length())
	_, d = get(t, srv, "/accounts/manage")
	assert.Equal(t, 1, d.Find(`p.crumbs a[href="/accounts"]`).Length())
}
