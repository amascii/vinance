package web_test

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/testutil"
)

// browser is a client that keeps cookies but does not follow redirects, so tests see each hop.
func browser(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	return &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func login(t *testing.T, c *http.Client, base, password, next string) *http.Response {
	t.Helper()
	resp, err := c.PostForm(base+"/login", url.Values{"password": {password}, "next": {next}})
	require.NoError(t, err)
	resp.Body.Close()
	return resp
}

func TestLoggedOutVisitorIsSentToLogin(t *testing.T) {
	srv := testutil.NewAuthServer(t)
	c := browser(t)

	for path, want := range map[string]string{
		"/":                    "/login",
		"/transactions?q=milk": "/login?next=%2Ftransactions%3Fq%3Dmilk",
		"/transactions/12":     "/login?next=%2Ftransactions%2F12",
		"/accounts/manage":     "/login?next=%2Faccounts%2Fmanage",
	} {
		resp, err := c.Get(srv.URL + path)
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusSeeOther, resp.StatusCode, path)
		assert.Equal(t, want, resp.Header.Get("Location"), path)
	}
}

func TestLoggedOutPostsChangeNothing(t *testing.T) {
	srv, conn := testutil.NewAuthServerWithDB(t)
	resp, err := browser(t).PostForm(srv.URL+"/accounts/manage/create", url.Values{"name": {"Sneaky"}, "type": {"asset"}, "currency": {"USD"}})
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, "/login", resp.Header.Get("Location"))

	var n int
	require.NoError(t, conn.QueryRow(`SELECT COUNT(*) FROM accounts WHERE name = 'Sneaky'`).Scan(&n))
	assert.Zero(t, n)
}

func TestHTMXRequestWithoutSessionRedirectsTheWholePage(t *testing.T) {
	srv := testutil.NewAuthServer(t)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/quickadd/suggest/description", strings.NewReader("description=mi"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Current-URL", srv.URL+"/transactions?q=milk")
	resp, err := browser(t).Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, "/login?next=%2Ftransactions%3Fq%3Dmilk", resp.Header.Get("HX-Redirect"), "returns to the page the user was on, not the fragment URL")
}

func TestLoginPageHasNoSiteNav(t *testing.T) {
	srv := testutil.NewAuthServer(t)
	resp, err := http.Get(srv.URL + "/login")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "private, no-store", resp.Header.Get("Cache-Control"))

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, 1, doc.Find(`form[method=post][action="/login"] input[type=password][name=password]`).Length())
	assert.Zero(t, doc.Find("nav").Length())
}

func TestWrongPasswordIsRefused(t *testing.T) {
	srv := testutil.NewAuthServer(t)
	c := browser(t)

	resp, err := c.PostForm(srv.URL+"/login", url.Values{"password": {"nope"}, "next": {"/budgets"}})
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, doc.Find("[role=alert]").Text(), "Wrong password")
	assert.Equal(t, "/budgets", doc.Find("input[name=next]").AttrOr("value", ""), "the destination survives a retry")

	home, err := c.Get(srv.URL + "/")
	require.NoError(t, err)
	home.Body.Close()
	assert.Equal(t, http.StatusSeeOther, home.StatusCode, "still logged out")
}

func TestLoginThenBrowseThenLogout(t *testing.T) {
	srv := testutil.NewAuthServer(t)
	c := browser(t)

	resp := login(t, c, srv.URL, testutil.Password, "/budgets")
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, "/budgets", resp.Header.Get("Location"))
	var cookie *http.Cookie
	for _, ck := range resp.Cookies() {
		if ck.Name == "vinance_session" {
			cookie = ck
		}
	}
	require.NotNil(t, cookie)
	assert.True(t, cookie.HttpOnly)
	assert.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
	assert.InDelta(t, 30*24*3600, cookie.MaxAge, 5, "the session lasts 30 days")

	page, err := c.Get(srv.URL + "/budgets")
	require.NoError(t, err)
	defer page.Body.Close()
	require.Equal(t, http.StatusOK, page.StatusCode)
	doc, err := goquery.NewDocumentFromReader(page.Body)
	require.NoError(t, err)
	assert.Equal(t, 1, doc.Find(`form[action="/logout"] button`).Length(), "the More menu offers Log out")

	// /login while logged in goes straight on.
	again, err := c.Get(srv.URL + "/login?next=/tags")
	require.NoError(t, err)
	again.Body.Close()
	assert.Equal(t, "/tags", again.Header.Get("Location"))

	out, err := c.PostForm(srv.URL+"/logout", nil)
	require.NoError(t, err)
	out.Body.Close()
	assert.Equal(t, "/login", out.Header.Get("Location"))

	after, err := c.Get(srv.URL + "/budgets")
	require.NoError(t, err)
	after.Body.Close()
	assert.Equal(t, http.StatusSeeOther, after.StatusCode, "the session is gone server-side")
}

func TestLoginRenewsTheSessionToken(t *testing.T) {
	srv := testutil.NewAuthServer(t)
	c := browser(t)
	// An attacker-planted pre-login cookie must not become the authenticated one.
	u, _ := url.Parse(srv.URL)
	c.Jar.SetCookies(u, []*http.Cookie{{Name: "vinance_session", Value: "planted"}})

	resp := login(t, c, srv.URL, testutil.Password, "/")
	for _, ck := range resp.Cookies() {
		if ck.Name == "vinance_session" {
			assert.NotEqual(t, "planted", ck.Value)
			return
		}
	}
	t.Fatal("no session cookie issued")
}

func TestNextCannotLeaveTheSite(t *testing.T) {
	srv := testutil.NewAuthServer(t)
	for _, next := range []string{"https://evil.example/", "//evil.example", `/\evil.example`, "evil", "/login", "/logout", ""} {
		resp := login(t, browser(t), srv.URL, testutil.Password, next)
		assert.Equal(t, "/", resp.Header.Get("Location"), "next=%q", next)
	}
}

func TestOpenPathsNeedNoSession(t *testing.T) {
	srv := testutil.NewAuthServer(t)
	for _, path := range []string{"/healthz", "/static/htmx.min.js", "/static/app.css", "/login"} {
		resp, err := browser(t).Get(srv.URL + path)
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode, path)
	}
}

func TestLoginIsRateLimited(t *testing.T) {
	srv := testutil.NewAuthServer(t)
	c := browser(t)
	var last int
	for range 12 {
		last = login(t, c, srv.URL, "wrong", "/").StatusCode
	}
	assert.Equal(t, http.StatusTooManyRequests, last)
	// Even the right password is refused while throttled.
	assert.Equal(t, http.StatusTooManyRequests, login(t, c, srv.URL, testutil.Password, "/").StatusCode)
}

func TestWithoutAuthThereIsNoLoginRoute(t *testing.T) {
	srv := testutil.NewServer(t)
	resp, err := http.Get(srv.URL + "/login")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	home, err := http.Get(srv.URL + "/")
	require.NoError(t, err)
	defer home.Body.Close()
	doc, err := goquery.NewDocumentFromReader(home.Body)
	require.NoError(t, err)
	assert.Zero(t, doc.Find(`form[action="/logout"]`).Length(), "no Log out when there is no login")
}
