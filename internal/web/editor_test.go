package web_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/db/gen"
	"github.com/amascii/vinance/internal/ledger"
	"github.com/amascii/vinance/internal/testutil"
)

// walmart seeds the $65 BRISK receipt split into drinks, snacks and a plain remainder.
func walmart(t *testing.T, s *seeder) int64 {
	t.Helper()
	id, err := s.svc.Create(context.Background(), ledger.TxnInput{
		Date: "2026-09-02", Description: "Walmart", Currency: "USD", Notes: "weekly",
		Splits: []ledger.SplitInput{
			{AccountID: s.acct["brisk"], Currency: "USD", Amount: -6500, Value: -6500},
			{AccountID: s.acct["expense"], Memo: "Sodas", Currency: "USD", Amount: 1000, Value: 1000, Tags: []string{"drinks"}},
			{AccountID: s.acct["expense"], Memo: "Chips", Currency: "USD", Amount: 2000, Value: 2000, Tags: []string{"snacks", "party"}},
			{AccountID: s.acct["expense"], Currency: "USD", Amount: 3500, Value: 3500},
		},
	})
	require.NoError(t, err)
	return id
}

func editorPath(id int64, next string) string {
	p := "/transactions/" + strconv.FormatInt(id, 10)
	if next != "" {
		p += "?next=" + url.QueryEscape(next)
	}
	return p
}

func lineCount(d *goquery.Document) int { return d.Find("#editor ol.lines > li.line").Length() }

func rowVal(d *goquery.Document, i int, field string) string {
	sel := d.Find(`#editor [name="r-` + strconv.Itoa(i) + `-` + field + `"]`)
	if sel.Is("select") {
		v, _ := sel.Find("option[selected]").First().Attr("value")
		return v
	}
	v, _ := sel.Attr("value")
	return v
}

func txnFromDB(t *testing.T, conn *sql.DB, id int64) ledger.Transaction {
	t.Helper()
	got, err := ledger.NewService(conn).Get(context.Background(), id)
	require.NoError(t, err)
	return got
}

func TestEditorShowsTheTransaction(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	id := walmart(t, s)

	resp, d := get(t, srv, editorPath(id, "/transactions?tag=drinks"))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "Edit transaction · vinance", d.Find("title").Text())

	v := func(sel string) string { x, _ := d.Find(sel).Attr("value"); return x }
	assert.Equal(t, "2026-09-02", v("input[name=date]"))
	assert.Equal(t, "Walmart", v("input[name=description]"))
	assert.Equal(t, "weekly", d.Find("textarea[name=notes]").Text())
	assert.Equal(t, "USD", v("input[name=currency]"))
	assert.Equal(t, "/transactions?tag=drinks", v("input[name=next]"))
	assert.Equal(t, "/transactions?tag=drinks", func() string { h, _ := d.Find("p.crumbs a").Attr("href"); return h }())

	require.Equal(t, 4, lineCount(d))
	assert.Equal(t, strconv.FormatInt(s.acct["brisk"], 10), rowVal(d, 0, "account"))
	assert.Equal(t, "-65.00", rowVal(d, 0, "amount"))
	assert.Equal(t, strconv.FormatInt(s.acct["expense"], 10), rowVal(d, 1, "account"))
	assert.Equal(t, "10.00", rowVal(d, 1, "amount"))
	assert.Equal(t, "Sodas", rowVal(d, 1, "memo"))
	assert.Equal(t, "#drinks", rowVal(d, 1, "tags"))
	assert.Equal(t, "#party #snacks", rowVal(d, 2, "tags"))
	assert.Equal(t, "", rowVal(d, 3, "tags"))

	assert.Equal(t, "Balanced ✓", d.Find("#remaining .remaining").Text())
	_, disabled := d.Find("button#save").Attr("disabled")
	assert.False(t, disabled, "Save is enabled when the lines balance")
	assert.Equal(t, 0, d.Find(".notice.warn").Length())
	assert.Contains(t, d.Find("p.meta").Text(), "created")
	assert.NotContains(t, d.Find("p.meta").Text(), "GnuCash")

	// Category lines can pick a currency; real-account lines show theirs as fixed text.
	assert.Equal(t, 3, d.Find("select.cur").Length())
	assert.Equal(t, "USD", d.Find("li.line").First().Find(".cur-label").Text())
	var groups []string
	d.Find("li.line").First().Find("select[name=r-0-account] optgroup").Each(func(_ int, g *goquery.Selection) {
		l, _ := g.Attr("label")
		groups = append(groups, l)
	})
	assert.Equal(t, []string{"Categories", "Accounts"}, groups)
}

