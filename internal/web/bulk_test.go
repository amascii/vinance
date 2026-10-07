package web_test

import (
	"database/sql"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func txnIDByDesc(t *testing.T, conn *sql.DB, desc string) string {
	t.Helper()
	var id int64
	require.NoError(t, conn.QueryRow("SELECT id FROM transactions WHERE description = ?", desc).Scan(&id))
	return strconv.FormatInt(id, 10)
}

// tagsByDesc maps each transaction description to its sorted, space-joined tags (all lines).
func tagsByDesc(t *testing.T, conn *sql.DB) map[string]string {
	t.Helper()
	rows, err := conn.Query(`SELECT t.description, COALESCE(group_concat(tg.name, ' '), '') FROM transactions t
		LEFT JOIN splits s ON s.transaction_id = t.id
		LEFT JOIN split_tags st ON st.split_id = s.id LEFT JOIN tags tg ON tg.id = st.tag_id
		GROUP BY t.id ORDER BY t.description, tg.name`)
	require.NoError(t, err)
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var d, tags string
		require.NoError(t, rows.Scan(&d, &tags))
		out[d] = tags
	}
	return out
}

func TestTransactionListRowsHaveCheckboxesAndABulkBar(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	s.spend("2026-09-01", "One", 100)
	s.spend("2026-09-02", "Two", 200)

	_, d := get(t, srv, "/transactions?q=o")
	boxes := d.Find("#txn-list input.pick[form=bulk][name=id]")
	assert.Equal(t, 2, boxes.Length(), "one checkbox per row, belonging to the bulk form")
	bar := d.Find("form#bulk")
	require.Equal(t, 1, bar.Length())
	_, hidden := bar.Attr("hidden")
	assert.True(t, hidden, "the bar only shows once something is ticked")
	assert.Equal(t, "/transactions?q=o", attr(bar.Find("input[name=return]"), "value"), "it returns to this filtered view")
	assert.Equal(t, "2", attr(bar, "data-count"))
	var ops []string
	bar.Find("select[name=op] option").Each(func(_ int, o *goquery.Selection) { ops = append(ops, attr(o, "value")) })
	assert.Equal(t, []string{"add", "remove", "move"}, ops)

	// The home page's recent list is not selectable, and an empty result has no bar.
	_, d = get(t, srv, "/")
	assert.Equal(t, 0, d.Find("input.pick").Length())
	_, d = get(t, srv, "/transactions?q=zzz")
	assert.Equal(t, 0, d.Find("form#bulk").Length())
}

func TestBulkReviewSaysWhatWillChangeAndChangesNothing(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	s.spend("2026-09-01", "One", 100)
	s.spend("2026-09-02", "Two", 200, "old")
	s.spend("2026-09-03", "Three", 300)
	before := tagsByDesc(t, conn)

	form := url.Values{"op": {"add"}, "bulk_tag": {"#Trip Home"}, "return": {"/transactions?q=o"}}
	form["id"] = []string{txnIDByDesc(t, conn, "One"), txnIDByDesc(t, conn, "Two")}
	resp, d := post(t, srv, "/transactions/bulk", form)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, rowTexts(d.Find(".bulk-summary")), "Add #trip-home to 2 transactions (2 lines).")
	assert.Equal(t, before, tagsByDesc(t, conn), "reviewing changes nothing")
	assert.Equal(t, []string{form["id"][0], form["id"][1]}, valsOf(d.Find("form[action='/transactions/bulk/apply'] input[name=id]")))
	assert.Equal(t, "/transactions?q=o", attr(d.Find("form[action='/transactions/bulk/apply'] input[name=return]"), "value"))
}

func TestBulkApplyAddRemoveMove(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	s.spend("2026-09-01", "One", 100, "old")
	s.spend("2026-09-02", "Two", 200, "old", "keep")
	s.spend("2026-09-03", "Three", 300, "old")
	one, two := txnIDByDesc(t, conn, "One"), txnIDByDesc(t, conn, "Two")
	apply := func(op, tag, to string, ids ...string) *http.Response {
		f := url.Values{"op": {op}, "bulk_tag": {tag}, "bulk_to": {to}, "return": {"/transactions"}}
		f["id"] = ids
		return postRaw(t, srv, "/transactions/bulk/apply", f)
	}

	resp := apply("move", "old", "new", one, two)
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, "/transactions", resp.Header.Get("Location"))
	assert.Equal(t, "Moved #old → #new: 2 transactions updated (2 lines).", flashOf(resp))
	assert.Equal(t, map[string]string{"One": "new", "Two": "keep new", "Three": "old"}, tagsByDesc(t, conn), "Three was not selected")

	resp = apply("add", "party", "", one)
	assert.Equal(t, "Added #party: 1 transaction updated (1 line).", flashOf(resp))
	resp = apply("remove", "new", "", one, two)
	assert.Equal(t, "Removed #new: 2 transactions updated (2 lines).", flashOf(resp))
	assert.Equal(t, map[string]string{"One": "party", "Two": "keep", "Three": "old"}, tagsByDesc(t, conn))
}

func TestBulkSelectAllMatchingUsesTheFilterNotThePage(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	for i := 0; i < 60; i++ { // more than one page of 50
		s.spend("2026-08-01", "Coffee "+strconv.Itoa(i), 100)
	}
	s.spend("2026-08-02", "Rent", 100)

	f := url.Values{"op": {"add"}, "bulk_tag": {"caffeine"}, "scope": {"all"}, "return": {"/transactions?q=coffee"}}
	_, d := post(t, srv, "/transactions/bulk", f)
	assert.Contains(t, rowTexts(d.Find(".bulk-summary")), "60 transactions")
	resp := postRaw(t, srv, "/transactions/bulk/apply", f)
	assert.Equal(t, "Added #caffeine: 60 transactions updated (60 lines).", flashOf(resp))
	tags := tagsByDesc(t, conn)
	assert.Equal(t, "caffeine", tags["Coffee 59"], "including rows past the first page")
	assert.Equal(t, "", tags["Rent"], "but not what the filter excludes")
}

func TestBulkProblemsComeBackAsAFlash(t *testing.T) {
	srv, conn := newApp(t)
	newSeeder(t, conn).spend("2026-09-01", "One", 100, "x")
	one := txnIDByDesc(t, conn, "One")
	for name, f := range map[string]url.Values{
		"nothing ticked":          {"op": {"add"}, "bulk_tag": {"y"}},
		"blank tag":               {"op": {"add"}, "bulk_tag": {"  "}, "id": {one}},
		"move to itself":          {"op": {"move"}, "bulk_tag": {"x"}, "bulk_to": {"X"}, "id": {one}},
		"nothing would change":    {"op": {"add"}, "bulk_tag": {"x"}, "id": {one}},
		"unknown external return": {"op": {"add"}, "bulk_tag": {"y"}, "return": {"//evil.example"}},
	} {
		resp := postRaw(t, srv, "/transactions/bulk", f)
		require.Equal(t, http.StatusSeeOther, resp.StatusCode, name)
		assert.NotEmpty(t, flashOf(resp), name)
		assert.Equal(t, "/transactions", resp.Header.Get("Location"), name)
	}
	assert.Equal(t, map[string]string{"One": "x"}, tagsByDesc(t, conn))
}

func valsOf(s *goquery.Selection) []string {
	var out []string
	s.Each(func(_ int, in *goquery.Selection) { out = append(out, attr(in, "value")) })
	return out
}
