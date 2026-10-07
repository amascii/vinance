package web_test

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/ledger"
)

func tagRows(d *goquery.Document) map[string]string {
	m := map[string]string{}
	d.Find("ul.tag-list > li").Each(func(_ int, li *goquery.Selection) {
		m[li.Find(".tag-name").Text()] = rowTexts(li.Find(".tag-uses"))
	})
	return m
}

func tagOrder(d *goquery.Document) []string {
	var out []string
	d.Find("ul.tag-list .tag-name").Each(func(_ int, s *goquery.Selection) { out = append(out, s.Text()) })
	return out
}

func TestTagManageListsFiltersAndSorts(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	s.spend("2026-09-01", "a", 100, "snacks", "party")
	s.spend("2026-09-02", "b", 100, "snacks")
	s.spend("2026-09-03", "c", 100, "snacks", "snack-bar")
	s.spend("2026-09-04", "d", 100, "groceries")
	_, err := conn.Exec("INSERT INTO tags (name) VALUES ('unused')")
	require.NoError(t, err)

	resp, d := get(t, srv, "/tags/manage")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, []string{"#groceries", "#party", "#snack-bar", "#snacks", "#unused"}, tagOrder(d), "by name")
	assert.Equal(t, "3 lines", tagRows(d)["#snacks"])
	assert.Equal(t, "1 line", tagRows(d)["#party"])
	assert.Equal(t, "0 lines", tagRows(d)["#unused"])
	assert.Equal(t, "5 tags", rowTexts(d.Find("p.count")))
	href, _ := d.Find("ul.tag-list > li").First().Find("a.tag-name").Attr("href")
	assert.Equal(t, "/transactions?tag=groceries", href)

	_, d = get(t, srv, "/tags/manage?sort=uses")
	assert.Equal(t, "#snacks", tagOrder(d)[0], "most used first")

	_, d = get(t, srv, "/tags/manage?q=%23SNACK")
	assert.Equal(t, []string{"#snack-bar", "#snacks"}, tagOrder(d), "filter is case-insensitive and ignores a leading #")
	assert.Equal(t, "2 tags of 5", rowTexts(d.Find("p.count")))

	// Only unused tags offer deletion.
	_, d = get(t, srv, "/tags/manage")
	deletable := []string{}
	d.Find("ul.tag-list > li").Each(func(_ int, li *goquery.Selection) {
		if li.Find(`form[action="/tags/manage/delete"]`).Length() > 0 {
			deletable = append(deletable, li.Find(".tag-name").Text())
		}
	})
	assert.Equal(t, []string{"#unused"}, deletable)
}

func TestTagRenameInPlaceWithFlash(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	s.spend("2026-09-01", "a", 100, "accesories")

	resp := postRaw(t, srv, "/tags/manage/rename", url.Values{"from": {"accesories"}, "to": {"Accessories"}, "q": {"acc"}, "sort": {"uses"}})
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, "/tags/manage?q=acc&sort=uses", resp.Header.Get("Location"), "the filter state is kept")

	tags, err := ledger.NewService(conn).ListTags(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []ledger.TagInfo{{Name: "accessories", Splits: 1}}, tags)

	// The flash arrives once, via a cookie, on the page the redirect lands on.
	var flash *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "vinance_flash" {
			flash = c
		}
	}
	require.NotNil(t, flash)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/tags/manage", nil)
	req.AddCookie(flash)
	r2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	d := doc(t, r2)
	assert.Equal(t, "Renamed #accesories to #accessories.", d.Find(".flash").Text())
	cleared := false
	for _, c := range r2.Cookies() {
		cleared = cleared || (c.Name == "vinance_flash" && c.MaxAge < 0)
	}
	assert.True(t, cleared, "the flash is cleared after being shown")
	_, d = get(t, srv, "/tags/manage")
	assert.Equal(t, 0, d.Find(".flash").Length(), "and doesn't come back")
}

