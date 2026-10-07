//go:build e2e

// Package e2e runs browser tests against the real app with headless Chromium.
// One-time setup: make e2e-install
package e2e

import (
	"os"
	"regexp"
	"testing"

	"github.com/mxschmitt/playwright-go"
	"github.com/stretchr/testify/require"
)

var browser playwright.Browser

func TestMain(m *testing.M) {
	pw, err := playwright.Run()
	if err != nil {
		panic("playwright not installed? run `make e2e-install`: " + err.Error())
	}
	browser, err = pw.Chromium.Launch()
	if err != nil {
		panic("could not launch chromium (run `make e2e-install`): " + err.Error())
	}
	code := m.Run()
	_ = browser.Close()
	_ = pw.Stop()
	os.Exit(code)
}

// newPage returns a fresh page in its own browser context, closed when the test ends.
func newPage(t *testing.T, opts ...playwright.BrowserNewContextOptions) playwright.Page {
	t.Helper()
	ctx, err := browser.NewContext(opts...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ctx.Close() })
	page, err := ctx.NewPage()
	require.NoError(t, err)
	return page
}

// regexpFor compiles a URL-matching pattern for expect.Page(...).ToHaveURL.
func regexpFor(pattern string) *regexp.Regexp { return regexp.MustCompile(pattern) }
