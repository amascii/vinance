//go:build e2e

package e2e

import (
	"testing"

	"github.com/mxschmitt/playwright-go"
	"github.com/stretchr/testify/require"
)

func TestRecurringJourney(t *testing.T) {
	srv, _ := newServerWithBilt(t) // server clock is fixed at 2026-10-01
	page := newPage(t, playwright.BrowserNewContextOptions{Viewport: &playwright.Size{Width: 390, Height: 844}})
	_, err := page.Goto(srv.URL + "/recurring")
	require.NoError(t, err)
	require.NoError(t, expect.Locator(page.Locator("ul.due-list")).ToHaveCount(0))

	// Add monthly rent that started last month.
	require.NoError(t, page.Locator("details.add-account summary").Click())
	form := page.Locator("details.add-account form")
	require.NoError(t, form.Locator("input[name=text]").Fill("1500 Rent #rent @brisk"))
	require.NoError(t, form.Locator("input[name=anchor]").Fill("2026-09-01"))
	require.NoError(t, form.Locator("button[type=submit]").Click())
	require.NoError(t, expect.Locator(page.Locator(".flash")).ToContainText("Monthly on the 1st"))

	// September's rent is overdue and October's is due today.
	due := page.Locator("ul.due-list > li")
	require.NoError(t, expect.Locator(due).ToHaveCount(2))
	require.NoError(t, expect.Locator(due.First().Locator(".chip.warn")).ToHaveText("overdue"))
	require.NoError(t, expect.Locator(due.First().Locator(".amount")).ToHaveText("-$1,500.00"))

	// Home advertises them.
	_, err = page.Goto(srv.URL + "/")
	require.NoError(t, err)
	require.NoError(t, expect.Locator(page.Locator(".due-banner")).ToContainText("2 recurring transactions are due"))
	require.NoError(t, page.Locator(".due-banner a").Click())
	require.NoError(t, expect.Page(page).ToHaveURL(regexpFor(`/recurring$`)))

	// Add September's, skip October's.
	require.NoError(t, due.First().Locator("button", playwright.LocatorLocatorOptions{HasText: "Add"}).Click())
	require.NoError(t, expect.Locator(page.Locator(".flash")).ToHaveText("Added Rent $1,500.00 for 2026-09-01."))
	require.NoError(t, expect.Locator(due).ToHaveCount(1))
	require.NoError(t, due.First().Locator("button", playwright.LocatorLocatorOptions{HasText: "Skip"}).Click())
	require.NoError(t, expect.Locator(page.Locator(".flash")).ToHaveText("Skipped 2026-10-01."))
	require.NoError(t, expect.Locator(page.Locator("p.empty", playwright.PageLocatorOptions{HasText: "Nothing is due"})).ToBeVisible())
	require.NoError(t, expect.Locator(page.Locator("ul.rule-list .tag-uses")).ToHaveText("Next: 2026-11-01"))

	// The added rent is a normal transaction, and the banner is gone.
	_, err = page.Goto(srv.URL + "/transactions")
	require.NoError(t, err)
	require.NoError(t, expect.Locator(page.Locator("li.txn")).ToHaveCount(1))
	require.NoError(t, expect.Locator(page.Locator("li.txn")).ToContainText("Rent"))
	require.NoError(t, expect.Locator(page.Locator("li.txn .date")).ToHaveText("2026-09-01"))
	_, err = page.Goto(srv.URL + "/")
	require.NoError(t, err)
	require.NoError(t, expect.Locator(page.Locator(".due-banner")).ToHaveCount(0))

	overflow, err := page.Evaluate(`document.documentElement.scrollWidth > document.documentElement.clientWidth`)
	require.NoError(t, err)
	require.Equal(t, false, overflow)
}
