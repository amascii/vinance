//go:build e2e

package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/testutil"
)

func TestLoginJourney(t *testing.T) {
	srv := testutil.NewAuthServer(t)
	page := newPage(t)

	// Asking for a page while logged out lands on the login form, remembering where you were going.
	_, err := page.Goto(srv.URL + "/transactions")
	require.NoError(t, err)
	require.NoError(t, expect.Page(page).ToHaveURL(regexpFor(`/login\?next=%2Ftransactions$`)))
	require.NoError(t, expect.Locator(page.Locator("header nav")).ToHaveCount(0))

	// A wrong password says so and keeps you on the form.
	require.NoError(t, page.Locator("input[name=password]").Fill("not it"))
	require.NoError(t, page.Locator("button[type=submit]").Click())
	require.NoError(t, expect.Locator(page.Locator("[role=alert]")).ToContainText("Wrong password"))

	// The right one goes on to the page you asked for.
	require.NoError(t, page.Locator("input[name=password]").Fill(testutil.Password))
	require.NoError(t, page.Locator("button[type=submit]").Click())
	require.NoError(t, expect.Page(page).ToHaveURL(regexpFor(`/transactions$`)))

	// Mid-session expiry: the next htmx request (a suggestion lookup) sends the whole page to login instead of
	// swapping a login form into the little suggestion box.
	_, err = page.Goto(srv.URL + "/")
	require.NoError(t, err)
	settled(t, page)
	require.NoError(t, page.Context().ClearCookies())
	require.NoError(t, page.Locator("#qa-description").PressSequentially("mi"))
	require.NoError(t, expect.Page(page).ToHaveURL(regexpFor(`/login$`)))

	// Log in again, then log out from the More menu.
	require.NoError(t, page.Locator("input[name=password]").Fill(testutil.Password))
	require.NoError(t, page.Locator("button[type=submit]").Click())
	require.NoError(t, expect.Page(page).ToHaveURL(regexpFor(`/$`)))
	openMore(t, page)
	require.NoError(t, page.Locator("details.nav-more button.logout-btn").Click())
	require.NoError(t, expect.Page(page).ToHaveURL(regexpFor(`/login$`)))

	// And the back button does not resurrect the page.
	_, err = page.GoBack()
	require.NoError(t, err)
	require.NoError(t, expect.Page(page).ToHaveURL(regexpFor(`/login`)))
}