func TestEditorNotFound(t *testing.T) {
	srv, _ := newApp(t)
	for _, path := range []string{"/transactions/999", "/transactions/abc", "/transactions/0", "/transactions/-3"} {
		resp, _ := get(t, srv, path)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode, path)
	}
	resp := postRaw(t, srv, "/transactions/999", url.Values{"action": {"save"}})
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp = postRaw(t, srv, "/transactions/999/delete", url.Values{})
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestEditorSaveSplitsAReceipt(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	// Quick-add recorded one plain $65 line; now split it the way the statement is broken down.
	id, err := s.svc.Create(context.Background(), ledger.TxnInput{
		Date: "2026-09-02", Description: "Walmart", Currency: "USD",
		Splits: []ledger.SplitInput{
			{AccountID: s.acct["brisk"], Currency: "USD", Amount: -6500, Value: -6500},
			{AccountID: s.acct["expense"], Currency: "USD", Amount: 6500, Value: 6500, Tags: []string{"groceries"}},
		},
	})
	require.NoError(t, err)

	_, d := get(t, srv, editorPath(id, "/transactions"))
	v := formValues(t, d, "form#editor")

	// Shrink the existing item, add two more, each time letting "Add line" fill in the remainder.
	v.Set("r-1-amount", "10.00")
	v.Set("r-1-tags", "#drinks")
	v.Set("r-1-memo", "Sodas")
	v.Set("action", "add")
	resp, d := post(t, srv, editorPath(id, ""), v)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, 3, lineCount(d))
	assert.Equal(t, "55.00", rowVal(d, 2, "amount"), "the new line takes what is left to allocate")
	assert.Equal(t, strconv.FormatInt(s.acct["expense"], 10), rowVal(d, 2, "account"))

	v = formValues(t, d, "form#editor")
	v.Set("r-2-amount", "20.00")
	v.Set("r-2-tags", "snacks, party")
	v.Set("r-2-memo", "Chips")
	v.Set("action", "add")
	_, d = post(t, srv, editorPath(id, ""), v)
	require.Equal(t, 4, lineCount(d))
	assert.Equal(t, "35.00", rowVal(d, 3, "amount"))

	v = formValues(t, d, "form#editor")
	v.Set("r-3-tags", "stationary")
	v.Set("r-3-memo", "Notebook")
	v.Set("action", "save")
	save := postRaw(t, srv, editorPath(id, ""), v)
	require.Equal(t, http.StatusSeeOther, save.StatusCode)
	assert.Equal(t, "/transactions", save.Header.Get("Location"))

	got := txnFromDB(t, conn, id)
	require.Len(t, got.Splits, 4)
	assert.EqualValues(t, -6500, got.Splits[0].Amount)
	assert.Equal(t, []string{"drinks"}, got.Splits[1].Tags)
	assert.Equal(t, []string{"party", "snacks"}, got.Splits[2].Tags, "commas and spaces both separate tags")
	assert.Equal(t, "Notebook", got.Splits[3].Memo)
	assert.EqualValues(t, 3500, got.Splits[3].Amount)
	assert.Equal(t, "2026-09-02", got.Date)
	sum := got.Summary()
	assert.EqualValues(t, -6500, sum.Net)
	assert.Equal(t, []string{"drinks", "party", "snacks", "stationary"}, sortedCopy(sum.Tags))
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func TestEditorSaveEditsHeaderAndHonoursNext(t *testing.T) {
	srv, conn := newApp(t)
	id := walmart(t, newSeeder(t, conn))

	_, d := get(t, srv, editorPath(id, "/transactions?imbalance=1"))
	v := formValues(t, d, "form#editor")
	v.Set("date", "2026-09-03")
	v.Set("description", "Walmart Supercenter")
	v.Set("notes", "fixed")
	v.Set("action", "save")

	resp := postRaw(t, srv, editorPath(id, ""), v)
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, "/transactions?imbalance=1", resp.Header.Get("Location"), "returns to where the user came from")
	got := txnFromDB(t, conn, id)
	assert.Equal(t, "Walmart Supercenter", got.Description)
	assert.Equal(t, "2026-09-03", got.Date)
	assert.Equal(t, "fixed", got.Notes)

	// htmx saves answer with HX-Redirect instead of a 3xx the XHR would follow silently.
	v.Set("description", "again")
	resp = postRaw(t, srv, editorPath(id, ""), v, "HX-Request", "true")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "/transactions?imbalance=1", resp.Header.Get("HX-Redirect"))
}

func TestEditorNextCannotBeAnOpenRedirect(t *testing.T) {
	srv, conn := newApp(t)
	id := walmart(t, newSeeder(t, conn))
	for _, evil := range []string{"//evil.example", "https://evil.example", "http://evil.example/x", `/\evil.example`, "javascript:alert(1)", "evil"} {
		_, d := get(t, srv, editorPath(id, ""))
		v := formValues(t, d, "form#editor")
		v.Set("next", evil)
		v.Set("action", "save")
		resp := postRaw(t, srv, editorPath(id, ""), v)
		assert.Equal(t, "/transactions", resp.Header.Get("Location"), "next=%q", evil)
	}
	_, d := get(t, srv, editorPath(id, "//evil.example"))
	next, _ := d.Find("input[name=next]").Attr("value")
	assert.Empty(t, next, "an unsafe next is dropped when rendering too")
}

