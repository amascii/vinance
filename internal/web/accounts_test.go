package web_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/db/gen"
	"github.com/amascii/vinance/internal/ledger"
	"github.com/amascii/vinance/internal/testutil"
)

func fundAccount(t *testing.T, s *seeder, acct int64, cur string, amt int64) {
	t.Helper()
	eq, err := gen.New(s.conn).GetBuiltinAccount(context.Background(), "equity")
	require.NoError(t, err)
	_, err = s.svc.Create(context.Background(), ledger.TxnInput{
		Date: "2026-01-01", Description: "Opening", Currency: cur,
		Splits: []ledger.SplitInput{
			{AccountID: acct, Currency: cur, Amount: amt, Value: amt},
			{AccountID: eq.ID, Currency: cur, Amount: -amt, Value: -amt},
		},
	})
	require.NoError(t, err)
}

func balanceRow(d *goquery.Document, group, name string) *goquery.Selection {
	var found *goquery.Selection
	d.Find("h3").Each(func(_ int, h *goquery.Selection) {
		if h.Text() != group {
			return
		}
		h.NextAllFiltered("ul.balances").First().Find("li").Each(func(_ int, li *goquery.Selection) {
			if rowTexts(li.Find("a.name")) == name {
				found = li
			}
		})
	})
	return found
}

func TestAccountsPage(t *testing.T) {
	srv, conn := newApp(t) // BRISK (USD card), Wharf Bank (USD), Neo (MXN)
	s := newSeeder(t, conn)
	fundAccount(t, s, s.acct["wharf-bank"], "USD", 123456)
	fundAccount(t, s, s.acct["neo"], "MXN", 500000)
	s.spend("2026-09-02", "Walmart", 6500, "groceries") // BRISK now owes $65.00
	testutil.SeedAccount(t, conn, "Yen Wallet", "asset", "JPY")
	fundAccount(t, s, accountID(t, conn, "yen-wallet"), "JPY", 50000)
	_, err := conn.Exec("INSERT INTO prices VALUES ('MXN','2026-09-01','0.0550')")
	require.NoError(t, err)

	resp, d := get(t, srv, "/accounts")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "Accounts · vinance", d.Find("title").Text())

	wharf := balanceRow(d, "Assets", "Wharf Bank")
	require.NotNil(t, wharf)
	assert.Equal(t, "$1,234.56", wharf.Find(".balance").Text())
	assert.Equal(t, 0, wharf.Find(".usd").Length(), "USD accounts need no conversion note")
	href, _ := wharf.Find("a.name").Attr("href")
	assert.Equal(t, "/transactions?account=wharf-bank", href)

	neo := balanceRow(d, "Assets", "Neo")
	assert.Equal(t, "MX$5,000.00", neo.Find(".balance").Text())
	assert.Equal(t, "≈ $275.00", neo.Find(".usd").Text())

	yen := balanceRow(d, "Assets", "Yen Wallet")
	assert.Equal(t, "¥50,000", yen.Find(".balance").Text())
	assert.Equal(t, "no rate", yen.Find(".usd.warn").Text())

	brisk := balanceRow(d, "Liabilities", "BRISK")
	assert.Equal(t, "$65.00", brisk.Find(".balance").Text(), "liabilities show what is owed")

	// 1,234.56 + 275.00 (yen has no rate) - 65.00
	assert.Equal(t, "$1,444.56", d.Find(".networth .figure").Text())
	assert.Equal(t, "$1,509.56 in assets − $65.00 owed", rowTexts(d.Find(".networth .breakdown")))
	assert.Contains(t, d.Find(".notice.warn").Text(), "No exchange rate on file for JPY")
	assert.Contains(t, d.Find("p.hint").Text(), "MXN as of 2026-09-01")
}

func TestAccountsPageNegativeNetWorthAndZeroBalances(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	s.spend("2026-09-02", "Big purchase", 50000, "x") // owes $500 with no assets funded

	_, d := get(t, srv, "/accounts")
	assert.Equal(t, "-$500.00", d.Find(".networth .figure").Text())
	assert.True(t, d.Find(".networth .figure").HasClass("neg"))
	assert.Equal(t, 0, d.Find(".notice.warn").Length())
	assert.Equal(t, 0, d.Find("p.hint").Length(), "no rate note when no converted balances")
	assert.True(t, balanceRow(d, "Assets", "Wharf Bank").HasClass("zero"))
	assert.Equal(t, "$0.00", balanceRow(d, "Assets", "Wharf Bank").Find(".balance").Text())
}

func TestAccountsPageEmptyDatabase(t *testing.T) {
	srv, _ := testutil.NewServerWithDB(t)
	resp, d := get(t, srv, "/accounts")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "$0.00", d.Find(".networth .figure").Text())
	assert.Equal(t, 2, d.Find("p.empty").Length())
}

func TestNavLinksToAccounts(t *testing.T) {
	srv, _ := newApp(t)
	_, d := get(t, srv, "/")
	assert.Equal(t, 1, d.Find(`header nav a[href="/accounts"]`).Length())
}

func TestAccountRowsHaveAnUnsavedTick(t *testing.T) {
	srv, _ := newApp(t)
	_, d := get(t, srv, "/accounts")
	ticks := d.Find("ul.balances li input.tick[type=checkbox]")
	assert.Equal(t, d.Find("ul.balances li").Length(), ticks.Length(), "every account row has one")
	ticks.Each(func(_ int, in *goquery.Selection) {
		_, checked := in.Attr("checked")
		assert.False(t, checked, "all start unticked")
		assert.Equal(t, "off", attr(in, "autocomplete"), "so the browser doesn't restore ticks on reload")
		assert.NotEmpty(t, attr(in, "aria-label"))
		assert.Empty(t, attr(in, "name"), "not a form field: nothing is ever sent or stored")
	})
	assert.Equal(t, 0, d.Find("form").Length(), "and there is no form to submit")
}
