package web_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/ledger"
)

// Server clock: 2026-10-01.

func ruleForm(text, freq, every, anchor, end string) url.Values {
	return url.Values{"text": {text}, "freq": {freq}, "every": {every}, "anchor": {anchor}, "end": {end}}
}

func dueRows(d *goquery.Document) []string {
	var out []string
	d.Find("ul.due-list > li").Each(func(_ int, li *goquery.Selection) {
		out = append(out, li.Find(".date").Text()+" "+li.Find(".desc").Text()+" "+li.Find(".amount").Text())
	})
	return out
}

func TestRecurringCreateAndDue(t *testing.T) {
	srv, conn := newApp(t)

	resp := postRaw(t, srv, "/recurring/create", ruleForm("1500 Rent #rent @brisk", "monthly", "1", "2026-08-01", ""))
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, "/recurring", resp.Header.Get("Location"))
	assert.Equal(t, "Added “1500 Rent #rent @brisk” (Monthly on the 1st).", flashOf(resp))

	_, d := get(t, srv, "/recurring")
	assert.Equal(t, "Recurring · vinance", d.Find("title").Text())
	assert.Equal(t, []string{"2026-08-01 Rent -$1,500.00", "2026-09-01 Rent -$1,500.00", "2026-10-01 Rent -$1,500.00"}, dueRows(d),
		"each overdue month is offered, oldest first")
	assert.Equal(t, 2, d.Find("ul.due-list li .chip.warn").Length(), "the two past ones are flagged overdue")
	assert.Equal(t, "BRISK", d.Find("ul.due-list li").First().Find(".chip.account").Text())
	assert.Equal(t, "#rent", d.Find("ul.due-list li").First().Find(".chip.tag").Text())

	rule := d.Find("ul.rule-list > li").First()
	assert.Equal(t, "1500 Rent #rent @brisk", rule.Find(".tag-name").Text())
	assert.Equal(t, "Next: 2026-08-01", rule.Find(".tag-uses").Text())
	assert.Contains(t, rule.Find(".account-meta").Text(), "Monthly on the 1st")
	assert.Contains(t, rule.Find(".account-meta").Text(), "-$1,500.00")

	assert.Equal(t, 0, count(t, conn, "transactions"), "nothing is created until you say so")
}

func TestRecurringAcceptAddsTheTransaction(t *testing.T) {
	srv, conn := newApp(t)
	postRaw(t, srv, "/recurring/create", ruleForm("1500 Rent #rent @brisk", "monthly", "1", "2026-08-01", ""))
	_, d := get(t, srv, "/recurring")
	accept := formValues(t, d, `ul.due-list > li:first-child form[action$="/accept"]`)
	assert.Equal(t, "2026-08-01", accept.Get("date"))

	resp := postRaw(t, srv, "/recurring/1/accept", accept)
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, "Added Rent $1,500.00 for 2026-08-01.", flashOf(resp))

	got := txnFromDB(t, conn, 1)
	assert.Equal(t, "Rent", got.Description)
	assert.Equal(t, "2026-08-01", got.Date, "dated on the due date, not today")
	assert.Equal(t, "BRISK", got.Splits[0].AccountName)
	assert.EqualValues(t, -150000, got.Splits[0].Amount)
	assert.Equal(t, []string{"rent"}, got.Splits[1].Tags)

	_, d = get(t, srv, "/recurring")
	assert.Equal(t, []string{"2026-09-01 Rent -$1,500.00", "2026-10-01 Rent -$1,500.00"}, dueRows(d))
	assert.Equal(t, "Next: 2026-09-01", d.Find("ul.rule-list > li .tag-uses").First().Text())

	// A double-click (the same occurrence posted again) is harmless.
	resp = postRaw(t, srv, "/recurring/1/accept", accept)
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, "That one was already handled.", flashOf(resp))
	assert.Equal(t, 1, count(t, conn, "transactions"))

	// The added transaction is an ordinary one: it shows up in the list and can be edited.
	_, list := get(t, srv, "/transactions")
	assert.Equal(t, []string{"Rent"}, listDescs(list))
}