func TestEditorRejectsUnbalancedSave(t *testing.T) {
	srv, conn := newApp(t)
	id := walmart(t, newSeeder(t, conn))

	_, d := get(t, srv, editorPath(id, ""))
	v := formValues(t, d, "form#editor")
	v.Set("r-3-amount", "30.00") // lines now total $60 against a $65 charge
	v.Set("description", "should not stick")
	v.Set("action", "save")

	resp, d := post(t, srv, editorPath(id, ""), v)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	assert.Contains(t, d.Find(".problems").Text(), "off by 5.00")
	assert.Equal(t, "$5.00 left to allocate", d.Find("#remaining .remaining").Text())
	_, disabled := d.Find("button#save").Attr("disabled")
	assert.True(t, disabled)
	assert.Equal(t, "should not stick", func() string { x, _ := d.Find("input[name=description]").Attr("value"); return x }(), "edits are kept for correction")

	got := txnFromDB(t, conn, id)
	assert.Equal(t, "Walmart", got.Description, "nothing was saved")
	assert.EqualValues(t, 3500, got.Splits[3].Amount)
}

func TestEditorLineErrors(t *testing.T) {
	srv, conn := newApp(t)
	id := walmart(t, newSeeder(t, conn))
	_, d := get(t, srv, editorPath(id, ""))

	tests := []struct {
		name, field, val, want string
	}{
		{"blank amount", "r-1-amount", "", "Line 2: enter an amount."},
		{"junk amount", "r-1-amount", "ten", "Line 2: the amount isn't valid for USD"},
		{"too many decimals", "r-2-amount", "1.234", "Line 3: the amount isn't valid"},
		{"no account", "r-3-account", "", "Line 4: choose an account."},
		{"unknown account", "r-3-account", "99999", "Line 4: choose an account."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := formValues(t, d, "form#editor")
			v.Set(tc.field, tc.val)
			v.Set("action", "save")
			resp, page := post(t, srv, editorPath(id, ""), v)
			require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
			assert.Contains(t, page.Find(".line-error").Text(), tc.want)
			assert.Equal(t, 1, page.Find("li.line .line-error").Length())
			assert.Equal(t, "Walmart", txnFromDB(t, conn, id).Description)
		})
	}

	t.Run("form-level validation: bad date and empty description", func(t *testing.T) {
		v := formValues(t, d, "form#editor")
		v.Set("date", "not-a-date")
		v.Set("description", "  ")
		v.Set("action", "save")
		resp, page := post(t, srv, editorPath(id, ""), v)
		require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
		problems := page.Find(".problems").Text()
		assert.Contains(t, problems, "not a valid YYYY-MM-DD date")
		assert.Contains(t, problems, "description is required")
	})

	t.Run("fewer than two lines", func(t *testing.T) {
		v := formValues(t, d, "form#editor")
		for _, k := range []string{"r-1-", "r-2-", "r-3-"} {
			for _, f := range []string{"account", "amount", "currency", "memo", "tags", "rec"} {
				v.Del(k + f)
			}
		}
		v.Set("r-0-amount", "0.00")
		v.Set("action", "save")
		resp, page := post(t, srv, editorPath(id, ""), v)
		require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
		assert.Contains(t, page.Find(".problems").Text(), "at least 2 splits")
	})
}

func TestEditorAddLineChoosesCategoryAndRemainder(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	id := walmart(t, s)

	// Balanced: the new line starts empty.
	_, d := get(t, srv, editorPath(id, ""))
	v := formValues(t, d, "form#editor")
	v.Set("action", "add")
	_, d = post(t, srv, editorPath(id, ""), v)
	require.Equal(t, 5, lineCount(d))
	assert.Equal(t, "", rowVal(d, 4, "amount"))
	assert.Equal(t, strconv.FormatInt(s.acct["expense"], 10), rowVal(d, 4, "account"))
	assert.Equal(t, "Balanced ✓", d.Find("#remaining .remaining").Text(), "an empty line doesn't unbalance anything")
	assert.Equal(t, 1, d.Find("li.line:nth-child(5) .cur").Length())

	// Over-allocated: the remainder is negative and the line takes it.
	v = formValues(t, d, "form#editor")
	v.Del("r-4-amount")
	v.Set("r-4-amount", "")
	v.Set("r-1-amount", "15.00") // +5 over
	v.Set("action", "add")
	_, d = post(t, srv, editorPath(id, ""), v)
	require.Equal(t, 6, lineCount(d))
	assert.Equal(t, "-5.00", rowVal(d, 5, "amount"))

	// The line after a real-account line defaults to Expenses.
	_, d = get(t, srv, editorPath(id, ""))
	v = formValues(t, d, "form#editor")
	for _, f := range []string{"account", "amount", "currency", "memo", "tags", "rec"} {
		v.Del("r-3-" + f)
		v.Del("r-2-" + f)
		v.Del("r-1-" + f)
	}
	v.Set("action", "add")
	_, d = post(t, srv, editorPath(id, ""), v)
	assert.Equal(t, strconv.FormatInt(s.acct["expense"], 10), rowVal(d, 1, "account"))
	assert.Equal(t, "65.00", rowVal(d, 1, "amount"))
}

