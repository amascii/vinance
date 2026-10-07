//go:build e2e

package e2e

import (
	"testing"

	"github.com/mxschmitt/playwright-go"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/testutil"
)

func TestAccountManagementJourney(t *testing.T) {
	srv, _ := testutil.NewServerWithDB(t) // no accounts yet: start from scratch like a new user
	page := newPage(t, playwright.BrowserNewContextOptions{Viewport: &playwright.Size{Width: 390, Height: 844}})
	_, err := page.Goto(srv.URL + "/accounts")
	require.NoError(t, err)
	require.NoError(t, expect.Locator(page.Locator(".networth .figure")).ToHaveText("$0.00"))

	// Add a checking account with money in it.
	require.NoError(t, page.Locator("a", playwright.PageLocatorOptions{HasText: "Add, rename or archive accounts"}).Click())
	require.NoError(t, page.Locator("details.add-account summary").Click())
	require.NoError(t, page.Locator("details.add-account input[name=name]").Fill("Metro Checking"))
	require.NoError(t, page.Locator("details.add-account input[name=opening]").Fill("1,200.00"))
	require.NoError(t, page.Locator("details.add-account button[type=submit]").Click())
	require.NoError(t, expect.Locator(page.Locator(".flash")).ToHaveText("Added Metro Checking (quick-add: @metro-checking)."))

	// A mistake is explained and the form keeps what was typed.
	require.NoError(t, page.Locator("details.add-account summary").Click())
	require.NoError(t, page.Locator("details.add-account input[name=name]").Fill("metro checking"))
	require.NoError(t, page.Locator("details.add-account button[type=submit]").Click())
	require.NoError(t, expect.Locator(page.Locator("p.problems").First()).ToContainText("already exists"))
	require.NoError(t, expect.Locator(page.Locator("details.add-account input[name=name]")).ToHaveValue("metro checking"))

	// Add a card too.
	require.NoError(t, page.Locator("details.add-account input[name=name]").Fill("Visa"))
	_, err = page.Locator("details.add-account select[name=type]").SelectOption(playwright.SelectOptionValues{Values: &[]string{"liability"}})
	require.NoError(t, err)
	require.NoError(t, page.Locator("details.add-account button[type=submit]").Click())
	require.NoError(t, expect.Locator(page.Locator(".flash")).ToContainText("Added Visa"))

	// The balances page shows the opening balance.
	_, err = page.Goto(srv.URL + "/accounts")
	require.NoError(t, err)
	require.NoError(t, expect.Locator(page.Locator(".networth .figure")).ToHaveText("$1,200.00"))

	// The new accounts work in quick-add right away.
	_, err = page.Goto(srv.URL + "/")
	require.NoError(t, err)
	require.NoError(t, expect.Locator(page.Locator("#qa-account option", playwright.PageLocatorOptions{HasText: "Visa"})).ToHaveCount(1))
	fill(t, page, entry{account: "visa", description: "Groceries", amount: "30", tags: "#food"})
	require.NoError(t, expect.Locator(page.Locator("#recent li.txn")).ToHaveCount(1))

	// Archive the card: it disappears from quick-add but keeps its history.
	_, err = page.Goto(srv.URL + "/accounts/manage")
	require.NoError(t, err)
	visa := page.Locator("ul.account-list > li", playwright.PageLocatorOptions{HasText: "Visa"})
	require.NoError(t, visa.Locator("summary").Click())
	require.NoError(t, expect.Locator(visa.Locator(`form[action$="/delete"]`)).ToHaveCount(0), "it has a transaction now, so no delete")
	require.NoError(t, visa.Locator("button", playwright.LocatorLocatorOptions{HasText: "Archive"}).Click())
	require.NoError(t, expect.Locator(page.Locator(".flash")).ToHaveText("Archived."))
	require.NoError(t, expect.Locator(page.Locator("h3", playwright.PageLocatorOptions{HasText: "Archived"})).ToBeVisible())

	_, err = page.Goto(srv.URL + "/")
	require.NoError(t, err)
	require.NoError(t, expect.Locator(page.Locator("#qa-account option", playwright.PageLocatorOptions{HasText: "Visa"})).ToHaveCount(0), "archived accounts can't be chosen")
	_, err = page.Goto(srv.URL + "/accounts")
	require.NoError(t, err)
	require.NoError(t, expect.Locator(page.Locator("ul.balances li", playwright.PageLocatorOptions{HasText: "Visa"}).Locator(".balance")).ToHaveText("$30.00"), "its balance still counts")
}

// The edit form is compact: Name, Type, Currency and Save sit on one line on a desktop.
func TestAccountEditFormIsCompact(t *testing.T) {
	srv, conn := testutil.NewServerWithDB(t)
	testutil.SeedAccount(t, conn, "Wharf Bank", "asset", "USD")
	page := newPage(t, playwright.BrowserNewContextOptions{Viewport: &playwright.Size{Width: 1200, Height: 800}})
	_, err := page.Goto(srv.URL + "/accounts/manage")
	require.NoError(t, err)
	row := page.Locator("ul.account-list > li").First()
	pencil, err := row.Locator("summary").BoundingBox()
	require.NoError(t, err)
	li, err := row.BoundingBox()
	require.NoError(t, err)
	require.InDelta(t, li.X+li.Width, pencil.X+pencil.Width, 4, "the edit pencil is at the right of the row")
	require.NoError(t, row.Locator("summary").Click())
	form := row.Locator("form.account-form")
	name, err := form.Locator("input[name=name]").BoundingBox()
	require.NoError(t, err)
	typ, err := form.Locator("select[name=type]").BoundingBox()
	require.NoError(t, err)
	cur, err := form.Locator("input[name=currency]").BoundingBox()
	require.NoError(t, err)
	save, err := form.Locator("button[type=submit]").BoundingBox()
	require.NoError(t, err)
	require.Less(t, name.Height, 40.0, "jumbo inputs are back")
	for _, b := range []*playwright.Rect{typ, cur, save} {
		require.InDelta(t, name.Y, b.Y, 6, "name, type, currency and save share a line")
	}
}

// Clicking the pencil focuses the name with its text selected, so typing replaces it.
func TestAccountEditSelectsName(t *testing.T) {
	srv, conn := testutil.NewServerWithDB(t)
	testutil.SeedAccount(t, conn, "Wharf Bank", "asset", "USD")
	page := newPage(t, playwright.BrowserNewContextOptions{Viewport: &playwright.Size{Width: 1200, Height: 800}})
	_, err := page.Goto(srv.URL + "/accounts/manage")
	require.NoError(t, err)
	row := page.Locator("ul.account-list > li").First()
	require.NoError(t, row.Locator("summary").Click())
	name := row.Locator("input[name=name]")
	require.NoError(t, expect.Locator(name).ToBeFocused())
	require.NoError(t, page.Keyboard().Type("bank"))
	require.NoError(t, expect.Locator(name).ToHaveValue("bank"))
}
