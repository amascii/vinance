package web_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/ledger"
)

// regRows returns "description | amount | running" for each row of an account view.
func regRows(d *goquery.Document) []string {
	var out []string
	d.Find("#txn-list > li.txn").Each(func(_ int, li *goquery.Selection) {
		out = append(out, rowTexts(li.Find(".desc"))+" | "+li.Find(".line1 .amount").First().Text()+" | "+rowTexts(li.Find(".running").First()))
	})
	return out
}

// registerFixture: Wharf Bank (asset) and BRISK (card) with a paycheck, a card payment (a transfer) and spending.
func registerFixture(t *testing.T) (srvURL string, get func(string) *goquery.Document) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	ctx := context.Background()
	mk := func(date, desc string, lines ...ledger.SplitInput) {
		_, err := s.svc.Create(ctx, ledger.TxnInput{Date: date, Description: desc, Currency: "USD", Splits: lines})
		require.NoError(t, err)
	}
	usd := func(acct int64, cents int64, tags ...string) ledger.SplitInput {
		return ledger.SplitInput{AccountID: acct, Currency: "USD", Amount: cents, Value: cents, Tags: tags}
	}
	mk("2026-09-01", "Paycheck", usd(s.acct["wharf-bank"], 100000), usd(s.acct["income"], -100000, "salary"))
	mk("2026-09-03", "Walmart", usd(s.acct["brisk"], -6500), usd(s.acct["expense"], 6500, "groceries"))
	mk("2026-09-05", "Card payment", usd(s.acct["wharf-bank"], -5000), usd(s.acct["brisk"], 5000))
	mk("2026-09-07", "Gas", usd(s.acct["wharf-bank"], -2000), usd(s.acct["expense"], 2000, "fuel"))
	return srv.URL, func(path string) *goquery.Document {
		resp, err := http.Get(srv.URL + path)
		require.NoError(t, err)
		return doc(t, resp)
	}
}

func TestAccountViewShowsARegister(t *testing.T) {
	_, get := registerFixture(t)

	d := get("/transactions?account=wharf-bank")
	assert.Equal(t, "Wharf Bank · Balance $930.00", d.Find("p.account-header").Text())
	assert.Equal(t, []string{
		"Gas | -$20.00 | Balance $930.00",
		"Card payment | -$50.00 | Balance $950.00",
		"Paycheck | +$1,000.00 | Balance $1,000.00",
	}, regRows(d), "each row: this account's change, and its balance afterwards")
	assert.True(t, d.Find("li.txn").Eq(0).HasClass("dir-out"))
	assert.True(t, d.Find("li.txn").Eq(2).HasClass("dir-in"))
	title, _ := d.Find("li.txn .running").First().Attr("title")
	assert.Equal(t, "Balance after this transaction", title)
}

func TestAccountViewForACreditCardShowsWhatIsOwed(t *testing.T) {
	_, get := registerFixture(t)

	d := get("/transactions?account=brisk")
	assert.Equal(t, "BRISK · Owed $15.00", d.Find("p.account-header").Text(), "matches the Accounts page: $65 charged, $50 paid")
	assert.Equal(t, []string{
		"Card payment | +$50.00 | Owed $15.00",
		"Walmart | -$65.00 | Owed $65.00",
	}, regRows(d), "a charge is money out (-), a payment is money in (+); the running figure is what is owed")

	// The other side of the same transfer shows the opposite movement.
	w := get("/transactions?account=wharf-bank")
	assert.Contains(t, regRows(w), "Card payment | -$50.00 | Balance $950.00")
}

func TestRegisterBalancesIgnoreFilters(t *testing.T) {
	_, get := registerFixture(t)

	// Only the last transaction is shown, but its balance counts everything before it.
	d := get("/transactions?account=wharf-bank&from=2026-09-06")
	assert.Equal(t, []string{"Gas | -$20.00 | Balance $930.00"}, regRows(d))
	assert.Equal(t, "Wharf Bank · Balance $930.00", d.Find("p.account-header").Text(), "the header is the account's balance, not the filtered total")

	d = get("/transactions?account=wharf-bank&tag=salary")
	assert.Equal(t, []string{"Paycheck | +$1,000.00 | Balance $1,000.00"}, regRows(d))

	d = get("/transactions?account=wharf-bank&q=card")
	assert.Equal(t, []string{"Card payment | -$50.00 | Balance $950.00"}, regRows(d))

	d = get("/transactions?account=wharf-bank&q=nothing-matches")
	assert.Empty(t, regRows(d))
	assert.Equal(t, "Wharf Bank · Balance $930.00", d.Find("p.account-header").Text())
}

