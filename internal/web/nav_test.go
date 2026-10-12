package web_test

import (
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
)

func TestNavLinksAndMenu(t *testing.T) {
	srv, _ := newApp(t)
	_, d := get(t, srv, "/")
	nav := d.Find("header nav.site-nav")
	assert.Equal(t, "Home", nav.Find(`a[href="/"] .label`).Text(), "Home replaces the brand")
	assert.Equal(t, 0, nav.Find(".brand").Length())
	assert.Equal(t, 1, d.Find(`header nav.site-nav > ul > li > a[href="/accounts"]`).Length(), "Accounts is always visible")
	assert.Equal(t, 1, d.Find(`header nav.site-nav > ul > li > a[href="/transactions"]`).Length(), "Transactions is always visible")
	var menu []string
	nav.Find("details.nav-more ul a").Each(func(_ int, a *goquery.Selection) { menu = append(menu, a.AttrOr("href", "")) })
	assert.Equal(t, []string{"/budgets", "/recurring", "/tags", "/accounts/manage", "/tags/manage"}, menu)
	assert.Equal(t, 9, nav.Find("svg.icon").Length(), "every nav item has an icon")
}

func TestNavMarksTheCurrentPage(t *testing.T) {
	srv, _ := newApp(t)
	cases := []struct {
		path, current string
		more          bool
	}{
		{"/", "/", false},
		{"/accounts", "/accounts", false},
		{"/transactions", "/transactions", false},
		{"/budgets", "/budgets", true},
		{"/tags/manage", "/tags/manage", true},
	}
	for _, c := range cases {
		_, d := get(t, srv, c.path)
		cur := d.Find(`header nav [aria-current="page"]`)
		assert.Equal(t, 1, cur.Length(), c.path)
		assert.Equal(t, c.current, cur.AttrOr("href", ""), c.path)
		assert.Equal(t, c.more, d.Find("details.nav-more summary").HasClass("active"), c.path)
	}
}
