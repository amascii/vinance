//go:build e2e

package e2e

import (
	"testing"

	"github.com/mxschmitt/playwright-go"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/testutil"
)

// TestSplitAReceiptInTheBrowser is the headline workflow: quick-add the statement line, then
// break it into tagged items, with the live indicator guiding to zero.
func TestSplitAReceiptInTheBrowser(t *testing.T) {
	srv, conn := testutil.NewServerWithDB(t)
	testutil.SeedDefaultAccounts(t, conn)
	page := newPage(t, playwright.BrowserNewContextOptions{Viewport: &playwright.Size{Width: 390, Height: 900}})
	_, err := page.Goto(srv.URL + "/")
	require.NoError(t, err)

	// 1. Record the statement line the fast way.
	quickAdd(t, page, "brisk", "Walmart", "65")
	require.NoError(t, expect.Locator(page.Locator("#recent li.txn")).ToHaveCount(1))

	// 2. Open it in the editor.
	require.NoError(t, page.Locator("#recent li.txn a.edit-link").Click())
	require.NoError(t, expect.Locator(page.Locator("h2")).ToHaveText("Edit transaction"))
	lines := page.Locator("#editor li.line")
	require.NoError(t, expect.Locator(lines).ToHaveCount(2))
	require.NoError(t, expect.Locator(page.Locator("#remaining")).ToContainText("Balanced"))
	require.NoError(t, expect.Locator(page.Locator("#save")).ToBeEnabled())

	// 3. Shrink the first item to $10: the live indicator says $55 is left, and Save is blocked.
	require.NoError(t, lines.Nth(1).Locator("input[name$=amount]").Fill("10.00"))
	require.NoError(t, lines.Nth(1).Locator("input[name$=memo]").Fill("Sodas"))
	require.NoError(t, lines.Nth(1).Locator("input[name$=tags]").Fill("#drinks"))
	require.NoError(t, expect.Locator(page.Locator("#remaining")).ToContainText("$55.00 left to allocate"))
	require.NoError(t, expect.Locator(page.Locator("#save")).ToBeDisabled())

	// 4. "+ Add line" fills in the remainder; trim it to $20 for the next item.
	require.NoError(t, page.Locator("button.add-line").Click())
	require.NoError(t, expect.Locator(lines).ToHaveCount(3))
	require.NoError(t, expect.Locator(lines.Nth(2).Locator("input[name$=amount]")).ToHaveValue("55.00"))
	require.NoError(t, expect.Locator(page.Locator("#remaining")).ToContainText("Balanced"), "the new line absorbs the remainder")
	settled(t, page)
	require.NoError(t, lines.Nth(2).Locator("input[name$=amount]").Fill("20.00"))
	require.NoError(t, lines.Nth(2).Locator("input[name$=memo]").Fill("Chips"))
	require.NoError(t, lines.Nth(2).Locator("input[name$=tags]").Fill("#snacks"))
	require.NoError(t, expect.Locator(page.Locator("#remaining")).ToContainText("$35.00 left to allocate"))

	// 5. One more line takes the last $35.
	require.NoError(t, page.Locator("button.add-line").Click())
	require.NoError(t, expect.Locator(lines).ToHaveCount(4))
	require.NoError(t, expect.Locator(lines.Nth(3).Locator("input[name$=amount]")).ToHaveValue("35.00"))
	require.NoError(t, lines.Nth(3).Locator("input[name$=memo]").Fill("Notebook"))
	require.NoError(t, lines.Nth(3).Locator("input[name$=tags]").Fill("#stationary"))
	// Earlier edits survived the re-render.
	require.NoError(t, expect.Locator(lines.Nth(1).Locator("input[name$=memo]")).ToHaveValue("Sodas"))
	require.NoError(t, expect.Locator(page.Locator("#remaining")).ToContainText("Balanced"))
	require.NoError(t, expect.Locator(page.Locator("#save")).ToBeEnabled())

	// 6. Save returns to the home list, where the receipt now shows its four lines.
	require.NoError(t, page.Locator("#save").Click())
	require.NoError(t, expect.Page(page).ToHaveURL(regexpFor(`/$`)))
	row := page.Locator("#recent li.txn").First()
	require.NoError(t, expect.Locator(row.Locator(".line1 .amount")).ToHaveText("-$65.00"))
	require.NoError(t, expect.Locator(row.Locator(".line2")).ToContainText("4 lines"))
	require.NoError(t, row.Locator("summary").Click())
	require.NoError(t, expect.Locator(row.Locator("ul.splits")).ToContainText("Notebook"))
	require.NoError(t, expect.Locator(row.Locator("ul.splits")).ToContainText("#stationary"))
}

