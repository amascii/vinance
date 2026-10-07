package web_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/ledger"
	"github.com/amascii/vinance/internal/testutil"
)

// Server clock: 2026-10-01, the 1st of a 31-day month.

func budgetItems(d *goquery.Document) map[string]*goquery.Selection {
	m := map[string]*goquery.Selection{}
	d.Find("ul.budgets > li").Each(func(_ int, li *goquery.Selection) { m[li.Find(".bar-label").Text()] = li })
	return m
}

func hasState(li *goquery.Selection, state string) bool { return li.HasClass("state-" + state) }

func budgetOrder(d *goquery.Document) []string {
	var out []string
	d.Find("ul.budgets > li .bar-label").Each(func(_ int, s *goquery.Selection) { out = append(out, s.Text()) })
	return out
}

func mustBudget(t *testing.T, _ any, svc *ledger.Service, tag string, usd int64) {
	t.Helper()
	_, err := svc.SetBudget(context.Background(), tag, usd)
	require.NoError(t, err)
}

func TestBudgetsPageMeasuresTheMonth(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	svc := ledger.NewService(conn)
	s.spend("2026-09-30", "last month", 99900, "groceries")
	s.spend("2026-10-01", "HEB", 25000, "groceries")
	s.spend("2026-10-01", "Dairy Queen", 3000, "fast-food")
	s.spend("2026-10-01", "Fancy lunch", 9000, "restaurant")
	s.spend("2026-10-01", "Mystery", 500)
	mustBudget(t, conn, svc, "groceries", 30000)
	mustBudget(t, conn, svc, "fast-food", 2000)
	mustBudget(t, conn, svc, "restaurant", 20000)
	mustBudget(t, conn, svc, "travel", 50000)

	resp, d := get(t, srv, "/budgets")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "Budgets · vinance", d.Find("title").Text())
	assert.Equal(t, "October 2026", d.Find("nav.month-nav strong").Text())
	assert.Equal(t, "$375.00", d.Find(".networth .figure").Text(), "all spending this month, budgeted or not (September excluded)")
	assert.Equal(t, "$1,020.00 budgeted across 4 tags", rowTexts(d.Find(".networth .breakdown")))
	assert.Equal(t, []string{"fast-food", "groceries", "restaurant", "travel"}, budgetOrder(d), "most used first")

	items := budgetItems(d)
	ff := items["fast-food"]
	assert.True(t, hasState(ff, "over"))
	assert.Equal(t, "$30.00 of $20.00", rowTexts(ff.Find(".bar-value")))
	assert.Equal(t, "⚠ $10.00 over (150% used)", rowTexts(ff.Find(".meter-status")))
	assert.Equal(t, "width:100%;", func() string { v, _ := ff.Find(".meter-fill").Attr("style"); return v }(), "the bar is capped at the track")

	g := items["groceries"]
	assert.True(t, hasState(g, "ok"))
	assert.Equal(t, "✓ $50.00 left (83% used)", rowTexts(g.Find(".meter-status")))
	assert.Equal(t, "width:83%;", func() string { v, _ := g.Find(".meter-fill").Attr("style"); return v }())
	href, _ := g.Find("a.bar-label").Attr("href")
	assert.Equal(t, "/transactions?from=2026-10-01&tag=groceries&to=2026-10-31", href)

	tv := items["travel"]
	assert.Equal(t, "✓ $500.00 left (0% used)", rowTexts(tv.Find(".meter-status")))
	assert.Equal(t, "$0.00 of $500.00", rowTexts(tv.Find(".bar-value")))

	// The meter is exposed to assistive tech with its value.
	now, _ := g.Find(".meter").Attr("aria-valuenow")
	assert.Equal(t, "83", now)
	// Even-pace tick: day 1 of 31 is about 3% in.
	pace, _ := g.Find(".meter-pace").Attr("style")
	assert.Equal(t, "left:3%;", pace)
	assert.Contains(t, d.Find("p.pace-note").Text(), "day 1 of 31")
}

