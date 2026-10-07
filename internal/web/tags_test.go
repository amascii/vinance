package web_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/ledger"
	"github.com/amascii/vinance/internal/testutil"
)

func bars(d *goquery.Document) map[string]*goquery.Selection {
	m := map[string]*goquery.Selection{}
	d.Find("ul.bars li.bar-row").Each(func(_ int, li *goquery.Selection) {
		m[li.Find(".bar-label").Text()] = li
	})
	return m
}

func barOrder(d *goquery.Document) []string {
	var out []string
	d.Find("ul.bars li.bar-row .bar-label").Each(func(_ int, s *goquery.Selection) { out = append(out, s.Text()) })
	return out
}

func width(li *goquery.Selection) string {
	st, _ := li.Find(".bar").Attr("style")
	return strings.TrimSpace(st)
}

// Server clock is fixed at 2026-10-01 (testutil.Today).
func TestTagsPageDefaultsToThisMonth(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	s.spend("2026-09-30", "last month", 9900, "old")
	s.spend("2026-10-01", "Dairy Queen", 1250, "fast-food", "outside-food")
	s.spend("2026-10-02", "HEB", 4200, "groceries")
	s.spend("2026-10-03", "Mystery", 500)

	resp, d := get(t, srv, "/tags")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "Tags · vinance", d.Find("title").Text())
	assert.Equal(t, "$59.50", d.Find(".networth .figure").Text())
	assert.Contains(t, d.Find(".networth .label").Text(), "Total spending")
	assert.Contains(t, d.Find(".networth .label").Text(), "Oct 1, 2026 – Oct 31, 2026")
	assert.Equal(t, "3 items", d.Find(".networth .breakdown").Text())

	assert.Equal(t, []string{"groceries", "fast-food", "outside-food", "Untagged"}, barOrder(d), "largest first, Untagged last")
	b := bars(d)
	assert.Equal(t, "$42.00", b["groceries"].Find(".bar-value").Text())
	assert.Equal(t, "100%", strings.TrimPrefix(strings.TrimSuffix(width(b["groceries"]), ";"), "width:"), "the largest bar is full width")
	assert.Equal(t, "width:29%", strings.TrimSuffix(width(b["fast-food"]), ";"), "12.50 / 42.00")
	assert.Equal(t, "$12.50", b["outside-food"].Find(".bar-value").Text(), "a two-tag line counts toward each tag")
	assert.Equal(t, "$5.00", b["Untagged"].Find(".bar-value").Text())
	assert.Equal(t, "1 item", b["groceries"].Find(".bar-detail").Text())
	assert.Equal(t, "groceries: $42.00 across 1 line", func() string { v, _ := b["groceries"].Attr("title"); return v }())
	assert.NotContains(t, strings.Join(barOrder(d), ","), "old", "last month's spending is outside the default range")
}

func TestTagsPageRangePresets(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	s.spend("2025-12-31", "last year", 100, "y")
	s.spend("2026-08-31", "aug", 200, "a")
	s.spend("2026-09-05", "sep", 400, "s")
	s.spend("2026-10-01", "oct", 800, "o")

	total := func(q string) string { _, d := get(t, srv, "/tags"+q); return d.Find(".networth .figure").Text() }
	assert.Equal(t, "$8.00", total("?range=month"))
	assert.Equal(t, "$4.00", total("?range=last-month"))
	assert.Equal(t, "$12.00", total("?range=30d"), "Sep 3 – Oct 1")
	assert.Equal(t, "$14.00", total("?range=ytd"))
	assert.Equal(t, "$15.00", total("?range=all"))
	assert.Equal(t, "$8.00", total("?range=nonsense"), "an unknown preset falls back to this month")
	assert.Equal(t, "$6.00", total("?from=2026-08-01&to=2026-09-30"), "custom range, inclusive")
	assert.Equal(t, "$12.00", total("?from=2026-09-01"), "open end")

	_, d := get(t, srv, "/tags?range=ytd")
	var active []string
	d.Find("nav[aria-label='Date range'] a.active").Each(func(_ int, a *goquery.Selection) { active = append(active, a.Text()) })
	assert.Equal(t, []string{"Year to date"}, active)

	_, d = get(t, srv, "/tags?from=2026-08-01&to=2026-09-30")
	assert.Equal(t, 0, d.Find("nav[aria-label='Date range'] a.active").Length(), "no preset is highlighted for a custom range")
	v, _ := d.Find("input[name=from]").Attr("value")
	assert.Equal(t, "2026-08-01", v, "the custom range is echoed into the form")

	_, d = get(t, srv, "/tags?from=garbage")
	assert.Contains(t, d.Find("p.problems").Text(), "Dates must look like")
	assert.Equal(t, "$8.00", d.Find(".networth .figure").Text(), "a bad date falls back to the default range")
}

