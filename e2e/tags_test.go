//go:build e2e

package e2e

import (
	"testing"

	"github.com/mxschmitt/playwright-go"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/testutil"
)

func TestTagReportJourney(t *testing.T) {
	srv, conn := testutil.NewServerWithDB(t)
	testutil.SeedDefaultAccounts(t, conn)
	testutil.SeedSpend(t, conn, "2026-10-01", "Dairy Queen", 1250, "fast-food")
	testutil.SeedSpend(t, conn, "2026-10-02", "HEB", 4200, "groceries")
	testutil.SeedSpend(t, conn, "2026-10-03", "Mystery", 500)
	testutil.SeedSpend(t, conn, "2026-08-15", "August treat", 900, "snacks")
	page := newPage(t, playwright.BrowserNewContextOptions{Viewport: &playwright.Size{Width: 390, Height: 844}})
	_, err := page.Goto(srv.URL + "/")
	require.NoError(t, err)

	require.NoError(t, page.Locator("header nav a", playwright.PageLocatorOptions{HasText: "Tags"}).Click())
	require.NoError(t, expect.Page(page).ToHaveURL(regexpFor(`/tags$`)))
	rows := page.Locator("ul.bars li.bar-row")
	require.NoError(t, expect.Locator(rows).ToHaveCount(3), "this month: groceries, fast-food, untagged")
	overflowTags, err := page.Evaluate(`document.documentElement.scrollWidth > document.documentElement.clientWidth`)
	require.NoError(t, err)
	require.Equal(t, false, overflowTags, "the report itself fits a phone (range form included)")
	for _, sel := range []string{"input[name=to]", "form.range-form button"} {
		box, err := page.Locator(sel).BoundingBox()
		require.NoError(t, err)
		require.LessOrEqual(t, box.X+box.Width, 390.0, sel+" stays on screen")
	}
	require.NoError(t, expect.Locator(page.Locator(".networth .figure")).ToHaveText("$59.50"))

	// The bars are proportional: the biggest fills the track, smaller ones are shorter.
	groceries, err := rows.First().Locator(".bar").BoundingBox()
	require.NoError(t, err)
	fastFood, err := rows.Nth(1).Locator(".bar").BoundingBox()
	require.NoError(t, err)
	require.Greater(t, groceries.Width, fastFood.Width)
	track, err := rows.First().Locator(".bar-track").BoundingBox()
	require.NoError(t, err)
	require.InDelta(t, track.Width, groceries.Width, 1.5, "the largest bar spans the track")
	require.LessOrEqual(t, groceries.Height, 24.0, "bars stay thin")

	// Widening the range pulls in August.
	require.NoError(t, page.Locator("nav[aria-label='Date range'] a", playwright.PageLocatorOptions{HasText: "All time"}).Click())
	require.NoError(t, expect.Locator(rows).ToHaveCount(4))
	require.NoError(t, expect.Locator(page.Locator(".networth .figure")).ToHaveText("$68.50"))

	// Clicking a bar lists the transactions behind it, within the same range.
	require.NoError(t, rows.Filter(playwright.LocatorFilterOptions{HasText: "snacks"}).Locator("a.bar-label").Click())
	require.NoError(t, expect.Locator(page.Locator("li.txn")).ToHaveCount(1))
	require.NoError(t, expect.Locator(page.Locator("li.txn")).ToContainText("August treat"))

	// So does Untagged.
	_, err = page.Goto(srv.URL + "/tags")
	require.NoError(t, err)
	require.NoError(t, page.Locator("ul.bars a.bar-label", playwright.PageLocatorOptions{HasText: "Untagged"}).Click())
	require.NoError(t, expect.Locator(page.Locator("li.txn")).ToHaveCount(1))
	require.NoError(t, expect.Locator(page.Locator("li.txn")).ToContainText("Mystery"))

	overflow, err := page.Evaluate(`document.documentElement.scrollWidth > document.documentElement.clientWidth`)
	require.NoError(t, err)
	require.Equal(t, false, overflow)
}
