//go:build e2e

package e2e

import (
	"database/sql"
	"net/http/httptest"
	"testing"

	"github.com/mxschmitt/playwright-go"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/testutil"
)

var expect = playwright.NewPlaywrightAssertions(5000)

// settled waits for htmx to finish wiring freshly swapped content (typing right after a swap can be lost).
func settled(t *testing.T, page playwright.Page) {
	t.Helper()
	require.NoError(t, expect.Locator(page.Locator(".htmx-added")).ToHaveCount(0))
}

// entry is what a person fills in on the form. Empty fields are left as they are.
type entry struct{ kind, account, to, direction, description, amount, tags, date string }

// selectValue chooses an option of a named select in the entry form.
func selectValue(t *testing.T, page playwright.Page, name, value string) {
	t.Helper()
	_, err := page.Locator("select[name=" + name + "]").SelectOption(playwright.SelectOptionValues{Values: &[]string{value}})
	require.NoError(t, err)
}

// fill enters the fields and submits with Enter in the amount box.
func fill(t *testing.T, page playwright.Page, e entry) {
	t.Helper()
	settled(t, page)
	if e.kind != "" {
		selectValue(t, page, "kind", e.kind)
	}
	pick := func(name, value string) {
		if value != "" {
			_, err := page.Locator("select[name=" + name + "]").SelectOption(playwright.SelectOptionValues{Values: &[]string{value}})
			require.NoError(t, err)
		}
	}
	pick("account", e.account)
	pick("to", e.to)
	if e.direction != "" {
		selectValue(t, page, "direction", e.direction)
	}
	if e.date != "" {
		require.NoError(t, page.Locator("#qa-date").Fill(e.date))
	}
	if e.tags != "" {
		require.NoError(t, page.Locator("#qa-tags").Fill(e.tags))
	}
	require.NoError(t, page.Locator("#qa-description").Fill(e.description))
	require.NoError(t, page.Locator("#qa-amount").Fill(e.amount))
	require.NoError(t, page.Locator("#qa-amount").Press("Enter"))
}

// quickAdd records a plain expense on the home page.
func quickAdd(t *testing.T, page playwright.Page, account, description, amount string) {
	t.Helper()
	fill(t, page, entry{account: account, description: description, amount: amount})
}

func TestQuickAddJourney(t *testing.T) {
	srv, conn := testutil.NewServerWithDB(t)
	testutil.SeedDefaultAccounts(t, conn)
	page := newPage(t)
	_, err := page.Goto(srv.URL + "/")
	require.NoError(t, err)

	// Description is focused; the form starts as a Spend on the first account.
	require.NoError(t, expect.Locator(page.Locator("#qa-description")).ToBeFocused())
	fill(t, page, entry{account: "brisk", description: "McDonald's", amount: "12.5", tags: "#fast-food"})

	// The row appears at the top; the form restarts with the same account, focused and empty.
	row := page.Locator("#recent li.txn").First()
	require.NoError(t, expect.Locator(row).ToContainText("McDonald's"))
	require.NoError(t, expect.Locator(row.Locator(".amount")).ToHaveText("-$12.50"))
	require.NoError(t, expect.Locator(page.Locator(".flash")).ToContainText("Added McDonald's"))
	require.NoError(t, expect.Locator(page.Locator("#qa-account")).ToHaveValue("brisk"))
	require.NoError(t, expect.Locator(page.Locator("#qa-description")).ToHaveValue(""))
	require.NoError(t, expect.Locator(page.Locator("#qa-description")).ToBeFocused(), "ready for the next entry")

	// The tag just used is suggested for the next one.
	settled(t, page)
	require.NoError(t, page.Locator("#qa-tags").Fill("#fa"))
	tag := page.Locator("#qa-tag-suggestions button")
	require.NoError(t, expect.Locator(tag).ToHaveText("#fast-food"))
	require.NoError(t, tag.Click())
	require.NoError(t, expect.Locator(page.Locator("#qa-tags")).ToHaveValue("#fast-food "))
}

func TestQuickAddShowsErrorsAndKeepsEntries(t *testing.T) {
	srv, conn := testutil.NewServerWithDB(t)
	testutil.SeedDefaultAccounts(t, conn)
	page := newPage(t)
	_, err := page.Goto(srv.URL + "/")
	require.NoError(t, err)

	// A transfer to the same account is refused (422 swapped in) and what was typed is kept.
	fill(t, page, entry{kind: "transfer", account: "brisk", to: "brisk", description: "Oops", amount: "5"})
	require.NoError(t, expect.Locator(page.Locator(".problems")).ToContainText("Choose two different accounts"))
	require.NoError(t, expect.Locator(page.Locator("#qa-description")).ToHaveValue("Oops"))
	require.NoError(t, expect.Locator(page.Locator("#qa-amount")).ToHaveValue("5"))
	require.NoError(t, expect.Locator(page.Locator("#recent li.txn")).ToHaveCount(0))

	// Fixing it and resubmitting works.
	fill(t, page, entry{to: "wharf-bank", description: "Oops", amount: "5"})
	require.NoError(t, expect.Locator(page.Locator("#recent li.txn")).ToHaveCount(1))
	require.NoError(t, expect.Locator(page.Locator(".problems")).ToHaveCount(0))
}

