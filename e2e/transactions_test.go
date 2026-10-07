//go:build e2e

package e2e

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"testing"

	"github.com/mxschmitt/playwright-go"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/db/gen"
	"github.com/amascii/vinance/internal/ledger"
	"github.com/amascii/vinance/internal/testutil"
)

func seed120(t *testing.T) string {
	t.Helper()
	srv, conn := testutil.NewServerWithDB(t)
	testutil.SeedDefaultAccounts(t, conn)
	for i := 0; i < 120; i++ {
		tag := "even"
		if i%2 == 1 {
			tag = "odd"
		}
		testutil.SeedSpend(t, conn, fmt.Sprintf("2026-08-%02d", 1+i/5), fmt.Sprintf("txn-%03d", i), int64(100+i), tag)
	}
	return srv.URL
}

func scrollToBottom(t *testing.T, page playwright.Page) {
	t.Helper()
	_, err := page.Evaluate(`window.scrollTo(0, document.body.scrollHeight)`)
	require.NoError(t, err)
}

func TestTransactionsInfiniteScroll(t *testing.T) {
	page := newPage(t)
	_, err := page.Goto(seed120(t) + "/transactions")
	require.NoError(t, err)

	rows := page.Locator("#txn-list li.txn")
	require.NoError(t, expect.Locator(rows).ToHaveCount(50))
	require.NoError(t, expect.Locator(page.Locator("p.count")).ToHaveText("120 transactions"))

	scrollToBottom(t, page)
	require.NoError(t, expect.Locator(rows).ToHaveCount(100))
	scrollToBottom(t, page)
	require.NoError(t, expect.Locator(rows).ToHaveCount(120))
	require.NoError(t, expect.Locator(page.Locator("li.more")).ToHaveCount(0), "the sentinel goes away on the last page")
}

func TestTransactionsLiveFilterAndHistory(t *testing.T) {
	page := newPage(t)
	_, err := page.Goto(seed120(t) + "/transactions")
	require.NoError(t, err)
	rows := page.Locator("#txn-list li.txn")
	require.NoError(t, expect.Locator(rows).ToHaveCount(50))

	// Typing filters live (no submit), updates the count and the URL.
	require.NoError(t, page.Locator("input[name=q]").PressSequentially("txn-07"))
	require.NoError(t, expect.Locator(page.Locator("p.count")).ToHaveText("10 transactions"))
	require.NoError(t, expect.Locator(rows).ToHaveCount(10))
	require.NoError(t, expect.Page(page).ToHaveURL(regexp.MustCompile(`/transactions\?q=txn-07$`)))

	// A tag filter combines with it.
	require.NoError(t, page.Locator("input[name=tag]").Fill("#even"))
	require.NoError(t, expect.Locator(page.Locator("p.count")).ToHaveText("5 transactions"))

	// Inputs keep focus and text while the results swap underneath them.
	v, err := page.Locator("input[name=q]").InputValue()
	require.NoError(t, err)
	require.Equal(t, "txn-07", v)

	// Back returns to a complete, correctly filtered page.
	_, err = page.GoBack()
	require.NoError(t, err)
	require.NoError(t, expect.Locator(page.Locator("p.count")).ToHaveText("10 transactions"))
	require.NoError(t, expect.Locator(page.Locator("input[name=q]")).ToHaveValue("txn-07"))
	require.NoError(t, expect.Locator(page.Locator("input[name=tag]")).ToHaveValue(""))

	// Clear filters goes back to everything.
	require.NoError(t, page.Locator("a.clear").Click())
	require.NoError(t, expect.Locator(page.Locator("p.count")).ToHaveText("120 transactions"))
}

func TestTransactionsSplitDetailExpands(t *testing.T) {
	srv, conn := testutil.NewServerWithDB(t)
	testutil.SeedDefaultAccounts(t, conn)
	seedWalmart(t, conn)

	page := newPage(t, playwright.BrowserNewContextOptions{Viewport: &playwright.Size{Width: 390, Height: 844}})
	_, err := page.Goto(srv.URL + "/transactions")
	require.NoError(t, err)

	row := page.Locator("#txn-list li.txn").First()
	require.NoError(t, expect.Locator(row.Locator(".line1 .desc")).ToHaveText("Walmart"))
	lines := row.Locator("ul.splits li.split")
	require.NoError(t, expect.Locator(lines.First()).ToBeHidden())

	require.NoError(t, row.Locator("summary").Click())
	require.NoError(t, expect.Locator(lines).ToHaveCount(4))
	require.NoError(t, expect.Locator(lines.Nth(1)).ToContainText("Sodas"))
	require.NoError(t, expect.Locator(lines.Nth(1)).ToContainText("#drinks"))
	require.NoError(t, expect.Locator(lines.Nth(1)).ToContainText("$10.00"))

	overflow, err := page.Evaluate(`document.documentElement.scrollWidth > document.documentElement.clientWidth`)
	require.NoError(t, err)
	require.Equal(t, false, overflow)
}

// seedWalmart records a $65 BRISK charge split into drinks, snacks and an untagged remainder.
func seedWalmart(t *testing.T, conn *sql.DB) {
	t.Helper()
	ctx := context.Background()
	q := gen.New(conn)
	brisk, err := q.GetAccountBySlug(ctx, "brisk")
	require.NoError(t, err)
	exp, err := q.GetBuiltinAccount(ctx, "expense")
	require.NoError(t, err)
	_, err = ledger.NewService(conn).Create(ctx, ledger.TxnInput{
		Date: "2026-09-02", Description: "Walmart", Currency: "USD",
		Splits: []ledger.SplitInput{
			{AccountID: brisk.ID, Currency: "USD", Amount: -6500, Value: -6500},
			{AccountID: exp.ID, Memo: "Sodas", Currency: "USD", Amount: 1000, Value: 1000, Tags: []string{"drinks"}},
			{AccountID: exp.ID, Memo: "Chips", Currency: "USD", Amount: 2000, Value: 2000, Tags: []string{"snacks"}},
			{AccountID: exp.ID, Currency: "USD", Amount: 3500, Value: 3500},
		},
	})
	require.NoError(t, err)
}

func TestTransactionsTotalCoversWholeResultSet(t *testing.T) {
	page := newPage(t)
	_, err := page.Goto(seed120(t) + "/transactions")
	require.NoError(t, err)
	total := page.Locator("#txn-total")
	require.NoError(t, expect.Locator(total).ToHaveCount(0), "no total without a filter")

	// 100 matches span two pages; the total still sums all of them (100*100 + 0..99 cents).
	require.NoError(t, page.Locator("input[name=q]").PressSequentially("txn-0"))
	require.NoError(t, expect.Locator(page.Locator("p.count")).ToHaveText("100 transactions"))
	require.NoError(t, expect.Locator(page.Locator("#txn-list li.txn")).ToHaveCount(50))
	require.NoError(t, expect.Locator(total).ToContainText("-$149.50"))

	// Narrowing the search updates it live (txn-07x: 100+70..100+79 cents).
	require.NoError(t, page.Locator("input[name=q]").PressSequentially("7"))
	require.NoError(t, expect.Locator(page.Locator("p.count")).ToHaveText("10 transactions"))
	require.NoError(t, expect.Locator(total).ToContainText("-$17.45"))
}