func TestEditorDeleteAsksFirst(t *testing.T) {
	srv, conn := testutil.NewServerWithDB(t)
	testutil.SeedDefaultAccounts(t, conn)
	testutil.SeedSpend(t, conn, "2026-09-01", "Doomed", 500, "snack")
	testutil.SeedSpend(t, conn, "2026-09-02", "Survivor", 700, "snack")
	page := newPage(t)
	_, err := page.Goto(srv.URL + "/transactions")
	require.NoError(t, err)

	require.NoError(t, page.Locator("li.txn", playwright.PageLocatorOptions{HasText: "Doomed"}).Locator("a.edit-link").Click())
	require.NoError(t, expect.Locator(page.Locator("input[name=description]")).ToHaveValue("Doomed"))

	// Declining the confirmation keeps the transaction.
	var asked string
	page.Once("dialog", func(d playwright.Dialog) { asked = d.Message(); _ = d.Dismiss() })
	require.NoError(t, page.Locator("button.delete").Click())
	require.Eventually(t, func() bool { return asked != "" }, 3_000_000_000, 50_000_000)
	require.Contains(t, asked, "Delete this transaction")
	require.NoError(t, expect.Locator(page.Locator("input[name=description]")).ToHaveValue("Doomed"))

	// Accepting deletes it and returns to the list it came from.
	page.Once("dialog", func(d playwright.Dialog) { _ = d.Accept() })
	require.NoError(t, page.Locator("button.delete").Click())
	require.NoError(t, expect.Page(page).ToHaveURL(regexpFor(`/transactions$`)))
	require.NoError(t, expect.Locator(page.Locator("li.txn")).ToHaveCount(1))
	require.NoError(t, expect.Locator(page.Locator("li.txn")).ToContainText("Survivor"))
}

func TestEditorFixesImbalanceFromTheNeedsFixingList(t *testing.T) {
	srv, conn := testutil.NewServerWithDB(t)
	testutil.SeedDefaultAccounts(t, conn)
	testutil.SeedImbalanced(t, conn, "2026-06-17", "Clothing store", 12000)
	page := newPage(t)
	_, err := page.Goto(srv.URL + "/")
	require.NoError(t, err)

	// The home page points at what needs fixing.
	require.NoError(t, expect.Locator(page.Locator(".imbalance-banner")).ToContainText("1 transaction needs fixing"))
	require.NoError(t, page.Locator(".imbalance-banner a").Click())
	require.NoError(t, expect.Page(page).ToHaveURL(regexpFor(`/transactions\?imbalance=1$`)))

	require.NoError(t, expect.Locator(page.Locator("li.txn")).ToHaveCount(1))
	require.NoError(t, expect.Locator(page.Locator("li.txn .chip.warn")).ToHaveText("needs fixing"))
	require.NoError(t, page.Locator("li.txn a.edit-link").Click())
	require.NoError(t, expect.Locator(page.Locator(".notice.warn")).ToBeVisible())

	// Reassign the Imbalance line to Expenses and tag it.
	line := page.Locator("#editor li.line.warn")
	_, err = line.Locator("select[name$=account]").SelectOption(playwright.SelectOptionValues{Labels: &[]string{"Expenses"}})
	require.NoError(t, err)
	require.NoError(t, expect.Locator(page.Locator("#editor li.line.warn")).ToHaveCount(0), "the line stops being flagged after the account changes")
	require.NoError(t, page.Locator("#editor li.line").Nth(1).Locator("input[name$=tags]").Fill("#fashion #clothes"))
	require.NoError(t, page.Locator("#save").Click())

	// Back on the needs-fixing list: nothing left to fix.
	require.NoError(t, expect.Page(page).ToHaveURL(regexpFor(`/transactions\?imbalance=1$`)))
	require.NoError(t, expect.Locator(page.Locator("li.txn")).ToHaveCount(0))
	require.NoError(t, expect.Locator(page.Locator("p.empty")).ToBeVisible())

	// ...and the banner is gone from home.
	_, err = page.Goto(srv.URL + "/")
	require.NoError(t, err)
	require.NoError(t, expect.Locator(page.Locator(".imbalance-banner")).ToHaveCount(0))
}