func TestQuickAddFitsPhoneScreen(t *testing.T) {
	srv, conn := testutil.NewServerWithDB(t)
	testutil.SeedDefaultAccounts(t, conn)
	page := newPage(t, playwright.BrowserNewContextOptions{Viewport: &playwright.Size{Width: 390, Height: 844}})
	_, err := page.Goto(srv.URL + "/")
	require.NoError(t, err)

	overflow := `document.documentElement.scrollWidth > document.documentElement.clientWidth`
	got, err := page.Evaluate(overflow)
	require.NoError(t, err)
	require.Equal(t, false, got, "the empty form fits")

	selectValue(t, page, "kind", "transfer")
	fill(t, page, entry{account: "brisk", to: "wharf-bank", tags: "#one #two #three #four #five",
		description: "an extremely long description that keeps going and going", amount: "12.50"})
	require.NoError(t, expect.Locator(page.Locator("#recent li.txn")).ToHaveCount(1))
	got, err = page.Evaluate(overflow)
	require.NoError(t, err)
	require.Equal(t, false, got, "no horizontal scrolling on a phone")
}

// TestPastTransactionAutocomplete is the day-to-day flow for recurring-but-varying entries:
// type a few letters, Tab to complete the WHOLE form from history, type over the selected amount, Enter.
func TestPastTransactionAutocomplete(t *testing.T) {
	srv, conn := newServerWithBilt(t)
	testutil.SeedSpend(t, conn, "2026-09-01", "Maple One", 50000, "rent-share")
	testutil.SeedSpend(t, conn, "2026-09-02", "Dairy Queen", 1250, "fast-food")
	testutil.SeedIncome(t, conn, "2026-09-10", "FNDXX Dividends", 4520, "dividends")
	page := newPage(t)
	_, err := page.Goto(srv.URL + "/")
	require.NoError(t, err)
	settled(t, page)
	require.NoError(t, expect.Locator(page.Locator("#qa-description")).ToBeFocused())

	// Three letters bring up the past transaction; Tab fills every field and selects the amount.
	require.NoError(t, page.Keyboard().Type("fnd"))
	option := page.Locator(".desc-suggestion")
	require.NoError(t, expect.Locator(option).ToHaveCount(1))
	require.NoError(t, expect.Locator(option).ToContainText("FNDXX Dividends"))
	require.NoError(t, expect.Locator(option.Locator(".d-detail")).ToContainText("+$45.20"))
	require.NoError(t, page.Keyboard().Press("Tab"))
	require.NoError(t, expect.Locator(page.Locator("#qa-description")).ToHaveValue("FNDXX Dividends"))
	require.NoError(t, expect.Locator(page.Locator("select[name=kind]")).ToHaveValue("income"))
	require.NoError(t, expect.Locator(page.Locator("#qa-account")).ToHaveValue("wharf-bank"), "into the account it always goes to")
	require.NoError(t, expect.Locator(page.Locator("#qa-amount")).ToHaveValue("45.20"))
	require.NoError(t, expect.Locator(page.Locator("#qa-tags")).ToHaveValue("#dividends"))
	require.NoError(t, expect.Locator(page.Locator("#qa-amount")).ToBeFocused(), "Tab moves on to the amount")
	selected, err := page.Evaluate(`(() => { const i = document.getElementById('qa-amount'); return i.value.slice(i.selectionStart, i.selectionEnd); })()`)
	require.NoError(t, err)
	require.Equal(t, "45.20", selected, "just the amount is selected")
	require.NoError(t, expect.Locator(option).ToHaveCount(0), "no list while the amount is being edited")

	// Typing replaces the selected amount; Enter submits.
	require.NoError(t, page.Keyboard().Type("52.10"))
	require.NoError(t, page.Keyboard().Press("Enter"))
	row := page.Locator("#recent li.txn").First()
	require.NoError(t, expect.Locator(row.Locator(".amount")).ToHaveText("+$52.10"))
	require.NoError(t, expect.Locator(page.Locator("#qa-account")).ToHaveValue("wharf-bank"), "the account carries over to the next entry")
	settled(t, page)

	// Arrow keys choose, Enter accepts the chosen one instead of submitting, Escape dismisses.
	require.NoError(t, page.Locator("#qa-description").Fill("dairy"))
	require.NoError(t, expect.Locator(option).ToHaveCount(1))
	require.NoError(t, page.Keyboard().Press("ArrowDown"))
	require.NoError(t, expect.Locator(option.First()).ToHaveAttribute("aria-selected", "true"))
	require.NoError(t, page.Keyboard().Press("Enter"))
	require.NoError(t, expect.Locator(page.Locator("#qa-description")).ToHaveValue("Dairy Queen"))
	require.NoError(t, expect.Locator(page.Locator("select[name=kind]")).ToHaveValue("expense"), "back to Spend")
	require.NoError(t, expect.Locator(page.Locator("#qa-account")).ToHaveValue("brisk"), "the history's account replaces the carried-over one")
	require.NoError(t, expect.Locator(page.Locator("#qa-amount")).ToHaveValue("12.50"))
	require.NoError(t, expect.Locator(page.Locator("#recent li.txn")).ToHaveCount(4), "accepting did not submit (3 seeded + 1 added)")

	require.NoError(t, page.Locator("#qa-description").Fill("mapl"))
	require.NoError(t, expect.Locator(option).ToHaveCount(1))
	require.NoError(t, page.Locator("#qa-description").Press("Escape"))
	require.NoError(t, expect.Locator(option).ToHaveCount(0))

	// Tapping (clicking) a suggestion works too, as on a phone.
	require.NoError(t, page.Locator("#qa-tags").Fill("")) // (tags are only filled in when the field is empty)
	require.NoError(t, page.Locator("#qa-description").Fill("maple"))
	require.NoError(t, expect.Locator(option).ToHaveCount(1))
	require.NoError(t, option.Click())
	require.NoError(t, expect.Locator(page.Locator("#qa-description")).ToHaveValue("Maple One"))
	require.NoError(t, expect.Locator(page.Locator("#qa-amount")).ToHaveValue("500.00"))
	require.NoError(t, expect.Locator(page.Locator("#qa-tags")).ToHaveValue("#rent-share"))
}