func TestRegisterIsOnlyForARealAccount(t *testing.T) {
	_, get := registerFixture(t)
	for _, path := range []string{"/transactions", "/transactions?tag=fuel", "/transactions?account=expenses", "/transactions?account=nope"} {
		d := get(path)
		assert.Equal(t, 0, d.Find(".running").Length(), path)
		assert.Equal(t, 0, d.Find("p.account-header").Length(), path)
	}
	d := get("/transactions")
	assert.Equal(t, "Gas | -$20.00 | ", regRows(d)[0], "the normal list keeps its whole-transaction amounts")
}

func TestRegisterContinuesAcrossInfiniteScrollPages(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	// 120 purchases of $1.00, $2.00, ... on BRISK; the balance after the n-th is -(n(n+1)/2) dollars.
	for i := 1; i <= 120; i++ {
		_, err := s.svc.Create(context.Background(), ledger.TxnInput{
			Date: fmt.Sprintf("2026-08-%02d", 1+(i-1)/5), Description: fmt.Sprintf("p%03d", i), Currency: "USD",
			Splits: []ledger.SplitInput{
				{AccountID: s.acct["brisk"], Currency: "USD", Amount: -int64(i) * 100, Value: -int64(i) * 100},
				{AccountID: s.acct["expense"], Currency: "USD", Amount: int64(i) * 100, Value: int64(i) * 100},
			},
		})
		require.NoError(t, err)
	}
	owed := func(n int) string { return fmt.Sprintf("Owed %s", ledger.Format(int64(n*(n+1)/2)*100, "USD")) }

	resp, err := http.Get(srv.URL + "/transactions?account=brisk")
	require.NoError(t, err)
	d := doc(t, resp)
	assert.Equal(t, "BRISK · "+owed(120), d.Find("p.account-header").Text())
	first := regRows(d)
	require.Len(t, first, 50)
	assert.Equal(t, "p120 | -$120.00 | "+owed(120), first[0])
	assert.Equal(t, "p071 | -$71.00 | "+owed(71), first[49])

	// The next page picks up exactly where the first left off, with balances from the full history.
	next, _ := d.Find("li.more").Attr("hx-get")
	_, frag := getHTMX(t, srv, next)
	var second []string
	frag.Find("li.txn").Each(func(_ int, li *goquery.Selection) {
		second = append(second, rowTexts(li.Find(".desc"))+" | "+li.Find(".line1 .amount").First().Text()+" | "+rowTexts(li.Find(".running").First()))
	})
	require.Len(t, second, 50)
	assert.Equal(t, "p070 | -$70.00 | "+owed(70), second[0])
	assert.Equal(t, "p021 | -$21.00 | "+owed(21), second[49])
	assert.Equal(t, 0, frag.Find("p.account-header").Length(), "the header is not repeated on later pages")
}

func TestRegisterInTheAccountsOwnCurrency(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	_, err := s.svc.Create(context.Background(), ledger.TxnInput{
		Date: "2026-09-01", Description: "Internet bill", Currency: "MXN",
		Splits: []ledger.SplitInput{
			{AccountID: s.acct["neo"], Currency: "MXN", Amount: -50000, Value: -50000},
			{AccountID: s.acct["expense"], Currency: "USD", Amount: 2810, Value: 50000, Tags: []string{"internet"}},
		},
	})
	require.NoError(t, err)

	resp, err := http.Get(srv.URL + "/transactions?account=neo")
	require.NoError(t, err)
	d := doc(t, resp)
	assert.Equal(t, []string{"Internet bill | -MX$500.00 | Balance -MX$500.00"}, regRows(d), "pesos, even though the expense line is tracked in dollars")
	assert.Equal(t, "Neo · Balance -MX$500.00", d.Find("p.account-header").Text())
}

func TestRegisterRowsStillLinkToTheEditor(t *testing.T) {
	_, get := registerFixture(t)
	d := get("/transactions?account=wharf-bank")
	href, ok := d.Find("li.txn a.edit-link").First().Attr("href")
	require.True(t, ok)
	assert.Contains(t, href, "next=%2Ftransactions%3Faccount%3Dwharf-bank")
}
