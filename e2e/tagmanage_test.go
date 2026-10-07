//go:build e2e

package e2e

import (
	"testing"

	"github.com/mxschmitt/playwright-go"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/testutil"
)

func tagItem(page playwright.Page, name string) playwright.Locator {
	return page.Locator("ul.tag-list > li", playwright.PageLocatorOptions{Has: page.Locator(".tag-name", playwright.PageLocatorOptions{HasText: "#" + name})})
}

func TestTagManagementJourney(t *testing.T) {
	srv, conn := testutil.NewServerWithDB(t)
	testutil.SeedDefaultAccounts(t, conn)
	testutil.SeedSpend(t, conn, "2026-09-01", "Belt", 2500, "accesories")
	testutil.SeedSpend(t, conn, "2026-09-02", "Milk", 400, "ingredients")
	testutil.SeedSpend(t, conn, "2026-09-03", "Eggs", 600, "ingredients")
	testutil.SeedSpend(t, conn, "2026-09-04", "HEB", 4200, "groceries")
	_, err := conn.Exec("INSERT INTO tags (name) VALUES ('stale')")
	require.NoError(t, err)
	page := newPage(t, playwright.BrowserNewContextOptions{Viewport: &playwright.Size{Width: 390, Height: 844}})
	_, err = page.Goto(srv.URL + "/tags")
	require.NoError(t, err)

	require.NoError(t, page.Locator("a", playwright.PageLocatorOptions{HasText: "Rename, merge or delete tags"}).Click())
	require.NoError(t, expect.Page(page).ToHaveURL(regexpFor(`/tags/manage$`)))
	require.NoError(t, expect.Locator(page.Locator("ul.tag-list > li")).ToHaveCount(4))

	// Fix the typo.
	item := tagItem(page, "accesories")
	require.NoError(t, item.Locator("summary").Click())
	require.NoError(t, item.Locator("input[name=to]").Fill("accessories"))
	require.NoError(t, item.Locator("button", playwright.LocatorLocatorOptions{HasText: "Save"}).Click())
	require.NoError(t, expect.Locator(page.Locator(".flash")).ToHaveText("Renamed #accesories to #accessories."))
	require.NoError(t, expect.Locator(page.Locator(".tag-name", playwright.PageLocatorOptions{HasText: "#accessories"})).ToHaveCount(1))
	require.NoError(t, expect.Locator(page.Locator(".tag-name", playwright.PageLocatorOptions{HasText: "#accesories"})).ToHaveCount(0))

	// Merging asks first. Cancel leaves everything alone...
	item = tagItem(page, "ingredients")
	require.NoError(t, item.Locator("summary").Click())
	require.NoError(t, item.Locator("input[name=to]").Fill("groceries"))
	require.NoError(t, item.Locator("button", playwright.LocatorLocatorOptions{HasText: "Save"}).Click())
	require.NoError(t, expect.Locator(page.Locator(".merge-confirm")).ToContainText("Merge ingredients (2 lines) into it?"))
	require.NoError(t, page.Locator(".merge-confirm a", playwright.PageLocatorOptions{HasText: "Cancel"}).Click())
	require.NoError(t, expect.Locator(page.Locator(".merge-confirm")).ToHaveCount(0))
	require.NoError(t, expect.Locator(tagItem(page, "ingredients")).ToHaveCount(1))

	// ...and confirming merges the two.
	item = tagItem(page, "ingredients")
	require.NoError(t, item.Locator("summary").Click())
	require.NoError(t, item.Locator("input[name=to]").Fill("groceries"))
	require.NoError(t, item.Locator("button", playwright.LocatorLocatorOptions{HasText: "Save"}).Click())
	require.NoError(t, page.Locator(".merge-confirm button", playwright.PageLocatorOptions{HasText: "Merge"}).Click())
	require.NoError(t, expect.Locator(page.Locator(".flash")).ToHaveText("Merged #ingredients into #groceries (2 lines)."))
	require.NoError(t, expect.Locator(tagItem(page, "ingredients")).ToHaveCount(0))
	require.NoError(t, expect.Locator(tagItem(page, "groceries").Locator(".tag-uses")).ToHaveText("3 lines"))

	// Unused tags can be deleted; used ones don't offer it.
	require.NoError(t, expect.Locator(tagItem(page, "groceries").Locator("button", playwright.LocatorLocatorOptions{HasText: "Delete"})).ToHaveCount(0))
	stale := tagItem(page, "stale")
	require.NoError(t, stale.Locator("summary").Click())
	require.NoError(t, stale.Locator("button", playwright.LocatorLocatorOptions{HasText: "Delete unused tag"}).Click())
	require.NoError(t, expect.Locator(page.Locator(".flash")).ToHaveText("Deleted #stale."))
	require.NoError(t, expect.Locator(page.Locator("ul.tag-list > li")).ToHaveCount(2))

	// The flash doesn't linger on reload.
	_, err = page.Reload()
	require.NoError(t, err)
	require.NoError(t, expect.Locator(page.Locator(".flash")).ToHaveCount(0))

	overflow, err := page.Evaluate(`document.documentElement.scrollWidth > document.documentElement.clientWidth`)
	require.NoError(t, err)
	require.Equal(t, false, overflow)
}