// TestDatePickerAndRememberedDate: no typing dates. The picker sets the day and it sticks.
func TestDatePickerAndRememberedDate(t *testing.T) {
	srv, _ := newServerWithBilt(t)
	page := newPage(t)
	_, err := page.Goto(srv.URL + "/")
	require.NoError(t, err)
	settled(t, page)

	date := page.Locator("#qa-date")
	require.NoError(t, expect.Locator(date).ToHaveValue("2026-10-01"), "starts on today")

	require.NoError(t, date.Fill("2026-09-30"))
	require.NoError(t, expect.Locator(date).ToHaveValue("2026-09-30"))
	require.NoError(t, date.Fill("2026-09-24"))
	require.NoError(t, expect.Locator(date).ToHaveValue("2026-09-24"))

	quickAdd(t, page, "wharf-bank", "Coffee beans", "12.50")
	require.NoError(t, expect.Locator(page.Locator("#recent li.txn")).ToHaveCount(1))
	require.NoError(t, expect.Locator(page.Locator("#qa-date")).ToHaveValue("2026-09-24"), "the day you are working through is kept")
	quickAdd(t, page, "", "Lunch", "14.20")
	require.NoError(t, expect.Locator(page.Locator("#recent li.txn")).ToHaveCount(2))

	// A reload keeps the memory; changing the day works as before.
	_, err = page.Reload()
	require.NoError(t, err)
	settled(t, page)
	require.NoError(t, expect.Locator(page.Locator("#qa-date")).ToHaveValue("2026-09-24"))
	require.NoError(t, page.Locator("#qa-date").Fill("2026-09-25"))
	quickAdd(t, page, "", "Dinner", "30")
	require.NoError(t, expect.Locator(page.Locator("#recent li.txn")).ToHaveCount(3))
	require.NoError(t, expect.Locator(page.Locator("#qa-date")).ToHaveValue("2026-09-25"))

	_, err = page.Goto(srv.URL + "/transactions")
	require.NoError(t, err)
	require.NoError(t, expect.Locator(page.Locator("li.txn .date")).ToHaveText([]string{"2026-09-25", "2026-09-24", "2026-09-24"}))
}

// newServerWithBilt starts a server with the default accounts seeded.
func newServerWithBilt(t *testing.T) (*httptest.Server, *sql.DB) {
	t.Helper()
	srv, conn := testutil.NewServerWithDB(t)
	testutil.SeedDefaultAccounts(t, conn)
	return srv, conn
}