func TestTagsPageDrillDownLinks(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	s.spend("2026-10-01", "a", 100, "snacks")
	s.spend("2026-10-02", "b", 100)

	_, d := get(t, srv, "/tags")
	href, _ := bars(d)["snacks"].Find("a.bar-label").Attr("href")
	assert.Equal(t, "/transactions?from=2026-10-01&tag=snacks&to=2026-10-31", href)
	href, _ = bars(d)["Untagged"].Find("a.bar-label").Attr("href")
	assert.Equal(t, "/transactions?from=2026-10-01&to=2026-10-31&untagged=1", href)

	// The links land on a list showing exactly the rows behind each number.
	_, list := get(t, srv, "/transactions?from=2026-10-01&tag=snacks&to=2026-10-31")
	assert.Equal(t, []string{"a"}, listDescs(list))
	_, list = get(t, srv, "/transactions?from=2026-10-01&to=2026-10-31&untagged=1")
	assert.Equal(t, []string{"b"}, listDescs(list))
	_, v := get(t, srv, "/transactions?untagged=1")
	_, checked := v.Find("input[name=untagged]").Attr("checked")
	assert.True(t, checked, "the checkbox reflects the filter")
}

func TestTagsPageIncomeAndKindLinks(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	s.spend("2026-10-01", "coffee", 450, "cafes")
	_, err := s.svc.Create(context.Background(), ledger.TxnInput{
		Date: "2026-10-01", Description: "Paycheck", Currency: "USD",
		Splits: []ledger.SplitInput{
			{AccountID: s.acct["wharf-bank"], Currency: "USD", Amount: 250000, Value: 250000},
			{AccountID: s.acct["income"], Currency: "USD", Amount: -250000, Value: -250000, Tags: []string{"salary"}},
		},
	})
	require.NoError(t, err)

	_, d := get(t, srv, "/tags?kind=income")
	assert.Equal(t, "$2,500.00", d.Find(".networth .figure").Text())
	assert.Contains(t, d.Find(".networth .label").Text(), "Total income")
	assert.Equal(t, []string{"salary"}, barOrder(d))

	var kinds []string
	d.Find("nav[aria-label='Report type'] a").Each(func(_ int, a *goquery.Selection) {
		h, _ := a.Attr("href")
		kinds = append(kinds, a.Text()+" "+h+" "+map[bool]string{true: "active", false: ""}[a.HasClass("active")])
	})
	assert.Equal(t, []string{"Spending /tags?kind=expense&range=month ", "Income /tags?kind=income&range=month active"}, kinds)
}

func TestTagsPageCurrenciesAndMissingRates(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	yen := testutil.SeedAccount(t, conn, "Yen Wallet", "asset", "JPY")
	_, err := conn.Exec("INSERT INTO prices VALUES ('MXN','2026-09-01','0.0500')")
	require.NoError(t, err)
	spend := func(acct int64, cur string, amt int64, tag string) {
		_, err := s.svc.Create(context.Background(), ledger.TxnInput{
			Date: "2026-10-01", Description: "x", Currency: cur,
			Splits: []ledger.SplitInput{
				{AccountID: acct, Currency: cur, Amount: -amt, Value: -amt},
				{AccountID: s.acct["expense"], Currency: cur, Amount: amt, Value: amt, Tags: []string{tag}},
			},
		})
		require.NoError(t, err)
	}
	spend(s.acct["brisk"], "USD", 1000, "food")
	spend(s.acct["neo"], "MXN", 20000, "food") // MX$200 at 0.05 = $10
	spend(yen, "JPY", 5000, "food")            // no JPY rate

	_, d := get(t, srv, "/tags")
	food := bars(d)["food"]
	assert.Equal(t, "$20.00", food.Find(".bar-value").Text(), "$10 + MX$200 as $10; yen excluded")
	assert.Equal(t, "3 items · incl. ¥5,000, MX$200.00", food.Find(".bar-detail").Text())
	assert.Contains(t, d.Find(".notice.warn").Text(), "No exchange rate on file for JPY")
}

func TestTagsPageEmpty(t *testing.T) {
	srv, _ := newApp(t)
	_, d := get(t, srv, "/tags")
	assert.Equal(t, "$0.00", d.Find(".networth .figure").Text())
	assert.Equal(t, 1, d.Find("p.empty").Length())
	assert.Equal(t, 0, d.Find("ul.bars").Length())
}

func TestTagsNavLink(t *testing.T) {
	srv, _ := newApp(t)
	_, d := get(t, srv, "/")
	assert.Equal(t, 1, d.Find(`header nav a[href="/tags"]`).Length())
}