func TestEditorBlankLinesAreIgnoredAndDroppedOnSave(t *testing.T) {
	srv, conn := newApp(t)
	id := walmart(t, newSeeder(t, conn))

	_, d := get(t, srv, editorPath(id, ""))
	v := formValues(t, d, "form#editor")
	v.Set("action", "add")
	_, d = post(t, srv, editorPath(id, ""), v)
	require.Equal(t, 5, lineCount(d))

	v = formValues(t, d, "form#editor")
	v.Set("action", "save")
	require.Equal(t, http.StatusSeeOther, postRaw(t, srv, editorPath(id, ""), v).StatusCode)
	assert.Len(t, txnFromDB(t, conn, id).Splits, 4, "the empty fifth line was not saved")

	// A line with a memo but no amount is a mistake, not a blank.
	v = formValues(t, d, "form#editor")
	v.Set("r-4-memo", "forgot the amount")
	v.Set("action", "save")
	resp, page := post(t, srv, editorPath(id, ""), v)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	assert.Contains(t, page.Find(".line-error").Text(), "Line 5: enter an amount.")
}

func TestEditorRemoveAndReorderLines(t *testing.T) {
	srv, conn := newApp(t)
	id := walmart(t, newSeeder(t, conn))
	_, d := get(t, srv, editorPath(id, ""))
	base := formValues(t, d, "form#editor")

	act := func(action string) *goquery.Document {
		v := url.Values{}
		for k, vals := range base {
			v[k] = append([]string(nil), vals...)
		}
		v.Set("action", action)
		resp, page := post(t, srv, editorPath(id, ""), v)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		return page
	}
	memos := func(d *goquery.Document) []string {
		var out []string
		for i := 0; i < lineCount(d); i++ {
			out = append(out, rowVal(d, i, "memo"))
		}
		return out
	}

	assert.Equal(t, []string{"", "Sodas", "Chips", ""}, memos(act("recalc")))
	assert.Equal(t, []string{"", "Chips", ""}, memos(act("remove-1")))
	assert.Equal(t, []string{"Sodas", "", "Chips", ""}, memos(act("up-1")))
	assert.Equal(t, []string{"", "Chips", "Sodas", ""}, memos(act("down-1")))
	assert.Equal(t, []string{"", "Sodas", "Chips", ""}, memos(act("up-0")), "up on the first line is a no-op")
	assert.Equal(t, []string{"", "Sodas", "Chips", ""}, memos(act("down-3")), "down on the last line is a no-op")
	assert.Equal(t, []string{"", "Sodas", "Chips", ""}, memos(act("remove-9")), "out-of-range index is ignored")
	assert.Equal(t, []string{"", "Sodas", "Chips", ""}, memos(act("remove-x")))

	removed := act("remove-3")
	assert.Equal(t, "$35.00 left to allocate", removed.Find("#remaining .remaining").Text())
	_, disabled := removed.Find("button#save").Attr("disabled")
	assert.True(t, disabled, "Save is blocked until the lines balance again")

	// First line can't move up, last can't move down: the buttons are disabled.
	_, d = get(t, srv, editorPath(id, ""))
	_, firstUp := d.Find("li.line").First().Find(`button[value="up-0"]`).Attr("disabled")
	_, lastDown := d.Find("li.line").Last().Find(`button[value="down-3"]`).Attr("disabled")
	assert.True(t, firstUp)
	assert.True(t, lastDown)

	// Nothing above touched the database.
	assert.Len(t, txnFromDB(t, conn, id).Splits, 4)
}

func TestEditorLinesNumberedByIndexNotLexically(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	id := walmart(t, s)
	// A hand-built post with 12 lines, indices 0..11: line r-10 must sort after r-2.
	v := url.Values{"action": {"recalc"}, "currency": {"USD"}, "date": {"2026-09-02"}, "description": {"x"}}
	for i := 0; i < 12; i++ {
		p := "r-" + strconv.Itoa(i) + "-"
		v.Set(p+"account", strconv.FormatInt(s.acct["expense"], 10))
		v.Set(p+"amount", strconv.Itoa(i))
		v.Set(p+"memo", "m"+strconv.Itoa(i))
	}
	_, d := post(t, srv, editorPath(id, ""), v)
	require.Equal(t, 12, lineCount(d))
	assert.Equal(t, "m2", rowVal(d, 2, "memo"))
	assert.Equal(t, "m10", rowVal(d, 10, "memo"))
	assert.Equal(t, "m11", rowVal(d, 11, "memo"))
}

