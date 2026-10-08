package web_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/db/gen"
	"github.com/amascii/vinance/internal/ledger"
)

// seeder adds transactions through the real ledger service.
type seeder struct {
	t    *testing.T
	conn *sql.DB
	svc  *ledger.Service
	acct map[string]int64
}

func newSeeder(t *testing.T, conn *sql.DB) *seeder {
	s := &seeder{t: t, conn: conn, svc: ledger.NewService(conn), acct: map[string]int64{}}
	for _, slug := range []string{"brisk", "wharf-bank", "neo"} {
		s.acct[slug] = accountID(t, conn, slug)
	}
	for _, typ := range []string{"expense", "imbalance", "income"} {
		a, err := gen.New(conn).GetBuiltinAccount(context.Background(), typ)
		require.NoError(t, err)
		s.acct[typ] = a.ID
	}
	return s
}

// spend records an expense on BRISK.
func (s *seeder) spend(date, desc string, cents int64, tags ...string) {
	s.t.Helper()
	_, err := s.svc.Create(context.Background(), ledger.TxnInput{
		Date: date, Description: desc, Currency: "USD",
		Splits: []ledger.SplitInput{
			{AccountID: s.acct["brisk"], Currency: "USD", Amount: -cents, Value: -cents},
			{AccountID: s.acct["expense"], Currency: "USD", Amount: cents, Value: cents, Tags: tags},
		},
	})
	require.NoError(s.t, err)
}

func listDescs(d *goquery.Document) []string {
	var out []string
	d.Find("#txn-list li.txn .line1 .desc").Each(func(_ int, s *goquery.Selection) { out = append(out, s.Text()) })
	return out
}

func TestTransactionsPageBasics(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	s.spend("2026-09-01", "Dairy Queen", 1250, "fast-food")
	s.spend("2026-09-03", "HEB", 4200, "groceries")
	s.spend("2026-09-02", "Sunoco", 3000, "fuel")

	resp, d := get(t, srv, "/transactions")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "Transactions · vinance", d.Find("title").Text())
	assert.Equal(t, []string{"HEB", "Sunoco", "Dairy Queen"}, listDescs(d), "newest first")
	assert.Equal(t, "3 transactions", d.Find("p.count").Text())
	assert.Equal(t, 0, d.Find("li.more").Length(), "no next page")
	assert.Equal(t, 0, d.Find("p.empty").Length())

	var accounts []string
	d.Find("select[name=account] option").Each(func(_ int, s *goquery.Selection) { accounts = append(accounts, s.Text()) })
	assert.Equal(t, []string{"All accounts", "BRISK", "Neo", "Wharf Bank"}, accounts, "real accounts only, no built-ins")
	var tags []string
	d.Find("datalist#tag-options option").Each(func(_ int, s *goquery.Selection) { v, _ := s.Attr("value"); tags = append(tags, v) })
	assert.ElementsMatch(t, []string{"fast-food", "groceries", "fuel"}, tags)
	assert.Equal(t, 1, d.Find(`header nav a[href="/transactions"]`).Length(), "nav links to the page")
}

func TestTransactionsFilters(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	s.spend("2026-09-01", "Dairy Queen", 1250, "fast-food", "outside-food")
	s.spend("2026-09-02", "McDonald's", 800, "fast-food", "outside-food")
	s.spend("2026-09-03", "HEB", 4200, "groceries")
	_, err := s.svc.Create(context.Background(), ledger.TxnInput{
		Date: "2026-09-04", Description: "Paycheck", Currency: "USD",
		Splits: []ledger.SplitInput{
			{AccountID: s.acct["wharf-bank"], Currency: "USD", Amount: 100000, Value: 100000},
			{AccountID: s.acct["income"], Currency: "USD", Amount: -100000, Value: -100000, Tags: []string{"salary"}},
		},
	})
	require.NoError(t, err)

	tests := []struct {
		name, query string
		want        []string
	}{
		{"text", "q=queen", []string{"Dairy Queen"}},
		{"tag with hash", "tag=%23groceries", []string{"HEB"}},
		{"two tags space separated", "tag=fast-food+outside-food", []string{"McDonald's", "Dairy Queen"}},
		{"tags are ANDed (these never co-occur)", "tag=%23fast-food%2C%23groceries", nil},
		{"tag is normalised", "tag=Fast+Food", nil}, // 'fast' and 'food' are two tags
		{"account", "account=wharf-bank", []string{"Paycheck"}},
		{"date range", "from=2026-09-02&to=2026-09-03", []string{"HEB", "McDonald's"}},
		{"combined", "tag=fast-food&from=2026-09-02", []string{"McDonald's"}},
		{"empty params are ignored", "q=&account=&tag=&from=&to=", []string{"Paycheck", "HEB", "McDonald's", "Dairy Queen"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, d := get(t, srv, "/transactions?"+tc.query)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, tc.want, nilIfEmpty(listDescs(d)))
			assert.Equal(t, fmt.Sprintf("%d %s", len(tc.want), map[bool]string{true: "transaction", false: "transactions"}[len(tc.want) == 1]), d.Find("p.count").Text())
			if len(tc.want) == 0 {
				assert.Equal(t, 1, d.Find("p.empty").Length())
			}
		})
	}
}

func nilIfEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

func TestTransactionsFormEchoesFilters(t *testing.T) {
	srv, conn := newApp(t)
	newSeeder(t, conn).spend("2026-09-01", "x", 100, "snack")

	_, d := get(t, srv, "/transactions?q=milk&account=neo&tag=%23snack&from=2026-01-01&to=2026-12-31&imbalance=1")
	val := func(sel string) string { v, _ := d.Find(sel).Attr("value"); return v }
	assert.Equal(t, "milk", val("#filters input[name=q]"))
	assert.Equal(t, "#snack", val("input[name=tag]"))
	assert.Equal(t, "2026-01-01", val("input[name=from]"))
	assert.Equal(t, "2026-12-31", val("input[name=to]"))
	assert.Equal(t, "neo", val("select[name=account] option[selected]"))
	_, checked := d.Find("input[name=imbalance]").Attr("checked")
	assert.True(t, checked)
}

func TestTransactionsNotices(t *testing.T) {
	srv, conn := newApp(t)
	newSeeder(t, conn).spend("2026-09-01", "x", 100)

	_, d := get(t, srv, "/transactions?from=yesterday")
	assert.Contains(t, d.Find("p.problems").Text(), "From date must look like")
	assert.Equal(t, 1, d.Find("li.txn").Length(), "a bad date is ignored, not fatal")

	_, d = get(t, srv, "/transactions?account=nope")
	assert.Contains(t, d.Find("p.problems").Text(), "Unknown account: nope")
	assert.Equal(t, 0, d.Find("li.txn").Length())

	_, d = get(t, srv, "/transactions?from=2026-10-01&to=2026-09-01")
	assert.Contains(t, d.Find("p.problems").Text(), "From date is after the To date")
}

func TestTransactionsImbalanceFilterAndChip(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	s.spend("2026-09-01", "fine", 100)
	_, err := s.svc.Create(context.Background(), ledger.TxnInput{
		Date: "2026-09-02", Description: "Clothing store", Currency: "USD",
		Splits: []ledger.SplitInput{
			{AccountID: s.acct["brisk"], Currency: "USD", Amount: -12000, Value: -12000},
			{AccountID: s.acct["imbalance"], Currency: "USD", Amount: 12000, Value: 12000},
		},
	})
	require.NoError(t, err)

	_, d := get(t, srv, "/transactions?imbalance=1")
	assert.Equal(t, []string{"Clothing store"}, listDescs(d))
	assert.Equal(t, "needs fixing", d.Find("li.txn .chip.warn").First().Text())
}

func TestTransactionsShowSplitDetail(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	s.spend("2026-09-01", "simple", 100, "snack")
	_, err := s.svc.Create(context.Background(), ledger.TxnInput{
		Date: "2026-09-02", Description: "Walmart", Currency: "USD",
		Splits: []ledger.SplitInput{
			{AccountID: s.acct["brisk"], Currency: "USD", Amount: -6500, Value: -6500},
			{AccountID: s.acct["expense"], Memo: "Sodas", Currency: "USD", Amount: 1000, Value: 1000, Tags: []string{"drinks"}},
			{AccountID: s.acct["expense"], Memo: "Chips", Currency: "USD", Amount: 2000, Value: 2000, Tags: []string{"snacks"}},
			{AccountID: s.acct["expense"], Currency: "USD", Amount: 3500, Value: 3500},
		},
	})
	require.NoError(t, err)

	_, d := get(t, srv, "/transactions")
	rows := d.Find("#txn-list > li.txn")
	require.Equal(t, 2, rows.Length())

	walmart, simple := rows.Eq(0), rows.Eq(1)
	assert.Equal(t, 1, walmart.Find("details").Length())
	lines := walmart.Find("ul.splits li.split")
	require.Equal(t, 4, lines.Length())
	assert.Equal(t, "BRISK", lines.Eq(0).Find(".chip.account").Text())
	assert.Equal(t, "-$65.00", lines.Eq(0).Find(".amount").Text())
	assert.Equal(t, "Sodas", lines.Eq(1).Find(".memo").Text())
	assert.Equal(t, "#drinks", lines.Eq(1).Find(".chip.tag").Text())
	assert.Equal(t, "$10.00", lines.Eq(1).Find(".amount").Text())
	assert.Equal(t, "Expenses", lines.Eq(3).Find(".chip.account").Text(), "a bare built-in split falls back to its account name")
	assert.Equal(t, "4 lines", strings.TrimSpace(walmart.Find("summary .line2 .chip:not(.account):not(.tag)").Text()))
	assert.Equal(t, "-$65.00", walmart.Find("summary .line1 .amount").Text(), "headline is the net, not the sum of lines")

	assert.Equal(t, 0, simple.Find("details").Length(), "plain two-line transactions stay compact")
}