func TestRecurringSkip(t *testing.T) {
	srv, conn := newApp(t)
	postRaw(t, srv, "/recurring/create", ruleForm("1500 Rent @brisk", "monthly", "1", "2026-09-01", ""))

	resp := postRaw(t, srv, "/recurring/1/skip", url.Values{"date": {"2026-09-01"}})
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, "Skipped 2026-09-01.", flashOf(resp))
	_, d := get(t, srv, "/recurring")
	assert.Equal(t, []string{"2026-10-01 Rent -$1,500.00"}, dueRows(d))
	assert.Equal(t, 0, count(t, conn, "transactions"))

	resp = postRaw(t, srv, "/recurring/1/skip", url.Values{"date": {"2026-09-01"}})
	assert.Equal(t, "That one was already handled.", flashOf(resp))
	resp = postRaw(t, srv, "/recurring/99/skip", url.Values{"date": {"2026-09-01"}})
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestRecurringKindsIncomeAndTransfer(t *testing.T) {
	srv, conn := newApp(t)
	postRaw(t, srv, "/recurring/create", ruleForm("+2500 Paycheck #salary @wharf-bank", "weekly", "2", "2026-09-18", ""))
	postRaw(t, srv, "/recurring/create", ruleForm("500 Card payment @wharf-bank @brisk", "monthly", "1", "2026-10-01", ""))

	_, d := get(t, srv, "/recurring")
	rows := dueRows(d)
	assert.Contains(t, rows, "2026-09-18 Paycheck +$2,500.00")
	assert.Contains(t, rows, "2026-10-01 Card payment $500.00", "transfers show a plain amount")
	assert.NotContains(t, rows, "2026-10-02 Paycheck +$2,500.00", "Oct 2 is tomorrow: not due yet")

	// Accept the paycheck: income lands in Wharf Bank.
	resp := postRaw(t, srv, "/recurring/1/accept", url.Values{"date": {"2026-09-18"}})
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	got := txnFromDB(t, conn, 1)
	assert.Equal(t, ledger.DirIn, got.Summary().Direction)
	assert.EqualValues(t, 250000, got.Summary().Net)

	// Biweekly: the next occurrence is two weeks later (Oct 2, not yet due).
	_, d = get(t, srv, "/recurring")
	assert.Equal(t, "Next: 2026-10-02", d.Find("ul.rule-list > li").First().Find(".tag-uses").Text())
}

func TestRecurringRuleValidation(t *testing.T) {
	srv, conn := newApp(t)
	tests := []struct {
		name string
		form url.Values
		want string
	}{
		{"empty text", ruleForm(" ", "monthly", "1", "2026-10-01", ""), "type an amount"},
		{"date in the text", ruleForm("5 Rent @brisk 10/15", "monthly", "1", "2026-10-01", ""), "leave the date out"},
		{"no account", ruleForm("5 Rent #rent", "monthly", "1", "2026-10-01", ""), "include the @account"},
		{"unknown account", ruleForm("5 Rent @nope", "monthly", "1", "2026-10-01", ""), "no account matches @nope"},
		{"missing amount", ruleForm("Rent @brisk", "monthly", "1", "2026-10-01", ""), "add an amount"},
		{"bad frequency", ruleForm("5 Rent @brisk", "daily", "1", "2026-10-01", ""), "frequency"},
		{"every zero", ruleForm("5 Rent @brisk", "monthly", "0", "2026-10-01", ""), "between 1 and 99"},
		{"every text", ruleForm("5 Rent @brisk", "monthly", "x", "2026-10-01", ""), "whole number"},
		{"bad start", ruleForm("5 Rent @brisk", "monthly", "1", "tomorrow", ""), "start date"},
		{"end before start", ruleForm("5 Rent @brisk", "monthly", "1", "2026-10-01", "2026-09-01"), "before the start"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, d := post(t, srv, "/recurring/create", tc.form)
			require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
			assert.Contains(t, strings.ToLower(d.Find("p.problems").First().Text()), strings.ToLower(tc.want))
			v, _ := d.Find("details.add-account input[name=text]").Attr("value")
			assert.Equal(t, trimmed(tc.form.Get("text")), v, "the form keeps what was typed")
			_, open := d.Find("details.add-account").Attr("open")
			assert.True(t, open)
		})
	}
	rules, err := ledger.NewService(conn).ListRules(context.Background())
	require.NoError(t, err)
	assert.Empty(t, rules, "nothing was created")
}

func trimmed(s string) string {
	for len(s) > 0 && s[0] == ' ' {
		s = s[1:]
	}
	for len(s) > 0 && s[len(s)-1] == ' ' {
		s = s[:len(s)-1]
	}
	return s
}

