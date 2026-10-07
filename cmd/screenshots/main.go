// Command screenshots regenerates docs/screenshots from synthetic demo data.
//
// It builds a throwaway database in a temp dir (never data/vinance.db), serves the real app against it
// with a fixed clock, and drives headless Chromium. One-time setup: make e2e-install.
//
//	go run ./cmd/screenshots [outdir]
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/mxschmitt/playwright-go"

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

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: web.NewRouter(quiet, conn, web.WithClock(func() time.Time { return demo.Today }))}
	go srv.Serve(ln)
	defer srv.Close()
	base := "http://" + ln.Addr().String()

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
	return nil
}