func TestTransactionsInfiniteScroll(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	for i := 0; i < 120; i++ {
		tag := "even"
		if i%2 == 1 {
			tag = "odd"
		}
		s.spend(fmt.Sprintf("2026-08-%02d", 1+i/5), fmt.Sprintf("txn-%03d", i), int64(100+i), tag)
	}

	_, d := get(t, srv, "/transactions")
	assert.Equal(t, "120 transactions", d.Find("p.count").Text())
	assert.Equal(t, 50, d.Find("#txn-list li.txn").Length())
	more := d.Find("#txn-list li.more")
	require.Equal(t, 1, more.Length())
	trigger, _ := more.Attr("hx-trigger")
	assert.Equal(t, "revealed", trigger)
	next, _ := more.Attr("hx-get")
	assert.Contains(t, next, "before=")

	seen := listDescs(d)
	pages := 1
	for next != "" {
		resp, frag := getHTMX(t, srv, next)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, 0, frag.Find("title").Length(), "just rows, not a page")
		assert.Equal(t, 0, frag.Find("p.count").Length(), "no count on later pages")
		frag.Find("li.txn .line1 .desc").Each(func(_ int, s *goquery.Selection) { seen = append(seen, s.Text()) })
		next, _ = frag.Find("li.more").Attr("hx-get")
		pages++
		require.Less(t, pages, 10)
	}
	assert.Equal(t, 3, pages)
	assert.Len(t, seen, 120)
	uniq := map[string]bool{}
	for _, s := range seen {
		uniq[s] = true
	}
	assert.Len(t, uniq, 120, "no duplicates or gaps across pages")
}

func TestTransactionsInfiniteScrollKeepsFilters(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	for i := 0; i < 130; i++ {
		tag := "even"
		if i%2 == 1 {
			tag = "odd"
		}
		s.spend(fmt.Sprintf("2026-08-%02d", 1+i/5), fmt.Sprintf("txn-%03d", i), 100, tag)
	}

	_, d := get(t, srv, "/transactions?tag=even")
	assert.Equal(t, "65 transactions", d.Find("p.count").Text())
	next, ok := d.Find("li.more").Attr("hx-get")
	require.True(t, ok)
	u, err := url.Parse(next)
	require.NoError(t, err)
	assert.Equal(t, "even", u.Query().Get("tag"), "the next-page URL carries the active filters")

	_, frag := getHTMX(t, srv, next)
	rows := frag.Find("li.txn")
	assert.Equal(t, 15, rows.Length())
	rows.Each(func(_ int, r *goquery.Selection) {
		assert.Equal(t, "#even", r.Find(".chip.tag").Text())
	})
	assert.Equal(t, 0, frag.Find("li.more").Length())
}

