//go:build e2e

package e2e

import (
	"testing"

	"github.com/mxschmitt/playwright-go"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/testutil"
)

func TestAccountsPageLinksToTransactions(t *testing.T) {
	srv, conn := testutil.NewServerWithDB(t)
	testutil.SeedDefaultAccounts(t, conn)
	testutil.SeedSpend(t, conn, "2026-09-01", "Coffee", 450, "cafes")
	page := newPage(t, playwright.BrowserNewContextOptions{Viewport: &playwright.Size{Width: 390, Height: 844}})
	_, err := page.Goto(srv.URL + "/")
	require.NoError(t, err)

	require.NoError(t, page.Locator("header nav a", playwright.PageLocatorOptions{HasText: "Accounts"}).Click())
	require.NoError(t, expect.Page(page).ToHaveURL(regexpFor(`/accounts$`)))
	require.NoError(t, expect.Locator(page.Locator(".networth .figure")).ToHaveText("-$4.50"))
	require.NoError(t, expect.Locator(page.Locator("ul.balances li", playwright.PageLocatorOptions{HasText: "BRISK"}).Locator(".balance")).ToHaveText("$4.50"))

	require.NoError(t, page.Locator("ul.balances a.name", playwright.PageLocatorOptions{HasText: "BRISK"}).Click())
	require.NoError(t, expect.Page(page).ToHaveURL(regexpFor(`/transactions\?account=brisk$`)))
	require.NoError(t, expect.Locator(page.Locator("li.txn")).ToHaveCount(1))
	require.NoError(t, expect.Locator(page.Locator("select[name=account]")).ToHaveValue("brisk"))

	// Clicking into an account shows a register: its own change per row and the running balance.
	require.NoError(t, expect.Locator(page.Locator("p.account-header")).ToHaveText("BRISK · Owed $4.50"))
	require.NoError(t, expect.Locator(page.Locator("li.txn .line1 .amount")).ToHaveText("-$4.50"))
	require.NoError(t, expect.Locator(page.Locator("li.txn .running")).ToHaveText("Owed $4.50"))

	overflow, err := page.Evaluate(`document.documentElement.scrollWidth > document.documentElement.clientWidth`)
	require.NoError(t, err)
	require.Equal(t, false, overflow)
}

func TestAccountTicksAreVisualOnly(t *testing.T) {
	srv, conn := newServerWithBilt(t)
	testutil.SeedSpend(t, conn, "2026-09-01", "Coffee", 450, "cafes")
	page := newPage(t)
	_, err := page.Goto(srv.URL + "/accounts")
	require.NoError(t, err)

	row := page.Locator("ul.balances li", playwright.PageLocatorOptions{HasText: "BRISK"})
	tick := row.Locator("input.tick")
	require.NoError(t, tick.Check())
	require.NoError(t, expect.Locator(tick).ToBeChecked())
	decoration, err := row.Locator("a.name").Evaluate(`el => getComputedStyle(el).textDecorationLine`, nil)
	require.NoError(t, err)
	require.Equal(t, "line-through", decoration, "a ticked account is struck through")

	// Ticks are not remembered: a reload clears them.
	_, err = page.Reload()
	require.NoError(t, err)
	require.NoError(t, expect.Locator(page.Locator("ul.balances input.tick:checked")).ToHaveCount(0))
}

