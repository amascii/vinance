package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSafeNext(t *testing.T) {
	good := []string{"/", "/transactions", "/transactions?tag=a&from=2026-01-01", "/transactions/12?next=%2F"}
	for _, s := range good {
		assert.Equal(t, s, safeNext(s), s)
	}
	bad := []string{"", "evil", "//evil.example", "https://evil.example", "http://x", `/\evil`, "javascript:alert(1)", "/ok\r\nSet-Cookie: x=1", "/ok\nx"}
	for _, s := range bad {
		assert.Equal(t, "", safeNext(s), "%q", s)
	}
}

func TestParseEditFormOrdersLinesNumerically(t *testing.T) {
	v := url.Values{"date": {"2026-09-02"}, "description": {"x"}, "currency": {" usd "}, "next": {"//evil"}}
	for _, n := range []string{"10", "2", "0", "1"} {
		v.Set("r-"+n+"-memo", "m"+n)
		v.Set("r-"+n+"-amount", n)
	}
	v.Set("r-3-account", "7")
	v.Set("r-x-memo", "ignored")
	v.Set("r--1-memo", "ignored")
	v.Set("r-5000-memo", "ignored")
	v.Set("rows-memo", "ignored")
	v.Set("r-4", "ignored")
	req, err := http.NewRequest(http.MethodPost, "/", strings.NewReader(v.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	require.NoError(t, req.ParseForm())

	f := parseEditForm(req)
	assert.Equal(t, "USD", f.currency, "currency is trimmed and uppercased")
	assert.Equal(t, "", f.next, "an unsafe next is dropped")
	var memos []string
	for _, r := range f.rows {
		memos = append(memos, r.memo)
	}
	assert.Equal(t, []string{"m0", "m1", "m2", "", "m10"}, memos, "numeric order (10 after 3), junk keys ignored")
	assert.Equal(t, "7", f.rows[3].account, "line 3 only has an account but still counts as a line")
}

func TestParseTags(t *testing.T) {
	assert.Equal(t, []string{"snacks", "food-truck", "party"}, parseTags("#Snacks, #Food-Truck  party"))
	assert.Equal(t, []string{"snacks", "snacks"}, parseTags("snacks #snacks"), "dedupe happens in the ledger")
	assert.Nil(t, parseTags("  ,, # "))
}
