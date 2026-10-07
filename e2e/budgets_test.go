//go:build e2e

package e2e

import (
	"testing"

	"github.com/mxschmitt/playwright-go"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/testutil"
)

func TestBudgetsJourney(t *testing.T) {
	srv, conn := newServerWithBilt(t) // clock fixed at 2026-10-01
	testutil.SeedSpend(t, conn, "2026-10-01", "HEB", 27500, "groceries")
	testutil.SeedSpend(t, conn, "2026-10-01", "Dairy Queen", 3000, "fast-food")
	testutil.SeedSpend(t, conn, "2026-09-12", "September treat", 1500, "fast-food")
	page := newPage(t, playwright.BrowserNewContextOptions{Viewport: &playwright.Size{Width: 390, Height: 844}})
	_, err := page.Goto(srv.URL + "/")
	require.NoError(t, err)

	require.NoError(t, page.Locator("header nav a", playwright.PageLocatorOptions{HasText: "Budgets"}).Click())
	require.NoError(t, expect.Page(page).ToHaveURL(regexpFor(`/budgets$`)))
	require.NoError(t, expect.Locator(page.Locator("p.empty")).ToContainText("No budgets yet"))

	// Set two budgets: one comfortable, one already blown.
	add := page.Locator(`form[action="/budgets/set"]`).Last()
	require.NoError(t, add.Locator("input[name=tag]").Fill("groceries"))
	require.NoError(t, add.Locator("input[name=amount]").Fill("400"))
	require.NoError(t, add.Locator("button[type=submit]").Click())
	require.NoError(t, expect.Locator(page.Locator(".flash")).ToHaveText("Budget for #groceries is $400.00 a month."))

	add = page.Locator(`form[action="/budgets/set"]`).Last()
	require.NoError(t, add.Locator("input[name=tag]").Fill("fast-food"))
	require.NoError(t, add.Locator("input[name=amount]").Fill("20"))
	require.NoError(t, add.Locator("button[type=submit]").Click())

	items := page.Locator("ul.budgets > li")
	require.NoError(t, expect.Locator(items).ToHaveCount(2))
	over := items.First()
	require.NoError(t, expect.Locator(over.Locator(".bar-label")).ToHaveText("fast-food"), "the most-used budget leads")
	require.NoError(t, expect.Locator(over.Locator(".meter-status")).ToContainText("$10.00 over"))
	require.NoError(t, expect.Locator(over.Locator(".state-icon")).ToHaveText("⚠"))
	require.NoError(t, expect.Locator(items.Nth(1).Locator(".meter-status")).ToContainText("$125.00 left"))
	require.NoError(t, expect.Locator(items.Nth(1).Locator(".state-icon")).ToHaveText("✓"))

	// The meter fills in proportion (69% of a 10px-tall bar, capped at the track when over).
	track, err := items.Nth(1).Locator(".meter").BoundingBox()
	require.NoError(t, err)
	fill, err := items.Nth(1).Locator(".meter-fill").BoundingBox()
	require.NoError(t, err)
	require.InDelta(t, 0.69, fill.Width/track.Width, 0.02)
	require.LessOrEqual(t, track.Height, 24.0, "thin meter")
	overFill, _ := over.Locator(".meter-fill").BoundingBox()
	overTrack, _ := over.Locator(".meter").BoundingBox()
	require.InDelta(t, overTrack.Width, overFill.Width, 1.5, "over budget: the bar is full, never overflowing the track")

	// Change an amount.
	require.NoError(t, items.Nth(1).Locator("summary").Click())
	require.NoError(t, items.Nth(1).Locator("input[name=amount]").Fill("300"))
	require.NoError(t, items.Nth(1).Locator("button", playwright.LocatorLocatorOptions{HasText: "Save"}).Click())
	require.NoError(t, expect.Locator(page.Locator("ul.budgets > li", playwright.PageLocatorOptions{HasText: "groceries"}).Locator(".meter-status")).ToContainText("$25.00 left"))

	// Previous month: its own spending, no pace tick.
	require.NoError(t, page.Locator(`nav.month-nav a[aria-label="Previous month"]`).Click())
	require.NoError(t, expect.Locator(page.Locator("nav.month-nav strong")).ToHaveText("September 2026"))
	require.NoError(t, expect.Locator(page.Locator("ul.budgets > li", playwright.PageLocatorOptions{HasText: "fast-food"}).Locator(".bar-value")).ToContainText("$15.00 of $20.00"))
	require.NoError(t, expect.Locator(page.Locator(".meter-pace")).ToHaveCount(0))

	// Clicking a tag lists that month's transactions for it.
	require.NoError(t, page.Locator("ul.budgets a.bar-label", playwright.PageLocatorOptions{HasText: "fast-food"}).Click())
	require.NoError(t, expect.Locator(page.Locator("li.txn")).ToHaveCount(1))
	require.NoError(t, expect.Locator(page.Locator("li.txn")).ToContainText("September treat"))

	// Remove a budget.
	_, err = page.Goto(srv.URL + "/budgets")
	require.NoError(t, err)
	row := page.Locator("ul.budgets > li", playwright.PageLocatorOptions{HasText: "fast-food"})
	require.NoError(t, row.Locator("summary").Click())
	require.NoError(t, row.Locator("button", playwright.LocatorLocatorOptions{HasText: "Remove budget"}).Click())
	require.NoError(t, expect.Locator(page.Locator("ul.budgets > li")).ToHaveCount(1))

	overflow, err := page.Evaluate(`document.documentElement.scrollWidth > document.documentElement.clientWidth`)
	require.NoError(t, err)
	require.Equal(t, false, overflow)
}