func TestEditorRemainingPreviewAndSaveButton(t *testing.T) {
	srv, conn := newApp(t)
	id := walmart(t, newSeeder(t, conn))
	_, d := get(t, srv, editorPath(id, ""))
	v := formValues(t, d, "form#editor")

	v.Set("r-2-amount", "17.50") // $2.50 short
	resp, frag := post(t, srv, editorPath(id, "")+"/remaining", v)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "$2.50 left to allocate", frag.Find(".remaining").Text())
	assert.True(t, frag.Find(".remaining").HasClass("under"))
	save := frag.Find("button#save")
	_, disabled := save.Attr("disabled")
	_, oob := save.Attr("hx-swap-oob")
	assert.True(t, disabled)
	assert.True(t, oob, "Save is swapped out-of-band")

	v.Set("r-2-amount", "22.50") // $2.50 over
	_, frag = post(t, srv, editorPath(id, "")+"/remaining", v)
	assert.Equal(t, "Over-allocated by $2.50", frag.Find(".remaining").Text())
	assert.True(t, frag.Find(".remaining").HasClass("over"))

	v.Set("r-2-amount", "20.00")
	_, frag = post(t, srv, editorPath(id, "")+"/remaining", v)
	assert.Equal(t, "Balanced ✓", frag.Find(".remaining").Text())
	_, disabled = frag.Find("button#save").Attr("disabled")
	assert.False(t, disabled)

	v.Set("r-2-amount", "twenty")
	_, frag = post(t, srv, editorPath(id, "")+"/remaining", v)
	assert.NotEqual(t, "Balanced ✓", frag.Find(".remaining").Text(), "a typo can't look balanced")
	_, disabled = frag.Find("button#save").Attr("disabled")
	assert.True(t, disabled)

	// The live indicator never writes anything.
	assert.EqualValues(t, 2000, txnFromDB(t, conn, id).Splits[2].Amount)
}

func TestEditorCrossCurrencyLinesNeedAValue(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	// An MXN bill on a Neo account, with the expense tracked as $28.10 (like the imported Internet bill rows).
	id, err := s.svc.Create(context.Background(), ledger.TxnInput{
		Date: "2026-01-15", Description: "Internet bill", Currency: "MXN",
		Splits: []ledger.SplitInput{
			{AccountID: s.acct["neo"], Currency: "MXN", Amount: -50000, Value: -50000},
			{AccountID: s.acct["expense"], Currency: "USD", Amount: 2810, Value: 50000, Tags: []string{"internet"}},
		},
	})
	require.NoError(t, err)

	_, d := get(t, srv, editorPath(id, ""))
	assert.Equal(t, "MXN", rowVal(d, 0, "currency"))
	assert.Equal(t, 0, d.Find(`input[name="r-0-value"]`).Length(), "same-currency lines have no separate value")
	assert.Equal(t, "USD", rowVal(d, 1, "currency"))
	assert.Equal(t, "28.10", rowVal(d, 1, "amount"))
	assert.Equal(t, "500.00", rowVal(d, 1, "value"))
	assert.Contains(t, d.Find("li.line").Eq(1).Find(".line-value").Text(), "Worth in MXN")
	assert.Equal(t, "Balanced ✓", d.Find("#remaining .remaining").Text())

	// Saving unchanged keeps the cross-currency line intact.
	v := formValues(t, d, "form#editor")
	v.Set("action", "save")
	require.Equal(t, http.StatusSeeOther, postRaw(t, srv, editorPath(id, ""), v).StatusCode)
	got := txnFromDB(t, conn, id)
	assert.EqualValues(t, 2810, got.Splits[1].Amount)
	assert.EqualValues(t, 50000, got.Splits[1].Value)
	assert.Equal(t, "USD", got.Splits[1].Currency)

	// Switching a category line to MXN makes the value field go away (amount is the value).
	v.Set("r-1-currency", "MXN")
	v.Set("action", "recalc")
	_, d = post(t, srv, editorPath(id, ""), v)
	assert.Equal(t, 0, d.Find(`input[name="r-1-value"]`).Length())
	assert.Equal(t, "MXN", rowVal(d, 1, "currency"))

	// And back to USD without a value is a clear error on save, not a silent zero.
	v = formValues(t, d, "form#editor")
	v.Set("r-1-currency", "USD")
	v.Set("action", "save")
	resp, d := post(t, srv, editorPath(id, ""), v)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	assert.Contains(t, d.Find(".line-error").Text(), "enter what this is worth in MXN")
}