// TestFixACardPaymentThatWasRecordedAsAnExpense is the real-life mix-up: a card payment typed on the
// card's register is a charge. The fix is to flip BOTH signs and point the other line at the bank.
func TestFixACardPaymentThatWasRecordedAsAnExpense(t *testing.T) {
	srv, conn := testutil.NewServerWithDB(t)
	testutil.SeedAccount(t, conn, "Bravo", "asset", "MXN")
	testutil.SeedAccount(t, conn, "Bravo Gold", "liability", "MXN")
	page := newPage(t, playwright.BrowserNewContextOptions{Viewport: &playwright.Size{Width: 390, Height: 900}})

	// Typed on the card's own register, "Pago Tdc 100.00" is a charge on the card (an expense).
	_, err := page.Goto(srv.URL + "/transactions?account=bravo-gold")
	require.NoError(t, err)
	settled(t, page)
	fill(t, page, entry{description: "Pago Tdc", amount: "100.00"})
	require.NoError(t, expect.Locator(page.Locator("p.account-header")).ToHaveText("Bravo Gold · Owed MX$100.00"))

	require.NoError(t, page.Locator("li.txn a.edit-link").Click())
	lines := page.Locator("#editor li.line")
	require.NoError(t, expect.Locator(lines.First().Locator("input[name$=amount]")).ToHaveValue("-100.00"))
	remaining := page.Locator("#remaining")

	// Pointing the second line at Bravo keeps it balanced, but the signs still describe a charge.
	_, err = lines.Nth(1).Locator("select[name$=account]").SelectOption(playwright.SelectOptionValues{Labels: &[]string{"Bravo"}})
	require.NoError(t, err)
	// Wait for the page to finish re-rendering (a real account shows its fixed currency, not a picker).
	require.NoError(t, expect.Locator(lines.Nth(1).Locator(".cur-label")).ToHaveText("MXN"))
	// ...and for htmx to wire up the fresh form (it marks new content "htmx-added" for ~20ms; no person types that fast).
	require.NoError(t, expect.Locator(page.Locator("#editor.htmx-added")).ToHaveCount(0))
	require.NoError(t, expect.Locator(remaining).ToContainText("Balanced"))

	// Flipping just the card's line (typing the + the hint mentions) leaves both lines positive...
	require.NoError(t, lines.First().Locator("input[name$=amount]").Fill("+100.00"))
	require.NoError(t, expect.Locator(remaining).ToContainText("Over-allocated by MX$200.00"))
	require.NoError(t, expect.Locator(page.Locator("#save")).ToBeDisabled())

	// ...Bravo's line must be negative (money leaving the bank). Then it balances and saves.
	require.NoError(t, lines.Nth(1).Locator("input[name$=amount]").Fill("-100.00"))
	require.NoError(t, expect.Locator(remaining).ToContainText("Balanced"))
	require.NoError(t, page.Locator("#save").Click())

	// Both registers now tell the right story.
	require.NoError(t, expect.Page(page).ToHaveURL(regexpFor(`/transactions\?account=bravo-gold$`)))
	require.NoError(t, expect.Locator(page.Locator("li.txn .line1 .amount")).ToHaveText("+MX$100.00"))
	require.NoError(t, expect.Locator(page.Locator("p.account-header")).ToHaveText("Bravo Gold · Owed -MX$100.00"))
	_, err = page.Goto(srv.URL + "/transactions?account=bravo")
	require.NoError(t, err)
	require.NoError(t, expect.Locator(page.Locator("li.txn .line1 .amount")).ToHaveText("-MX$100.00"))
	_, err = page.Goto(srv.URL + "/tags?range=all")
	require.NoError(t, err)
	require.NoError(t, expect.Locator(page.Locator(".networth .figure")).ToHaveText("$0.00"), "no longer counted as spending")
}

