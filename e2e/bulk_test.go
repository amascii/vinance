//go:build e2e

package e2e

import (
	"testing"

	"github.com/mxschmitt/playwright-go"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/testutil"
)

// TestBulkTagSelectedTransactions: tick two of three rows, add a tag, confirm; only those two change.
func TestBulkTagSelectedTransactions(t *testing.T) {
	srv, conn := testutil.NewServerWithDB(t)
	testutil.SeedDefaultAccounts(t, conn)
	testutil.SeedSpend(t, conn, "2026-09-01", "Alpha", 100, "x")
	testutil.SeedSpend(t, conn, "2026-09-02", "Beta", 200, "x")
	testutil.SeedSpend(t, conn, "2026-09-03", "Gamma", 300, "x")
	page := newPage(t, playwright.BrowserNewContextOptions{Viewport: &playwright.Size{Width: 1280, Height: 800}})
	_, err := page.Goto(srv.URL + "/transactions")
	require.NoError(t, err)
	settled(t, page)

	row := func(name string) playwright.Locator {
		return page.Locator("li.txn", playwright.PageLocatorOptions{HasText: name})
	}
	bar := page.Locator("#bulk")
	require.NoError(t, expect.Locator(bar).ToBeHidden(), "no bar until something is ticked")

	require.NoError(t, row("Alpha").Locator("input.pick").Check())
	require.NoError(t, row("Gamma").Locator("input.pick").Check())
	require.NoError(t, expect.Locator(bar).ToBeVisible())
	require.NoError(t, expect.Locator(page.Locator("#bulk-count")).ToHaveText("2 selected"))

	require.NoError(t, bar.Locator("input[name=bulk_tag]").Fill("#trip"))
	require.NoError(t, bar.Locator("button[type=submit]").Click())
	require.NoError(t, expect.Locator(page.Locator(".bulk-summary")).ToContainText("Add #trip to 2 transactions"))
	require.NoError(t, page.Locator("#apply").Click())

	require.NoError(t, expect.Locator(page.Locator(".flash")).ToHaveText("Added #trip: 2 transactions updated (2 lines)."))
	require.NoError(t, expect.Locator(row("Alpha").Locator(".chip.tag")).ToHaveText([]string{"#trip", "#x"}))
	require.NoError(t, expect.Locator(row("Beta").Locator(".chip.tag")).ToHaveText([]string{"#x"}))
	require.NoError(t, expect.Locator(row("Gamma").Locator(".chip.tag")).ToHaveText([]string{"#trip", "#x"}))
}

// TestBulkMoveTagForEverythingMatching: "select all N matching" covers rows beyond the first page,
// and changing the filter afterwards clears the selection.
func TestBulkMoveTagForEverythingMatching(t *testing.T) {
	srv, conn := testutil.NewServerWithDB(t)
	testutil.SeedDefaultAccounts(t, conn)
	for i := 0; i < 55; i++ {
		testutil.SeedSpend(t, conn, "2026-09-01", "Latte", 100, "cafes")
	}
	testutil.SeedSpend(t, conn, "2026-09-02", "Rent", 500, "cafes")
	page := newPage(t, playwright.BrowserNewContextOptions{Viewport: &playwright.Size{Width: 1280, Height: 800}})
	_, err := page.Goto(srv.URL + "/transactions?q=latte")
	require.NoError(t, err)
	settled(t, page)

	bar := page.Locator("#bulk")
	require.NoError(t, page.Locator("li.txn input.pick").First().Check())
	require.NoError(t, expect.Locator(page.Locator("#bulk-all")).ToHaveText("Select all 55 matching"))
	require.NoError(t, page.Locator("#bulk-all").Click())
	require.NoError(t, expect.Locator(page.Locator("#bulk-count")).ToHaveText("All 55 matching selected"))

	// Changing the search starts the selection over.
	require.NoError(t, page.Locator("#filters input[name=q]").Fill("rent"))
	require.NoError(t, expect.Locator(page.Locator("#txn-list li.txn")).ToHaveCount(1))
	require.NoError(t, expect.Locator(page.Locator("#bulk")).ToBeHidden())
	require.NoError(t, page.Locator("#filters input[name=q]").Fill("latte"))
	require.NoError(t, expect.Locator(page.Locator("#txn-list li.txn")).ToHaveCount(50))
	require.NoError(t, expect.Locator(page.Locator("input.pick:checked")).ToHaveCount(0))

	require.NoError(t, page.Locator("li.txn input.pick").First().Check())
	require.NoError(t, page.Locator("#bulk-all").Click())
	_, err = bar.Locator("select[name=op]").SelectOption(playwright.SelectOptionValues{Values: &[]string{"move"}})
	require.NoError(t, err)
	require.NoError(t, bar.Locator("input[name=bulk_tag]").Fill("cafes"))
	require.NoError(t, bar.Locator("input[name=bulk_to]").Fill("coffee"))
	require.NoError(t, bar.Locator("button[type=submit]").Click())
	require.NoError(t, expect.Locator(page.Locator(".bulk-summary")).ToContainText("on 55 transactions"))
	require.NoError(t, page.Locator("#apply").Click())
	require.NoError(t, expect.Locator(page.Locator(".flash")).ToHaveText("Moved #cafes → #coffee: 55 transactions updated (55 lines)."))

	_, err = page.Goto(srv.URL + "/transactions?q=rent")
	require.NoError(t, err)
	require.NoError(t, expect.Locator(page.Locator("li.txn .chip.tag")).ToHaveText([]string{"#cafes"}), "Rent was outside the filter")
}