func TestTransactionsHTMXShapes(t *testing.T) {
	srv, conn := newApp(t)
	newSeeder(t, conn).spend("2026-09-01", "Dairy Queen", 1250, "fast-food")

	// A filter change gets the results fragment (count + list), not a whole page.
	resp, frag := getHTMX(t, srv, "/transactions?q=dairy")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 0, frag.Find("title").Length())
	assert.Equal(t, "1 transaction", frag.Find("p.count").Text())
	assert.Equal(t, 1, frag.Find("ul#txn-list li.txn").Length())
	assert.Equal(t, 0, frag.Find("form#filters").Length(), "the form is not re-rendered, so typing isn't interrupted")
	assert.Contains(t, resp.Header.Values("Vary"), "HX-Request")
	assert.Equal(t, "/transactions?q=dairy", resp.Header.Get("HX-Push-Url"), "clean URL, no empty params")

	resp, _ = getHTMX(t, srv, "/transactions?q=&account=&tag=%23Fast-Food&from=&to=&imbalance=1")
	assert.Equal(t, "/transactions?imbalance=1&tag=%23Fast-Food", resp.Header.Get("HX-Push-Url"))
	resp, _ = getHTMX(t, srv, "/transactions?q=&account=")
	assert.Equal(t, "/transactions", resp.Header.Get("HX-Push-Url"))

	// Restoring history (back button) must get a complete page.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/transactions?q=dairy", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-History-Restore-Request", "true")
	r, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	d := doc(t, r)
	assert.Equal(t, "Transactions · vinance", d.Find("title").Text())
	assert.Equal(t, 1, d.Find("form#filters").Length())

	// A plain request is a complete page too.
	_, d = get(t, srv, "/transactions?q=dairy")
	assert.Equal(t, 1, d.Find("form#filters").Length())
}

func TestTransactionsBadCursor(t *testing.T) {
	srv, _ := newApp(t)
	resp, _ := get(t, srv, "/transactions?before=garbage")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestTransactionsEmptyDatabase(t *testing.T) {
	srv, _ := newApp(t)
	_, d := get(t, srv, "/transactions")
	assert.Equal(t, "0 transactions", d.Find("p.count").Text())
	assert.Equal(t, 1, d.Find("p.empty").Length())
}

func TestTransactionsTotalNetsMatchingRows(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	s.spend("2026-09-01", "meal (john)", 10000)
	_, err := s.svc.Create(context.Background(), ledger.TxnInput{
		Date: "2026-09-02", Description: "transfer (john)", Currency: "USD",
		Splits: []ledger.SplitInput{
			{AccountID: s.acct["wharf-bank"], Currency: "USD", Amount: 5000, Value: 5000},
			{AccountID: s.acct["income"], Currency: "USD", Amount: -5000, Value: -5000},
		},
	})
	require.NoError(t, err)
	s.spend("2026-09-03", "lunch (mary)", 700)

	_, d := get(t, srv, "/transactions")
	assert.Equal(t, 0, d.Find("#txn-total").Length(), "no total without a search or filter")

	_, d = get(t, srv, "/transactions?q=john")
	total := d.Find("#txn-total strong")
	require.Equal(t, 1, total.Length())
	assert.Equal(t, "-$50.00 USD", strings.TrimSpace(total.Text()), "-100 paid, +50 paid back, across both accounts")
	assert.True(t, total.HasClass("out"))
	assert.Contains(t, d.Find("#txn-total span").Text(), "2 transactions")

	_, d = getHTMX(t, srv, "/transactions?q=john")
	assert.Equal(t, "-$50.00 USD", strings.TrimSpace(d.Find("#txn-total strong").Text()), "live filter swap carries it too")

	_, d = get(t, srv, "/transactions?q=transfer")
	total = d.Find("#txn-total strong")
	assert.Equal(t, "+$50.00 USD", strings.TrimSpace(total.Text()))
	assert.True(t, total.HasClass("in"))

	_, d = get(t, srv, "/transactions?q=nobody")
	assert.Equal(t, 0, d.Find("#txn-total").Length(), "nothing matched, nothing to total")
}

func TestTransactionsGroupedByDay(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	s.spend("2026-08-31", "last", 100)
	for i := 0; i < 52; i++ { // one day that straddles the page boundary
		s.spend("2026-09-01", fmt.Sprintf("mid-%02d", i), 100)
	}
	for i := 0; i < 3; i++ {
		s.spend("2026-09-02", fmt.Sprintf("top-%d", i), 100)
	}

	headings := func(d *goquery.Document) []string {
		var out []string
		d.Find("li.day").Each(func(_ int, s *goquery.Selection) { out = append(out, strings.TrimSpace(s.Text())) })
		return out
	}

	_, d := get(t, srv, "/transactions")
	assert.Equal(t, []string{"September 2, 2026", "September 1, 2026"}, headings(d))
	first := d.Find("#txn-list > li").First()
	assert.True(t, first.HasClass("day"), "a heading comes before the first row")

	next, _ := d.Find("li.more").Attr("hx-get")
	require.NotEmpty(t, next)
	_, frag := getHTMX(t, srv, next)
	assert.Equal(t, []string{"August 31, 2026"}, headings(frag), "September 1 continues the page above, so it gets no second heading")
	assert.Equal(t, 5, frag.Find("li.txn").Length()-1)
}