// TestEntryFormIsTwoConsistentRows: the entry is always two rows, whatever the type. Row 1 chooses
// (type, account(s), date); row 2 types (description, amount, tags, +).
func TestEntryFormIsTwoConsistentRows(t *testing.T) {
	srv, _ := newServerWithBilt(t)
	for _, width := range []int{1000, 1280} {
		page := newPage(t, playwright.BrowserNewContextOptions{Viewport: &playwright.Size{Width: width, Height: 800}})
		_, err := page.Goto(srv.URL + "/")
		require.NoError(t, err)
		settled(t, page)
		for _, kind := range []string{"expense", "income", "transfer"} {
			selectValue(t, page, "kind", kind)
			rows := page.Locator(".entry-line")
			require.NoError(t, expect.Locator(rows).ToHaveCount(2))
			for i := 0; i < 2; i++ {
				box, err := rows.Nth(i).BoundingBox()
				require.NoError(t, err)
				require.Less(t, box.Height, 48.0, "%s row %d at %dpx wide is a single row", kind, i+1, width)
			}
			add, err := page.Locator("button.add").BoundingBox()
			require.NoError(t, err)
			desc, err := page.Locator("#qa-description").BoundingBox()
			require.NoError(t, err)
			require.InDelta(t, desc.Y, add.Y, 6, "+ sits on the typing row")
		}
	}
}

// Clicking on to the amount or tags without choosing a suggestion dismisses the list, so the tag
// buttons below it stay within reach.
func TestSuggestionsDismissedWhenMovingToAnotherField(t *testing.T) {
	srv, conn := newServerWithBilt(t)
	testutil.SeedSpend(t, conn, "2026-09-02", "Dairy Queen", 1250, "fast-food")
	page := newPage(t)
	_, err := page.Goto(srv.URL + "/")
	require.NoError(t, err)
	settled(t, page)
	option := page.Locator(".desc-suggestion")

	require.NoError(t, page.Locator("#qa-description").Fill("dairy"))
	require.NoError(t, expect.Locator(option).ToHaveCount(1))
	require.NoError(t, page.Locator("#qa-amount").Click())
	require.NoError(t, expect.Locator(option).ToHaveCount(0), "list closes when the amount is clicked")

	require.NoError(t, page.Locator("#qa-description").Fill("dairy q")) // (a changed value, or htmx skips the request)
	require.NoError(t, expect.Locator(option).ToHaveCount(1))
	require.NoError(t, page.Locator("#qa-tags").Click())
	require.NoError(t, expect.Locator(option).ToHaveCount(0), "list closes when the tags are clicked")
	// The description is left as typed.
	require.NoError(t, expect.Locator(page.Locator("#qa-description")).ToHaveValue("dairy q"))

	// A response that arrives after focus has moved on is dropped.
	require.NoError(t, page.Locator("#qa-description").Press("End"))
	require.NoError(t, page.Keyboard().Type("x"))
	require.NoError(t, page.Locator("#qa-amount").Click())
	page.WaitForTimeout(500)
	require.NoError(t, expect.Locator(option).ToHaveCount(0))
}

// Tab completes the tag being typed when exactly one tag is suggested; with several it moves on as usual.
func TestTabCompletesTheOnlyTagSuggestion(t *testing.T) {
	srv, conn := newServerWithBilt(t)
	testutil.SeedSpend(t, conn, "2026-09-02", "Dairy Queen", 1250, "fast-food")
	testutil.SeedSpend(t, conn, "2026-09-03", "Cafe", 400, "cafes", "cake")
	page := newPage(t)
	_, err := page.Goto(srv.URL + "/")
	require.NoError(t, err)
	settled(t, page)
	tags := page.Locator("#qa-tags")
	list := page.Locator("#qa-tag-suggestions button")

	require.NoError(t, tags.Fill("#fa"))
	require.NoError(t, expect.Locator(list).ToHaveCount(1))
	require.NoError(t, page.Keyboard().Press("Tab"))
	require.NoError(t, expect.Locator(tags).ToHaveValue("#fast-food "))
	require.NoError(t, expect.Locator(tags).ToBeFocused(), "Tab completed instead of moving on")

	// Two candidates: Tab does not guess, it moves focus on.
	require.NoError(t, tags.Fill("#ca"))
	require.NoError(t, expect.Locator(list).ToHaveCount(2))
	require.NoError(t, page.Keyboard().Press("Tab"))
	require.NoError(t, expect.Locator(tags).ToHaveValue("#ca"))
	require.NoError(t, expect.Locator(tags).Not().ToBeFocused())
}