func TestBudgetSeverityBoundaries(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	svc := ledger.NewService(conn)
	// each tag has a $100.00 budget
	for tag, cents := range map[string]int64{"low": 8400, "edge": 8500, "full": 10000, "over": 10001} {
		s.spend("2026-10-01", tag, cents, tag)
		mustBudget(t, conn, svc, tag, 10000)
	}
	_, d := get(t, srv, "/budgets")
	items := budgetItems(d)
	assert.True(t, hasState(items["low"], "ok"), "84% is fine")
	assert.True(t, hasState(items["edge"], "warn"), "85% starts warning")
	assert.True(t, hasState(items["full"], "warn"), "exactly 100% is reached but not exceeded")
	assert.Equal(t, "! $0.00 left (100% used)", rowTexts(items["full"].Find(".meter-status")))
	assert.True(t, hasState(items["over"], "over"), "a single cent over is over")
	assert.Equal(t, "⚠ $0.01 over (100% used)", rowTexts(items["over"].Find(".meter-status")))
	// Severity never relies on colour alone: each state has its own icon and wording.
	for _, tag := range []string{"low", "edge", "full", "over"} {
		assert.NotEmpty(t, items[tag].Find(".state-icon").Text(), tag)
	}
}

func TestBudgetsMonthNavigation(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	s.spend("2026-09-10", "sept", 4000, "groceries")
	s.spend("2026-10-10", "oct", 1000, "groceries")
	mustBudget(t, conn, ledger.NewService(conn), "groceries", 10000)

	_, d := get(t, srv, "/budgets")
	prev, _ := d.Find(`nav.month-nav a[aria-label="Previous month"]`).Attr("href")
	next, _ := d.Find(`nav.month-nav a[aria-label="Next month"]`).Attr("href")
	assert.Equal(t, "/budgets?month=2026-09", prev)
	assert.Equal(t, "/budgets?month=2026-11", next)

	_, d = get(t, srv, prev)
	assert.Equal(t, "September 2026", d.Find("nav.month-nav strong").Text())
	assert.Equal(t, "$40.00 of $100.00", rowTexts(budgetItems(d)["groceries"].Find(".bar-value")))
	assert.Equal(t, 0, d.Find(".meter-pace").Length(), "no pace tick for a past month")
	assert.Equal(t, 0, d.Find("p.pace-note").Length())

	_, d = get(t, srv, "/budgets?month=2026-11")
	assert.Equal(t, "$0.00 of $100.00", rowTexts(budgetItems(d)["groceries"].Find(".bar-value")))
	assert.Equal(t, 0, d.Find(".meter-pace").Length(), "nor for a future one")

	_, d = get(t, srv, "/budgets?month=2027-01")
	prev, _ = d.Find(`nav.month-nav a[aria-label="Previous month"]`).Attr("href")
	assert.Equal(t, "/budgets?month=2026-12", prev, "year boundary")
	_, d = get(t, srv, "/budgets?month=2026-01")
	prev, _ = d.Find(`nav.month-nav a[aria-label="Previous month"]`).Attr("href")
	assert.Equal(t, "/budgets?month=2025-12", prev)

	_, d = get(t, srv, "/budgets?month=garbage")
	assert.Equal(t, "October 2026", d.Find("nav.month-nav strong").Text(), "a bad month falls back to the current one")
}

func TestBudgetSetAndUpdate(t *testing.T) {
	srv, conn := newApp(t)
	svc := ledger.NewService(conn)

	resp := postRaw(t, srv, "/budgets/set", url.Values{"month": {"2026-09"}, "tag": {"#Groceries "}, "amount": {"1,250.50"}})
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, "/budgets?month=2026-09", resp.Header.Get("Location"), "stays on the month being viewed")
	assert.Equal(t, "Budget for #groceries is $1,250.50 a month.", flashOf(resp))
	list, err := svc.ListBudgets(context.Background())
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "groceries", list[0].Tag)
	assert.EqualValues(t, 125050, list[0].MonthlyUSD)

	// Setting the same tag again changes it.
	postRaw(t, srv, "/budgets/set", url.Values{"month": {"2026-10"}, "tag": {"groceries"}, "amount": {"400"}})
	list, _ = svc.ListBudgets(context.Background())
	require.Len(t, list, 1)
	assert.EqualValues(t, 40000, list[0].MonthlyUSD)

	_, d := get(t, srv, "/budgets")
	assert.Equal(t, "400.00", func() string { v, _ := d.Find(`ul.budgets form input[name=amount]`).Attr("value"); return v }(), "the edit field holds the current amount")
	var tags []string
	d.Find("datalist#budget-tags option").Each(func(_ int, o *goquery.Selection) { v, _ := o.Attr("value"); tags = append(tags, v) })
	assert.Equal(t, []string{"groceries"}, tags)
}