func TestTagMergeNeedsConfirmation(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	s.spend("2026-09-01", "a", 100, "ingredients")
	s.spend("2026-09-02", "b", 100, "ingredients")
	s.spend("2026-09-03", "c", 100, "groceries")

	// Step 1: renaming onto an existing tag asks first and changes nothing.
	resp, d := post(t, srv, "/tags/manage/rename", url.Values{"from": {"ingredients"}, "to": {"groceries"}})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	box := d.Find("form.merge-confirm")
	require.Equal(t, 1, box.Length())
	text := rowTexts(box.Find("p"))
	assert.Contains(t, text, "A tag named groceries already exists (1 line)")
	assert.Contains(t, text, "Merge ingredients (2 lines) into it?")
	tags, _ := ledger.NewService(conn).ListTags(context.Background())
	assert.Equal(t, []ledger.TagInfo{{Name: "groceries", Splits: 1}, {Name: "ingredients", Splits: 2}}, tags, "nothing merged yet")

	// Step 2: the confirmation form carries everything needed, and merges.
	v := formValues(t, d, "form.merge-confirm")
	assert.Equal(t, "1", v.Get("confirm"))
	assert.Equal(t, "ingredients", v.Get("from"))
	assert.Equal(t, "groceries", v.Get("to"))
	resp = postRaw(t, srv, "/tags/manage/rename", v)
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	tags, _ = ledger.NewService(conn).ListTags(context.Background())
	assert.Equal(t, []ledger.TagInfo{{Name: "groceries", Splits: 3}}, tags)
	flash := ""
	for _, c := range resp.Cookies() {
		if c.Name == "vinance_flash" {
			flash, _ = url.QueryUnescape(c.Value)
		}
	}
	assert.Equal(t, "Merged #ingredients into #groceries (2 lines).", flash)
}

func TestTagRenameValidation(t *testing.T) {
	srv, conn := newApp(t)
	newSeeder(t, conn).spend("2026-09-01", "a", 100, "snacks")

	resp, d := post(t, srv, "/tags/manage/rename", url.Values{"from": {"snacks"}, "to": {"  # "}})
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	assert.Contains(t, d.Find(".problems").Text(), "Enter a tag name")

	resp, d = post(t, srv, "/tags/manage/rename", url.Values{"from": {"ghost"}, "to": {"boo"}})
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Contains(t, d.Find(".problems").Text(), "no longer exists")

	// Renaming to itself (even differently formatted) is a quiet no-op.
	resp = postRaw(t, srv, "/tags/manage/rename", url.Values{"from": {"snacks"}, "to": {"#Snacks"}})
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)
	tags, _ := ledger.NewService(conn).ListTags(context.Background())
	assert.Equal(t, []ledger.TagInfo{{Name: "snacks", Splits: 1}}, tags)
}

func TestTagDelete(t *testing.T) {
	srv, conn := newApp(t)
	newSeeder(t, conn).spend("2026-09-01", "a", 100, "used")
	_, err := conn.Exec("INSERT INTO tags (name) VALUES ('unused')")
	require.NoError(t, err)

	resp, d := post(t, srv, "/tags/manage/delete", url.Values{"name": {"used"}})
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Contains(t, d.Find(".problems").Text(), "#used is still used by transactions")

	resp = postRaw(t, srv, "/tags/manage/delete", url.Values{"name": {"unused"}})
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)
	tags, _ := ledger.NewService(conn).ListTags(context.Background())
	assert.Equal(t, []ledger.TagInfo{{Name: "used", Splits: 1}}, tags)

	resp = postRaw(t, srv, "/tags/manage/delete", url.Values{"name": {"unused"}}) // already gone: harmless
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)
}

func TestTagManageRejectsCrossOrigin(t *testing.T) {
	srv, conn := newApp(t)
	newSeeder(t, conn).spend("2026-09-01", "a", 100, "snacks")
	resp := postRaw(t, srv, "/tags/manage/rename", url.Values{"from": {"snacks"}, "to": {"x"}}, "Sec-Fetch-Site", "cross-site")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	tags, _ := ledger.NewService(conn).ListTags(context.Background())
	assert.Equal(t, []ledger.TagInfo{{Name: "snacks", Splits: 1}}, tags)
}

func TestTagReportLinksToManagement(t *testing.T) {
	srv, _ := newApp(t)
	_, d := get(t, srv, "/tags")
	assert.Equal(t, 1, d.Find(`a[href="/tags/manage"]`).Length())
	_, d = get(t, srv, "/tags/manage")
	assert.Equal(t, 1, d.Find(`p.crumbs a[href="/tags"]`).Length())
}
