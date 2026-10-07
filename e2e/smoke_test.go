//go:build e2e

package e2e

import (
	"testing"

	"github.com/mxschmitt/playwright-go"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/testutil"
)

func TestSmoke(t *testing.T) {
	srv := testutil.NewServer(t)
	page := newPage(t)

	_, err := page.Goto(srv.URL + "/")
	require.NoError(t, err)

	title, err := page.Title()
	require.NoError(t, err)
	require.Equal(t, "vinance", title)

	// htmx loaded and is usable in the browser.
	v, err := page.Evaluate(`typeof htmx !== "undefined" && htmx.version`)
	require.NoError(t, err)
	require.Equal(t, "2.0.11", v)
}

func TestSmokeMobileViewport(t *testing.T) {
	srv := testutil.NewServer(t)
	page := newPage(t, playwright.BrowserNewContextOptions{
		Viewport: &playwright.Size{Width: 390, Height: 844},
	})
	_, err := page.Goto(srv.URL + "/")
	require.NoError(t, err)

	// No horizontal scrolling at phone width.
	overflow, err := page.Evaluate(`document.documentElement.scrollWidth > document.documentElement.clientWidth`)
	require.NoError(t, err)
	require.Equal(t, false, overflow)
}
