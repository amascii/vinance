package web_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/testutil"
)

func TestHomePage(t *testing.T) {
	srv := testutil.NewServer(t)

	resp, err := http.Get(srv.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "vinance", doc.Find("title").Text())
	assert.Equal(t, 1, doc.Find("form#qa-form input#qa-description[name=description]").Length(), "quick-add input is on the home page")
	assert.Equal(t, 1, doc.Find(`script[src="/static/htmx.min.js"]`).Length())
}

func TestHealthz(t *testing.T) {
	srv := testutil.NewServer(t)

	resp, err := http.Get(srv.URL + "/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.JSONEq(t, `{"status":"ok"}`, string(body))
}

func TestHealthzReportsDBDown(t *testing.T) {
	srv, conn := testutil.NewServerWithDB(t)
	require.NoError(t, conn.Close())

	resp, err := http.Get(srv.URL + "/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

func TestStaticAssetsServed(t *testing.T) {
	srv := testutil.NewServer(t)
	for _, path := range []string{"/static/htmx.min.js", "/static/pico.min.css"} {
		resp, err := http.Get(srv.URL + path)
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode, path)
	}
}