func TestBudgetSetErrors(t *testing.T) {
	srv, conn := newApp(t)
	tests := []struct {
		name string
		form url.Values
		want string
	}{
		{"blank amount", url.Values{"tag": {"food"}, "amount": {""}}, "monthly amount in dollars"},
		{"junk amount", url.Values{"tag": {"food"}, "amount": {"lots"}}, "monthly amount in dollars"},
		{"zero", url.Values{"tag": {"food"}, "amount": {"0"}}, "monthly amount in dollars"},
		{"negative", url.Values{"tag": {"food"}, "amount": {"-5"}}, "monthly amount in dollars"},
		{"too many decimals", url.Values{"tag": {"food"}, "amount": {"1.234"}}, "monthly amount in dollars"},
		{"no tag", url.Values{"tag": {" # "}, "amount": {"10"}}, "choose a tag"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, d := post(t, srv, "/budgets/set", tc.form)
			require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
			assert.Contains(t, d.Find("p.problems").First().Text(), tc.want)
			v, _ := d.Find(`form[action="/budgets/set"] input[name=tag]`).Last().Attr("value")
			assert.Equal(t, strings.TrimSpace(tc.form.Get("tag")), v, "the add form keeps what was typed")
		})
	}
	list, _ := ledger.NewService(conn).ListBudgets(context.Background())
	assert.Empty(t, list)

	// An error on an existing row's edit form is shown on that row.
	mustBudget(t, conn, ledger.NewService(conn), "food", 1000)
	resp, d := post(t, srv, "/budgets/set", url.Values{"tag": {"food"}, "amount": {"oops"}, "edit": {"1"}})
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	row := budgetItems(d)["food"]
	assert.Contains(t, row.Find("details p.problems").Text(), "monthly amount in dollars")
	_, open := row.Find("details").Attr("open")
	assert.True(t, open)
}

func TestBudgetDelete(t *testing.T) {
	srv, conn := newApp(t)
	svc := ledger.NewService(conn)
	mustBudget(t, conn, svc, "food", 1000)

	resp := postRaw(t, srv, "/budgets/delete", url.Values{"tag": {"food"}, "month": {"2026-09"}})
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, "/budgets?month=2026-09", resp.Header.Get("Location"))
	assert.Equal(t, "Removed the budget for #food.", flashOf(resp))
	list, _ := svc.ListBudgets(context.Background())
	assert.Empty(t, list)

	resp = postRaw(t, srv, "/budgets/delete", url.Values{"tag": {"food"}}) // already gone: harmless
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, "", flashOf(resp))
}

func TestBudgetsEmptyStateNavAndCrossOrigin(t *testing.T) {
	srv, conn := newApp(t)
	_, d := get(t, srv, "/budgets")
	assert.Equal(t, 1, d.Find("p.empty").Length())
	assert.Equal(t, 0, d.Find("ul.budgets").Length())
	assert.Equal(t, 0, d.Find(".networth").Length(), "no totals until there is a budget")
	_, d = get(t, srv, "/")
	assert.Equal(t, 1, d.Find(`header nav a[href="/budgets"]`).Length())

	resp := postRaw(t, srv, "/budgets/set", url.Values{"tag": {"x"}, "amount": {"5"}}, "Sec-Fetch-Site", "cross-site")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	list, _ := ledger.NewService(conn).ListBudgets(context.Background())
	assert.Empty(t, list)
}

func TestBudgetsMissingRatesNotice(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	yen := testutil.SeedAccount(t, conn, "Yen Wallet", "asset", "JPY")
	_, err := s.svc.Create(context.Background(), ledger.TxnInput{
		Date: "2026-10-01", Description: "ramen", Currency: "JPY",
		Splits: []ledger.SplitInput{
			{AccountID: yen, Currency: "JPY", Amount: -1000, Value: -1000},
			{AccountID: s.acct["expense"], Currency: "JPY", Amount: 1000, Value: 1000, Tags: []string{"food"}},
		},
	})
	require.NoError(t, err)
	mustBudget(t, conn, ledger.NewService(conn), "food", 10000)

	_, d := get(t, srv, "/budgets")
	assert.Contains(t, d.Find(".notice.warn").Text(), "No exchange rate on file for JPY")
}