// TestEditorLinesAreOneRowEach: on a laptop-sized screen every split line is a single short row,
// so a whole receipt fits without scrolling, and "+ Add line" puts the cursor in the new line.
func TestEditorLinesAreOneRowEach(t *testing.T) {
	srv, conn := testutil.NewServerWithDB(t)
	testutil.SeedDefaultAccounts(t, conn)
	page := newPage(t, playwright.BrowserNewContextOptions{Viewport: &playwright.Size{Width: 1280, Height: 800}})
	_, err := page.Goto(srv.URL + "/")
	require.NoError(t, err)
	quickAdd(t, page, "brisk", "HEB", "15.59")
	require.NoError(t, expect.Locator(page.Locator("#recent li.txn")).ToHaveCount(1))
	require.NoError(t, page.Locator("#recent li.txn a.edit-link").Click())

	lines := page.Locator("#editor li.line")
	require.NoError(t, expect.Locator(lines).ToHaveCount(2))
	settled(t, page)
	for i := 0; i < 4; i++ {
		require.NoError(t, page.Locator("button.add-line").Click())
		require.NoError(t, expect.Locator(lines).ToHaveCount(3+i))
		require.NoError(t, expect.Locator(lines.Nth(2+i).Locator("input[name$=memo]")).ToBeFocused(), "the new line is ready to type in")
		settled(t, page)
	}

	// Six lines: each at most ~48px tall, and the Save button is still on screen.
	for i := 0; i < 6; i++ {
		box, err := lines.Nth(i).BoundingBox()
		require.NoError(t, err)
		require.Less(t, box.Height, 52.0, "line %d is a single row", i+1)
	}
	save, err := page.Locator("#save").BoundingBox()
	require.NoError(t, err)
	require.Less(t, save.Y+save.Height, 800.0, "six lines and Save fit in one 800px screen")
}

// TestEditorActionsAreOneRow: Save is the big button; Cancel (✕) and Delete (trash) are small icon buttons on the same row.
func TestEditorActionsAreOneRow(t *testing.T) {
	srv, conn := testutil.NewServerWithDB(t)
	testutil.SeedDefaultAccounts(t, conn)
	testutil.SeedSpend(t, conn, "2026-09-01", "Lunch", 500, "snack")
	page := newPage(t, playwright.BrowserNewContextOptions{Viewport: &playwright.Size{Width: 1280, Height: 800}})
	_, err := page.Goto(srv.URL + "/transactions")
	require.NoError(t, err)
	require.NoError(t, page.Locator("li.txn a.edit-link").Click())

	box := func(sel string) *playwright.Rect {
		b, err := page.Locator(sel).BoundingBox()
		require.NoError(t, err)
		return b
	}
	save, cancel, del := box("#save"), box("a.cancel"), box("button.delete")
	require.InDelta(t, save.Y, cancel.Y, 8, "Cancel is on Save's row")
	require.InDelta(t, save.Y, del.Y, 8, "Delete is on Save's row")
	require.Greater(t, save.Width, 3*cancel.Width, "Save is the biggest button")
	require.Less(t, cancel.Width, 60.0)
	require.Less(t, del.Width, 60.0)
	require.NoError(t, expect.Locator(page.Locator("a.cancel")).ToHaveAttribute("aria-label", "Cancel"))
	require.NoError(t, expect.Locator(page.Locator("button.delete")).ToHaveAttribute("aria-label", "Delete transaction"))
}

// TestEditorLineTagSuggestions: typing in one split line's tags offers known tags, and clicking one
// completes only that line.
func TestEditorLineTagSuggestions(t *testing.T) {
	srv, conn := testutil.NewServerWithDB(t)
	testutil.SeedDefaultAccounts(t, conn)
	testutil.SeedSpend(t, conn, "2026-08-01", "Old shop", 300, "groceries")
	testutil.SeedSpend(t, conn, "2026-09-01", "Target", 900, "household")
	page := newPage(t, playwright.BrowserNewContextOptions{Viewport: &playwright.Size{Width: 1280, Height: 800}})
	_, err := page.Goto(srv.URL + "/transactions")
	require.NoError(t, err)
	require.NoError(t, page.Locator("li.txn", playwright.PageLocatorOptions{HasText: "Target"}).Locator("a.edit-link").Click())
	lines := page.Locator("#editor li.line")
	require.NoError(t, expect.Locator(lines).ToHaveCount(2))
	settled(t, page)

	tags := lines.Nth(1).Locator("input[name$=tags]")
	require.NoError(t, tags.Fill("#household #gro"))
	chip := lines.Nth(1).Locator(".tag-suggestion")
	require.NoError(t, expect.Locator(chip).ToHaveText([]string{"#groceries"}))
	require.NoError(t, chip.Click())
	require.NoError(t, expect.Locator(tags).ToHaveValue("#household #groceries "))
	require.NoError(t, expect.Locator(lines.Nth(0).Locator("input[name$=tags]")).ToHaveValue(""), "the card line is untouched")
	require.NoError(t, expect.Locator(page.Locator("#save")).ToBeEnabled(), "tags don't affect the balance")
}