func TestEditorChangingAccountAdoptsItsCurrency(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	id := walmart(t, s)

	_, d := get(t, srv, editorPath(id, ""))
	v := formValues(t, d, "form#editor")
	v.Set("r-0-account", strconv.FormatInt(s.acct["neo"], 10)) // BRISK (USD) -> Neo (MXN)
	v.Set("action", "recalc")
	_, d = post(t, srv, editorPath(id, ""), v)
	assert.Equal(t, "MXN", rowVal(d, 0, "currency"), "a real account fixes the line currency")
	assert.Equal(t, "MXN", d.Find("li.line").First().Find(".cur-label").Text())
	assert.Equal(t, 1, d.Find(`input[name="r-0-value"]`).Length(), "and a MXN line in a USD transaction needs a value")

	v = formValues(t, d, "form#editor")
	v.Set("r-0-amount", "-1200.00")
	v.Set("r-0-value", "-65.00")
	v.Set("action", "save")
	require.Equal(t, http.StatusSeeOther, postRaw(t, srv, editorPath(id, ""), v).StatusCode)
	got := txnFromDB(t, conn, id)
	assert.Equal(t, "Neo", got.Splits[0].AccountName)
	assert.Equal(t, "MXN", got.Splits[0].Currency)
	assert.EqualValues(t, -120000, got.Splits[0].Amount)
	assert.EqualValues(t, -6500, got.Splits[0].Value)
}

func TestEditorFlagsAndFixesImbalance(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	id, err := s.svc.Create(context.Background(), ledger.TxnInput{
		Date: "2026-06-17", Description: "Clothing store", Currency: "USD",
		Splits: []ledger.SplitInput{
			{AccountID: s.acct["brisk"], Currency: "USD", Amount: -12000, Value: -12000},
			{AccountID: s.acct["imbalance"], Currency: "USD", Amount: 12000, Value: 12000},
		},
	})
	require.NoError(t, err)

	_, d := get(t, srv, editorPath(id, "/transactions?imbalance=1"))
	assert.Equal(t, 1, d.Find(".notice.warn").Length())
	assert.Equal(t, 1, d.Find("li.line.warn").Length())

	// Reassign the Imbalance line to Expenses and tag it.
	v := formValues(t, d, "form#editor")
	v.Set("r-1-account", strconv.FormatInt(s.acct["expense"], 10))
	v.Set("r-1-tags", "#fashion #clothes")
	v.Set("action", "save")
	require.Equal(t, http.StatusSeeOther, postRaw(t, srv, editorPath(id, ""), v).StatusCode)

	got := txnFromDB(t, conn, id)
	assert.False(t, got.Summary().HasImbalance)
	assert.Equal(t, []string{"clothes", "fashion"}, got.Splits[1].Tags)

	_, d = get(t, srv, editorPath(id, ""))
	assert.Equal(t, 0, d.Find(".notice.warn").Length())

	p, err := s.svc.List(context.Background(), ledger.Filter{Imbalance: true})
	require.NoError(t, err)
	assert.Empty(t, p.Transactions, "the fixed transaction leaves the needs-fixing list")
}

func TestEditorPreservesReconciledFlags(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	id, err := s.svc.Create(context.Background(), ledger.TxnInput{
		Date: "2026-03-06", Description: "Payment", Currency: "USD",
		Splits: []ledger.SplitInput{
			{AccountID: s.acct["wharf-bank"], Currency: "USD", Amount: -6500, Value: -6500},
			{AccountID: s.acct["brisk"], Currency: "USD", Amount: 6500, Value: 6500, Reconciled: "c"},
		},
	})
	require.NoError(t, err)

	_, d := get(t, srv, editorPath(id, ""))
	v := formValues(t, d, "form#editor")
	v.Set("description", "Payment (edited)")
	v.Set("action", "save")
	require.Equal(t, http.StatusSeeOther, postRaw(t, srv, editorPath(id, ""), v).StatusCode)
	got := txnFromDB(t, conn, id)
	assert.Equal(t, "n", got.Splits[0].Reconciled)
	assert.Equal(t, "c", got.Splits[1].Reconciled, "editing must not silently un-reconcile")
}

func TestEditorDelete(t *testing.T) {
	srv, conn := newApp(t)
	s := newSeeder(t, conn)
	id := walmart(t, s)
	keep := walmart(t, s)

	resp := postRaw(t, srv, editorPath(id, "")+"/delete", url.Values{"next": {"/transactions?tag=x"}})
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, "/transactions?tag=x", resp.Header.Get("Location"))
	assert.Equal(t, 1, count(t, conn, "transactions"))
	assert.Equal(t, 4, count(t, conn, "splits"), "its lines went with it, the other transaction's stayed")
	_, err := ledger.NewService(conn).Get(context.Background(), id)
	assert.ErrorIs(t, err, ledger.ErrNotFound)
	assert.Equal(t, "Walmart", txnFromDB(t, conn, keep).Description)

	resp = postRaw(t, srv, editorPath(keep, "")+"/delete", url.Values{"next": {"//evil.example"}}, "HX-Request", "true")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "/transactions", resp.Header.Get("HX-Redirect"))
	assert.Equal(t, 0, count(t, conn, "transactions"))
}