func TestRecurringUpdatePauseDelete(t *testing.T) {
	srv, conn := newApp(t)
	postRaw(t, srv, "/recurring/create", ruleForm("1500 Rent @brisk", "monthly", "1", "2026-08-01", ""))
	postRaw(t, srv, "/recurring/1/accept", url.Values{"date": {"2026-08-01"}})

	// Edit the text: your place in the schedule is kept.
	resp := postRaw(t, srv, "/recurring/1/update", ruleForm("1550 Rent #rent @brisk", "monthly", "1", "2026-08-01", ""))
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, "Saved “1550 Rent #rent @brisk”.", flashOf(resp))
	_, d := get(t, srv, "/recurring")
	assert.Equal(t, []string{"2026-09-01 Rent -$1,550.00", "2026-10-01 Rent -$1,550.00"}, dueRows(d))

	// Errors show beside the rule, and the edit form stays open with what was typed.
	resp2, d := post(t, srv, "/recurring/1/update", ruleForm("1550 Rent @nope", "monthly", "1", "2026-08-01", ""))
	require.Equal(t, http.StatusUnprocessableEntity, resp2.StatusCode)
	li := d.Find("ul.rule-list > li").First()
	assert.Contains(t, strings.ToLower(li.Find("details p.problems").Text()), "no account matches @nope")
	_, open := li.Find("details").Attr("open")
	assert.True(t, open)
	v, _ := li.Find("input[name=text]").Attr("value")
	assert.Equal(t, "1550 Rent @nope", v)

	// Changing the schedule restarts at the first date on/after today.
	postRaw(t, srv, "/recurring/1/update", ruleForm("1550 Rent #rent @brisk", "monthly", "1", "2026-01-15", ""))
	_, d = get(t, srv, "/recurring")
	assert.Empty(t, dueRows(d), "old dates aren't resurrected")
	assert.Equal(t, "Next: 2026-10-15", d.Find("ul.rule-list > li .tag-uses").First().Text())

	// Pause hides it from Due; resume brings it back.
	resp = postRaw(t, srv, "/recurring/1/active", url.Values{"active": {"0"}})
	assert.Equal(t, "Paused.", flashOf(resp))
	_, d = get(t, srv, "/recurring")
	assert.Equal(t, "Paused", d.Find("ul.rule-list > li .tag-uses").First().Text())
	assert.True(t, d.Find("ul.rule-list > li").First().HasClass("paused"))
	resp = postRaw(t, srv, "/recurring/1/active", url.Values{"active": {"1"}})
	assert.Equal(t, "Resumed.", flashOf(resp))

	// Delete keeps the transactions it created.
	resp = postRaw(t, srv, "/recurring/1/delete", url.Values{})
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, 1, count(t, conn, "transactions"))
	_, d = get(t, srv, "/recurring")
	assert.Equal(t, 0, d.Find("ul.rule-list > li").Length())
	for _, p := range []string{"/recurring/1/update", "/recurring/1/active", "/recurring/1/delete", "/recurring/1/accept", "/recurring/abc/delete"} {
		r := postRaw(t, srv, p, ruleForm("5 x @brisk", "monthly", "1", "2026-10-01", ""))
		assert.Equal(t, http.StatusNotFound, r.StatusCode, p)
	}
}

func TestRecurringRuleWhoseAccountWasArchived(t *testing.T) {
	srv, conn := newApp(t)
	postRaw(t, srv, "/recurring/create", ruleForm("1500 Rent @brisk", "monthly", "1", "2026-10-01", ""))
	require.NoError(t, ledger.NewService(conn).SetAccountArchived(context.Background(), accountID(t, conn, "brisk"), true))

	_, d := get(t, srv, "/recurring")
	due := d.Find("ul.due-list > li").First()
	assert.Contains(t, due.Find("p.problems").Text(), "can't be added as written")
	assert.Contains(t, due.Find("p.problems").Text(), "no account matches @brisk")
	_, disabled := due.Find(`form[action$="/accept"] button`).Attr("disabled")
	assert.True(t, disabled, "Add is disabled until the rule is fixed")
	assert.Contains(t, d.Find("ul.rule-list > li .problems").First().Text(), "can't be added as written")

	resp, d := post(t, srv, "/recurring/1/accept", url.Values{"date": {"2026-10-01"}})
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, "forcing it is refused too")
	assert.Equal(t, 0, count(t, conn, "transactions"))

	// Fixing the rule's account makes it addable again.
	postRaw(t, srv, "/recurring/1/update", ruleForm("1500 Rent @wharf-bank", "monthly", "1", "2026-10-01", ""))
	resp = postRaw(t, srv, "/recurring/1/accept", url.Values{"date": {"2026-10-01"}})
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, 1, count(t, conn, "transactions"))
}

func TestHomeBannerForDueRecurring(t *testing.T) {
	srv, _ := newApp(t)
	_, d := get(t, srv, "/")
	assert.Equal(t, 0, d.Find(".due-banner").Length(), "nothing due, no banner")

	postRaw(t, srv, "/recurring/create", ruleForm("1500 Rent @brisk", "monthly", "1", "2026-10-01", ""))
	_, d = get(t, srv, "/")
	assert.Equal(t, "1 recurring transaction is due →", rowTexts(d.Find(".due-banner a")))
	href, _ := d.Find(".due-banner a").Attr("href")
	assert.Equal(t, "/recurring", href)

	postRaw(t, srv, "/recurring/create", ruleForm("9.99 Streaming @brisk", "monthly", "1", "2026-09-15", ""))
	_, d = get(t, srv, "/")
	assert.Equal(t, "2 recurring transactions are due →", rowTexts(d.Find(".due-banner a")), "Rent (Oct 1) and Streaming (Sep 15); Streaming's Oct 15 is not due yet")
}

func TestRecurringNavAndCrossOrigin(t *testing.T) {
	srv, conn := newApp(t)
	_, d := get(t, srv, "/")
	assert.Equal(t, 1, d.Find(`header nav a[href="/recurring"]`).Length())

	resp := postRaw(t, srv, "/recurring/create", ruleForm("5 x @brisk", "monthly", "1", "2026-10-01", ""), "Sec-Fetch-Site", "cross-site")
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	rules, _ := ledger.NewService(conn).ListRules(context.Background())
	assert.Empty(t, rules)
}
