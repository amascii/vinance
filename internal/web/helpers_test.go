package web_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/db/gen"
	"github.com/amascii/vinance/internal/testutil"
)

// newApp starts a test server with the default accounts (BRISK, Wharf Bank, Neo) seeded.
func newApp(t *testing.T) (*httptest.Server, *sql.DB) {
	t.Helper()
	srv, conn := testutil.NewServerWithDB(t)
	testutil.SeedDefaultAccounts(t, conn)
	return srv, conn
}

func doc(t *testing.T, resp *http.Response) *goquery.Document {
	t.Helper()
	defer resp.Body.Close()
	d, err := goquery.NewDocumentFromReader(resp.Body)
	require.NoError(t, err)
	return d
}

func get(t *testing.T, srv *httptest.Server, path string) (*http.Response, *goquery.Document) {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	require.NoError(t, err)
	status := resp.StatusCode
	d := doc(t, resp)
	resp.StatusCode = status
	return resp, d
}

func post(t *testing.T, srv *httptest.Server, path string, form url.Values, headers ...string) (*http.Response, *goquery.Document) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	status := resp.StatusCode
	d := doc(t, resp)
	resp.StatusCode = status
	return resp, d
}

func count(t *testing.T, conn *sql.DB, table string) int {
	t.Helper()
	var n int
	require.NoError(t, conn.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&n))
	return n
}

func accountID(t *testing.T, conn *sql.DB, slug string) int64 {
	t.Helper()
	a, err := gen.New(conn).GetAccountBySlug(context.Background(), slug)
	require.NoError(t, err)
	return a.ID
}

// getHTMX issues a GET the way htmx does (HX-Request: true).
func getHTMX(t *testing.T, srv *httptest.Server, path string) (*http.Response, *goquery.Document) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	require.NoError(t, err)
	req.Header.Set("HX-Request", "true")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	status := resp.StatusCode
	d := doc(t, resp)
	resp.StatusCode = status
	return resp, d
}

// noRedirectClient lets tests inspect redirect responses.
var noRedirectClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// postRaw posts a form without following redirects and returns the response (body unread).
func postRaw(t *testing.T, srv *httptest.Server, path string, form url.Values, headers ...string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := noRedirectClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// formValues collects what a browser would submit for the form matched by sel: every named
// input (by value attribute), the selected option of each select, and textarea text.
func formValues(t *testing.T, d *goquery.Document, sel string) url.Values {
	t.Helper()
	form := d.Find(sel)
	require.Equal(t, 1, form.Length(), "form %s", sel)
	v := url.Values{}
	form.Find("input[name]").Each(func(_ int, s *goquery.Selection) {
		name, _ := s.Attr("name")
		typ, _ := s.Attr("type")
		if typ == "checkbox" || typ == "radio" {
			if _, on := s.Attr("checked"); !on {
				return
			}
		}
		val, _ := s.Attr("value")
		v.Add(name, val)
	})
	form.Find("select[name]").Each(func(_ int, s *goquery.Selection) {
		name, _ := s.Attr("name")
		opt := s.Find("option[selected]").First()
		if opt.Length() == 0 {
			opt = s.Find("option").First()
		}
		val, _ := opt.Attr("value")
		v.Add(name, val)
	})
	form.Find("textarea[name]").Each(func(_ int, s *goquery.Selection) {
		name, _ := s.Attr("name")
		v.Add(name, s.Text())
	})
	return v
}

func stringsReader(s string) *strings.Reader { return strings.NewReader(s) }