func TestEditorDeleteButtonAsksForConfirmation(t *testing.T) {
	srv, conn := newApp(t)
	id := walmart(t, newSeeder(t, conn))
	_, d := get(t, srv, editorPath(id, ""))
	del := d.Find("button.delete")
	confirm, _ := del.Attr("hx-confirm")
	assert.Contains(t, confirm, "Delete this transaction")
	post, _ := del.Attr("hx-post")
	assert.Equal(t, editorPath(id, "")+"/delete", post)
}

func TestListRowsLinkToTheEditor(t *testing.T) {
	srv, conn := newApp(t)
	id := walmart(t, newSeeder(t, conn))

	_, d := get(t, srv, "/transactions?tag=drinks")
	href, ok := d.Find("li.txn .desc a.edit-link").Attr("href")
	require.True(t, ok)
	assert.Equal(t, editorPath(id, "/transactions?tag=drinks"), href, "the editor returns to the filtered list")

	_, d = get(t, srv, "/")
	href, _ = d.Find("li.txn .desc a.edit-link").Attr("href")
	assert.Equal(t, editorPath(id, "/"), href)
}

func TestEditorWithoutHTMXReturnsFullPage(t *testing.T) {
	srv, conn := newApp(t)
	id := walmart(t, newSeeder(t, conn))
	_, d := get(t, srv, editorPath(id, ""))
	v := formValues(t, d, "form#editor")
	v.Set("r-3-amount", "1.00")
	v.Set("action", "save")

	req, _ := http.NewRequest(http.MethodPost, srv.URL+editorPath(id, ""), stringsReader(v.Encode())) // no HX-Request
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	page := doc(t, resp)
	assert.Equal(t, "Edit transaction · vinance", page.Find("title").Text(), "a rejected save re-renders a complete page")
	assert.Equal(t, 1, page.Find("form#editor").Length())
}

func TestEditorRejectsCrossOriginPosts(t *testing.T) {
	srv, conn := newApp(t)
	id := walmart(t, newSeeder(t, conn))
	resp := postRaw(t, srv, editorPath(id, "")+"/delete", url.Values{}, "Sec-Fetch-Site", "cross-site")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, 1, count(t, conn, "transactions"))
}

// ---- the "I paid a credit card but it was recorded as an expense" fix ----

// cardPaymentRecordedAsAnExpense reproduces how it happens: a card payment typed on the card's
// register is a charge (money out of Bravo Gold, into Expenses).
func cardPaymentRecordedAsAnExpense(t *testing.T, conn *sql.DB) (id int64, bravo, gold int64) {
	t.Helper()
	bravo = testutil.SeedAccount(t, conn, "Bravo", "asset", "MXN")
	gold = testutil.SeedAccount(t, conn, "Bravo Gold", "liability", "MXN")
	exp, err := gen.New(conn).GetBuiltinAccount(context.Background(), "expense")
	require.NoError(t, err)
	id, err = ledger.NewService(conn).Create(context.Background(), ledger.TxnInput{
		Date: "2026-10-02", Description: "Pago Tdc", Currency: "MXN",
		Splits: []ledger.SplitInput{
			{AccountID: gold, Currency: "MXN", Amount: -10000, Value: -10000},
			{AccountID: exp.ID, Currency: "MXN", Amount: 10000, Value: 10000},
		},
	})
	require.NoError(t, err)
	return id, bravo, gold
}

func remainingText(d *goquery.Document) string { return d.Find("#remaining .remaining").Text() }

func TestFixingACardPaymentRecordedAsAnExpense(t *testing.T) {
	srv, conn := newApp(t)
	id, bravo, _ := cardPaymentRecordedAsAnExpense(t, conn)
	path := editorPath(id, "")

	_, d := get(t, srv, path)
	v := formValues(t, d, "form#editor")
	assert.Equal(t, "-100.00", v.Get("r-0-amount"), "Bravo Gold: a charge")
	assert.Equal(t, "100.00", v.Get("r-1-amount"), "Expenses")

	// Step 1: Expenses becomes Bravo. Changing the account does NOT change the sign, so it is still balanced,
	// but now it says "money into Bravo, out of the card" (a cash advance), the opposite of a payment.
	v.Set("r-1-account", strconv.FormatInt(bravo, 10))
	v.Set("action", "recalc")
	_, d = post(t, srv, path, v)
	assert.Equal(t, "Balanced ✓", remainingText(d))

	// Step 2: flip only the card's line to + : now both lines are positive, so they total +200.00.
	v = formValues(t, d, "form#editor")
	v.Set("r-0-amount", "100.00")
	_, frag := post(t, srv, path+"/remaining", v)
	assert.Equal(t, "Over-allocated by MX$200.00", frag.Find(".remaining").Text(), "this is the message: both lines are positive")

	// Step 3: Bravo's line must be negative (money leaving the bank). Now it balances and saves.
	v.Set("r-1-amount", "-100.00")
	_, frag = post(t, srv, path+"/remaining", v)
	assert.Equal(t, "Balanced ✓", frag.Find(".remaining").Text())
	v.Set("action", "save")
	require.Equal(t, http.StatusSeeOther, postRaw(t, srv, path, v).StatusCode)

	got := txnFromDB(t, conn, id)
	assert.Equal(t, "Bravo Gold", got.Splits[0].AccountName)
	assert.EqualValues(t, 10000, got.Splits[0].Amount, "money INTO the card: what you owe goes down")
	assert.Equal(t, "Bravo", got.Splits[1].AccountName)
	assert.EqualValues(t, -10000, got.Splits[1].Amount, "money OUT of the bank")
	assert.Equal(t, ledger.DirTransfer, got.Summary().Direction, "it is now a transfer, no longer an expense")
	rep, err := ledger.NewService(conn).TagReport(context.Background(), ledger.ReportFilter{})
	require.NoError(t, err)
	assert.Zero(t, rep.Total.Splits, "a transfer is not spending: the expense is gone from the reports")
}