// TestAddFromAnAccountsRegister: the quick-add bar on a register is the home page's bar, locked to the account.
func TestAddFromAnAccountsRegister(t *testing.T) {
	srv, conn := newServerWithBilt(t)
	testutil.SeedIncome(t, conn, "2026-09-10", "FNDXX Dividends", 4520, "dividends")
	testutil.SeedSpend(t, conn, "2026-09-11", "Coffee", 450, "cafes")
	page := newPage(t, playwright.BrowserNewContextOptions{Viewport: &playwright.Size{Width: 390, Height: 844}})
	_, err := page.Goto(srv.URL + "/accounts")
	require.NoError(t, err)
	require.NoError(t, page.Locator("ul.balances a.name", playwright.PageLocatorOptions{HasText: "Wharf Bank"}).Click())
	require.NoError(t, expect.Locator(page.Locator("p.account-header")).ToHaveText("Wharf Bank · Balance $45.20"))
	require.NoError(t, expect.Locator(page.Locator(".scoped-entry #qa-form")).ToHaveCount(1))

	// Same autocomplete as the home page: three letters, Tab fills the form, type over the selected amount.
	settled(t, page)
	require.NoError(t, expect.Locator(page.Locator("#qa-description")).ToBeFocused())
	require.NoError(t, page.Keyboard().Type("fnd"))
	require.NoError(t, expect.Locator(page.Locator(".desc-suggestion")).ToHaveCount(1))
	require.NoError(t, page.Keyboard().Press("Tab"))
	require.NoError(t, expect.Locator(page.Locator("select[name=kind]")).ToHaveValue("income"))
	require.NoError(t, expect.Locator(page.Locator("#qa-amount")).ToHaveValue("45.20"))
	require.NoError(t, expect.Locator(page.Locator("#qa-tags")).ToHaveValue("#dividends"))
	require.NoError(t, expect.Locator(page.Locator("#qa-account")).ToHaveCount(0), "the account is the register's: nothing to choose")
	require.NoError(t, page.Keyboard().Type("52.10"))
	require.NoError(t, page.Keyboard().Press("Enter"))

	// The page reloads: confirmation, new balance, the new row on top with its running balance.
	require.NoError(t, expect.Locator(page.Locator(".flash")).ToHaveText("Added FNDXX Dividends $52.10 (Wharf Bank)"))
	require.NoError(t, expect.Locator(page.Locator("p.account-header")).ToHaveText("Wharf Bank · Balance $97.30"))
	first := page.Locator("li.txn").First()
	require.NoError(t, expect.Locator(first.Locator(".line1 .amount")).ToHaveText("+$52.10"))
	require.NoError(t, expect.Locator(first.Locator(".running")).ToHaveText("Balance $97.30"))
	require.NoError(t, expect.Locator(page.Locator("#qa-description")).ToBeFocused())
	require.NoError(t, expect.Locator(page.Locator("#qa-description")).ToHaveValue(""))

	// A mistake (a transfer with nothing chosen is impossible here: no other account) is refused and nothing is added.
	settled(t, page)
	fill(t, page, entry{kind: "expense", description: "Oops", amount: "abc"})
	require.NoError(t, expect.Locator(page.Locator(".problems")).ToContainText("amount"))
	require.NoError(t, expect.Locator(page.Locator("#qa-description")).ToHaveValue("Oops"))
	require.NoError(t, expect.Locator(page.Locator("li.txn")).ToHaveCount(2))

	// A date picked once is remembered for the next entry, as on the home page.
	settled(t, page)
	fill(t, page, entry{description: "Gas", amount: "20", date: "2026-09-24"})
	require.NoError(t, expect.Locator(page.Locator(".flash")).ToContainText("Added Gas $20.00"))
	require.NoError(t, expect.Locator(page.Locator("#qa-date")).ToHaveValue("2026-09-24"))
	fill(t, page, entry{description: "Parking", amount: "7"})
	require.NoError(t, expect.Locator(page.Locator(".flash")).ToContainText("Added Parking $7.00"))
	require.NoError(t, expect.Locator(page.Locator("li.txn .date", playwright.PageLocatorOptions{HasText: "2026-09-24"})).ToHaveCount(2))

	overflow, err := page.Evaluate(`document.documentElement.scrollWidth > document.documentElement.clientWidth`)
	require.NoError(t, err)
	require.Equal(t, false, overflow)

	// The same form on a card's register adds to the card.
	_, err = page.Goto(srv.URL + "/transactions?account=brisk")
	require.NoError(t, err)
	require.NoError(t, expect.Locator(page.Locator(".scoped-entry #qa-form")).ToHaveCount(1))
	settled(t, page)
	fill(t, page, entry{description: "Snack", amount: "3"})
	require.NoError(t, expect.Locator(page.Locator("p.account-header")).ToHaveText("BRISK · Owed $7.50"))
}

// TestPayACardFromItsOwnRegister: "Neo Credit Card Payment 300.00 261003" typed on the card's page, naming the
// bank that pays it, is a transfer (bank -> card), not a charge.
func TestPayACardFromItsOwnRegister(t *testing.T) {
	srv, conn := testutil.NewServerWithDB(t)
	testutil.SeedAccount(t, conn, "Neo", "asset", "MXN")
	testutil.SeedAccount(t, conn, "Neo Credit", "liability", "MXN")
	page := newPage(t, playwright.BrowserNewContextOptions{Viewport: &playwright.Size{Width: 390, Height: 844}})

	// First a charge on the card, entered on its register: MX$1,000 owed.
	_, err := page.Goto(srv.URL + "/transactions?account=neo-credit")
	require.NoError(t, err)
	settled(t, page)
	fill(t, page, entry{description: "Groceries", amount: "1000", tags: "#groceries"})
	require.NoError(t, expect.Locator(page.Locator("p.account-header")).ToHaveText("Neo Credit · Owed MX$1,000.00"))

	// Transfer: pick the paying bank; "Into Neo Credit" is already chosen for a card.
	settled(t, page)
	selectValue(t, page, "kind", "transfer")
	require.NoError(t, expect.Locator(page.Locator("select[name=to] option")).ToHaveCount(1), "the card itself is not offered as the other account")
	require.NoError(t, expect.Locator(page.Locator("select[name=direction]")).ToHaveValue("in"))
	fill(t, page, entry{to: "neo", description: "Neo Credit Card Payment", amount: "300.00", date: "2026-10-03"})

	require.NoError(t, expect.Locator(page.Locator(".flash")).ToHaveText("Added Neo Credit Card Payment MX$300.00 (Neo → Neo Credit)"))
	require.NoError(t, expect.Locator(page.Locator("p.account-header")).ToHaveText("Neo Credit · Owed MX$700.00"))
	first := page.Locator("li.txn").First()
	require.NoError(t, expect.Locator(first.Locator(".line1 .amount")).ToHaveText("+MX$300.00"))
	require.NoError(t, expect.Locator(first.Locator(".running")).ToHaveText("Owed MX$700.00"))
	require.NoError(t, expect.Locator(first.Locator(".chip.account")).ToHaveText([]string{"Neo", "Neo Credit"}))

	// The bank's register shows the other half.
	_, err = page.Goto(srv.URL + "/transactions?account=neo")
	require.NoError(t, err)
	require.NoError(t, expect.Locator(page.Locator("li.txn .line1 .amount")).ToHaveText("-MX$300.00"))
	require.NoError(t, expect.Locator(page.Locator("p.account-header")).ToHaveText("Neo · Balance -MX$300.00"))
}
