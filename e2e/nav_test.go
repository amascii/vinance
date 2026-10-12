//go:build e2e

package e2e

import (
	"testing"

	"github.com/mxschmitt/playwright-go"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/testutil"
)

// openMore opens the nav's More menu.
func openMore(t *testing.T, page playwright.Page) {
	t.Helper()
	require.NoError(t, page.Locator("details.nav-more summary").Click())
	require.NoError(t, expect.Locator(page.Locator("details.nav-more ul")).ToBeVisible())
}

func TestNavJourneyOnPhone(t *testing.T) {
	srv, _ := testutil.NewServerWithDB(t)
	page := newPage(t, playwright.BrowserNewContextOptions{Viewport: &playwright.Size{Width: 360, Height: 740}})
	_, err := page.Goto(srv.URL + "/")
	require.NoError(t, err)

	// One row: Home, Accounts, Transactions and More all share a line, and only Accounts shows its label.
	nav := page.Locator("header nav.site-nav")
	box, err := nav.BoundingBox()
	require.NoError(t, err)
	require.Less(t, box.Height, 80.0, "the nav does not wrap onto a second row")
	require.NoError(t, expect.Locator(nav.Locator(`a[href="/accounts"] .label`)).ToBeVisible())
	require.NoError(t, expect.Locator(nav.Locator(`a[href="/transactions"]`)).ToHaveAccessibleName("Transactions"))
	require.NoError(t, expect.Locator(nav.Locator(`a[href="/"]`)).ToHaveAttribute("aria-current", "page"))

	// The menu closes on a click outside and on Escape.
	more := page.Locator("details.nav-more ul")
	require.NoError(t, expect.Locator(more).ToBeHidden())
	openMore(t, page)
	require.NoError(t, page.Mouse().Click(20, 600))
	require.NoError(t, expect.Locator(more).ToBeHidden())
	openMore(t, page)
	require.NoError(t, page.Keyboard().Press("Escape"))
	require.NoError(t, expect.Locator(more).ToBeHidden())
	require.NoError(t, expect.Locator(page.Locator("details.nav-more summary")).ToBeFocused())

	// A page in the menu highlights both its entry and the More button.
	openMore(t, page)
	require.NoError(t, more.Locator("a", playwright.LocatorLocatorOptions{HasText: "Recurring"}).Click())
	require.NoError(t, expect.Page(page).ToHaveURL(regexpFor(`/recurring$`)))
	require.NoError(t, expect.Locator(page.Locator("details.nav-more summary")).ToHaveClass(regexpFor(`\bactive\b`)))
	require.NoError(t, expect.Locator(page.Locator(`details.nav-more a[href="/recurring"]`)).ToHaveAttribute("aria-current", "page"))

	// Home goes back to the entry form.
	require.NoError(t, nav.Locator(`a[href="/"]`).Click())
	require.NoError(t, expect.Locator(page.Locator("#qa-form")).ToBeVisible())
}