func TestPlusSignIsAcceptedInEditorAmounts(t *testing.T) {
	srv, conn := newApp(t)
	id, bravo, _ := cardPaymentRecordedAsAnExpense(t, conn)
	path := editorPath(id, "")

	_, d := get(t, srv, path)
	v := formValues(t, d, "form#editor")
	v.Set("r-0-amount", "+100.00") // as the editor's own hint suggests
	v.Set("r-1-account", strconv.FormatInt(bravo, 10))
	v.Set("r-1-amount", "-100.00")
	_, frag := post(t, srv, path+"/remaining", v)
	assert.Equal(t, "Balanced ✓", frag.Find(".remaining").Text())
	v.Set("action", "save")
	require.Equal(t, http.StatusSeeOther, postRaw(t, srv, path, v).StatusCode)
	assert.EqualValues(t, 10000, txnFromDB(t, conn, id).Splits[0].Amount)
}

func TestAMalformedAmountIsNamedInsteadOfBlamingTheOtherLines(t *testing.T) {
	srv, conn := newApp(t)
	id, bravo, _ := cardPaymentRecordedAsAnExpense(t, conn)
	path := editorPath(id, "")
	_, d := get(t, srv, path)

	for _, bad := range []string{"abc", "100.0.1", "−100.00" /* a typographic minus */, "100,00x"} {
		v := formValues(t, d, "form#editor")
		v.Set("r-0-amount", bad)
		v.Set("r-1-account", strconv.FormatInt(bravo, 10))
		v.Set("r-1-amount", "-100.00")

		_, frag := post(t, srv, path+"/remaining", v)
		assert.Equal(t, "Line 1: that amount isn't a number (like -12.50 or +12.50)", frag.Find(".remaining").Text(), "%q", bad)
		_, disabled := frag.Find("button#save").Attr("disabled")
		assert.True(t, disabled, "Save stays off while a line isn't a number: %q", bad)

		// And the line itself is flagged right away (not only after a failed save).
		v.Set("action", "recalc")
		_, page := post(t, srv, path, v)
		assert.Contains(t, page.Find("li.line").First().Find(".line-error").Text(), "the amount isn't valid", "%q", bad)
		assert.Equal(t, 0, page.Find("li.line").Eq(1).Find(".line-error").Length(), "the good line is not flagged")
	}

	// A line that is just unfinished (a memo typed, no amount yet) is not nagged about until you save.
	v := formValues(t, d, "form#editor")
	v.Set("r-1-amount", "")
	v.Set("r-1-memo", "typing a note first")
	v.Set("action", "recalc")
	_, page := post(t, srv, path, v)
	assert.Equal(t, 0, page.Find(".line-error").Length())
}

func TestEditorHintExplainsSigns(t *testing.T) {
	srv, conn := newApp(t)
	id, _, _ := cardPaymentRecordedAsAnExpense(t, conn)
	_, d := get(t, srv, editorPath(id, ""))
	assert.Contains(t, d.Find("p.hint").First().Text(), "Changing a line's account does not flip its sign")
}

func TestEditorLinesWireUpTagSuggestions(t *testing.T) {
	srv, conn := newApp(t)
	id := walmart(t, newSeeder(t, conn))
	_, d := get(t, srv, editorPath(id, ""))
	require.Equal(t, 4, d.Find("li.line").Length())
	d.Find("li.line").Each(func(_ int, line *goquery.Selection) {
		in := line.Find("input[name$=tags]")
		assert.Equal(t, "/quickadd/suggest/tags", attr(in, "hx-post"))
		assert.Equal(t, "this", attr(in, "hx-include"), "only this field is sent, not the whole editor")
		target := attr(in, "hx-target")
		assert.Equal(t, 1, line.Find(target).Length(), "each line has its own suggestion list: %s", target)
		assert.Equal(t, attr(in, "id"), attr(line.Find(target), "data-tags-for"))
		assert.Equal(t, "innerHTML", attr(in, "hx-swap"), "set explicitly: the form's own swap must not be inherited")
	})
}
