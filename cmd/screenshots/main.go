// Command screenshots regenerates docs/screenshots from synthetic demo data.
//
// It builds a throwaway database in a temp dir (never data/vinance.db), serves the real app against it
// with a fixed clock, and drives headless Chromium. One-time setup: make e2e-install.
//
//	go run ./cmd/screenshots [outdir]
package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/mxschmitt/playwright-go"
	"golang.org/x/crypto/bcrypt"

	"github.com/amascii/vinance/internal/db"
	"github.com/amascii/vinance/internal/demo"
	"github.com/amascii/vinance/internal/logging"
	"github.com/amascii/vinance/internal/web"
)

func main() {
	out := "docs/screenshots"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	if err := run(out); err != nil {
		logging.New(os.Stderr, slog.LevelInfo).Error("screenshots failed", "error", err)
		os.Exit(1)
	}
}

func run(out string) error {
	ctx := context.Background()
	quiet := logging.New(io.Discard, slog.LevelError)

	dir, err := os.MkdirTemp("", "vinance-demo-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	conn, err := db.Open(filepath.Join(dir, "demo.db"))
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := db.Migrate(ctx, conn, quiet); err != nil {
		return err
	}
	res, err := demo.Seed(ctx, conn)
	if err != nil {
		return err
	}

	base, closeSrv, err := serve(quiet, conn)
	if err != nil {
		return err
	}
	defer closeSrv()
	// The same data behind a login, for the login page and the nav's Log out entry.
	hash, err := bcrypt.GenerateFromPassword([]byte(demoPassword), bcrypt.MinCost)
	if err != nil {
		return err
	}
	authBase, closeAuth, err := serve(quiet, conn, web.WithAuth(string(hash), false))
	if err != nil {
		return err
	}
	defer closeAuth()

	pw, err := playwright.Run()
	if err != nil {
		return fmt.Errorf("playwright not installed? run `make e2e-install`: %w", err)
	}
	defer pw.Stop()
	browser, err := pw.Chromium.Launch()
	if err != nil {
		return err
	}
	defer browser.Close()
	bctx, err := browser.NewContext(playwright.BrowserNewContextOptions{
		Viewport:          &playwright.Size{Width: 1180, Height: 760},
		DeviceScaleFactor: playwright.Float(1.5),
	})
	if err != nil {
		return err
	}
	page, err := bctx.NewPage()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}

	shots := []struct {
		name, path string
		full       bool
		height     float64 // when set, crop a full-page shot to this many CSS pixels from the top
	}{
		{"home", "/", false, 0},
		{"transactions", "/transactions", false, 0},
		{"editor", fmt.Sprintf("/transactions/%d", res.SplitReceiptID), true, 0},
		{"accounts", "/accounts", true, 0},
		{"tags", "/tags", true, 0},
		{"budgets", "/budgets", true, 1010},
		{"recurring", "/recurring", true, 0},
	}
	for _, s := range shots {
		if _, err := page.Goto(base + s.path); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
		if err := page.WaitForLoadState(); err != nil {
			return err
		}
		time.Sleep(300 * time.Millisecond) // let htmx finish settling
		opts := playwright.PageScreenshotOptions{
			Path:     playwright.String(filepath.Join(out, s.name+".png")),
			FullPage: playwright.Bool(s.full),
		}
		if s.height > 0 {
			opts.Clip = &playwright.Rect{X: 0, Y: 0, Width: 1180, Height: s.height}
		}
		if _, err := page.Screenshot(opts); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}
	return phoneAuth(browser, authBase, out)
}

const demoPassword = "demo password"

// serve starts the real app on a free local port and returns its base URL and a stop function.
func serve(logger *slog.Logger, conn *sql.DB, opts ...web.Option) (string, func(), error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	opts = append(opts, web.WithClock(func() time.Time { return demo.Today }))
	srv := &http.Server{Handler: web.NewRouter(logger, conn, opts...)}
	go srv.Serve(ln)
	return "http://" + ln.Addr().String(), func() { srv.Close() }, nil
}

// phoneAuth captures, at phone width, the login page and then the nav with the More menu open (including Log out).
func phoneAuth(browser playwright.Browser, base, out string) error {
	bctx, err := browser.NewContext(playwright.BrowserNewContextOptions{
		Viewport:          &playwright.Size{Width: 390, Height: 844},
		DeviceScaleFactor: playwright.Float(2),
	})
	if err != nil {
		return err
	}
	defer bctx.Close()
	page, err := bctx.NewPage()
	if err != nil {
		return err
	}
	if _, err := page.Goto(base + "/budgets"); err != nil { // redirected to the login page
		return fmt.Errorf("login: %w", err)
	}
	if err := page.Locator("input[name=password]").Fill(demoPassword); err != nil {
		return fmt.Errorf("login: %w", err)
	}
	// Shot before submitting would show an empty field; show it filled but hidden dots are fine.
	time.Sleep(300 * time.Millisecond)
	if _, err := page.Screenshot(playwright.PageScreenshotOptions{
		Path: playwright.String(filepath.Join(out, "login.png")),
		Clip: &playwright.Rect{X: 0, Y: 0, Width: 390, Height: 420},
	}); err != nil {
		return err
	}
	if err := page.Locator("button[type=submit]").Click(); err != nil {
		return fmt.Errorf("nav-phone: %w", err)
	}
	if err := page.Locator("details.nav-more summary").Click(); err != nil {
		return fmt.Errorf("nav-phone: %w", err)
	}
	time.Sleep(300 * time.Millisecond)
	_, err = page.Screenshot(playwright.PageScreenshotOptions{
		Path: playwright.String(filepath.Join(out, "nav-phone.png")),
		Clip: &playwright.Rect{X: 0, Y: 0, Width: 390, Height: 470},
	})
	return err
}
