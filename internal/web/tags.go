package web

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/amascii/vinance/internal/ledger"
	"github.com/amascii/vinance/internal/web/views"
)

// dateRange is a named span of dates; empty bounds are open.
type dateRange struct {
	key, label, from, to string
}

// ranges returns the presets, relative to today.
func ranges(today time.Time) []dateRange {
	day := func(t time.Time) string { return t.Format("2006-01-02") }
	first := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, today.Location())
	lastOfMonth := first.AddDate(0, 1, -1)
	prevFirst := first.AddDate(0, -1, 0)
	return []dateRange{
		{"month", "This month", day(first), day(lastOfMonth)},
		{"last-month", "Last month", day(prevFirst), day(first.AddDate(0, 0, -1))},
		{"30d", "Last 30 days", day(today.AddDate(0, 0, -29)), day(today)},
		{"ytd", "Year to date", fmt.Sprintf("%d-01-01", today.Year()), day(today)},
		{"all", "All time", "", ""},
	}
}

func (s *Server) tags(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	income := q.Get("kind") == "income"
	kind := "expense"
	if income {
		kind = "income"
	}
	presets := ranges(s.now())

	// A range is a preset (?range=) or a custom from/to; the default is this month.
	var from, to, active string
	from, to = q.Get("from"), q.Get("to")
	var notices []string
	for _, d := range []*string{&from, &to} {
		if *d != "" {
			if _, err := time.Parse("2006-01-02", *d); err != nil {
				notices = append(notices, "Dates must look like 2026-09-24.")
				from, to = "", ""
				break
			}
		}
	}
	custom := from != "" || to != ""
	if !custom {
		active = "month" // the default, also used for an unknown preset name
		for _, p := range presets {
			if p.key == q.Get("range") {
				active = p.key
			}
		}
		for _, p := range presets {
			if p.key == active {
				from, to = p.from, p.to
			}
		}
	}

	rep, err := s.ledger.TagReport(r.Context(), ledger.ReportFilter{Income: income, From: from, To: to})
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	m := views.TagsPage{Notices: notices, Kind: kind, KindLabel: map[bool]string{false: "spending", true: "income"}[income], Missing: rep.Missing}
	href := func(kv ...string) string {
		v := url.Values{}
		v.Set("kind", kind)
		for i := 0; i+1 < len(kv); i += 2 {
			v.Set(kv[i], kv[i+1])
		}
		return "/tags?" + v.Encode()
	}
	for _, p := range presets {
		m.Ranges = append(m.Ranges, views.Choice{Label: p.label, Href: href("range", p.key), Active: !custom && p.key == active})
	}
	keep := func(k string) string { // switch kind, keep the range
		v := url.Values{"kind": {k}}
		if custom {
			v.Set("from", from)
			v.Set("to", to)
		} else {
			v.Set("range", active)
		}
		return "/tags?" + v.Encode()
	}
	m.Kinds = []views.Choice{
		{Label: "Spending", Href: keep("expense"), Active: !income},
		{Label: "Income", Href: keep("income"), Active: income},
	}
	m.From, m.To = from, to
	m.RangeLabel = rangeLabel(from, to)

	m.Total = ledger.Format(rep.Total.USD, "USD")
	m.TotalNote = lineNote(rep.Total)
	m.Empty = rep.Total.Splits == 0

	var max int64
	for _, l := range rep.Lines {
		if l.USD > max {
			max = l.USD
		}
	}
	if rep.Untagged.USD > max {
		max = rep.Untagged.USD
	}
	drill := func(extra ...string) string {
		v := url.Values{}
		for i := 0; i+1 < len(extra); i += 2 {
			v.Set(extra[i], extra[i+1])
		}
		if from != "" {
			v.Set("from", from)
		}
		if to != "" {
			v.Set("to", to)
		}
		return "/transactions?" + v.Encode()
	}
	bar := func(l ledger.TagLine, name, link string) views.TagBar {
		pct := 0
		if max > 0 && l.USD > 0 {
			pct = int(l.USD * 100 / max)
			if pct < 1 {
				pct = 1
			}
		}
		return views.TagBar{
			Tag: name, Href: link, Amount: ledger.Format(l.USD, "USD"), Detail: lineNote(l), Pct: pct,
			Title: fmt.Sprintf("%s: %s across %d %s", name, ledger.Format(l.USD, "USD"), l.Splits, plural(l.Splits, "line", "lines")),
		}
	}
	for _, l := range rep.Lines {
		m.Bars = append(m.Bars, bar(l, l.Tag, drill("tag", l.Tag)))
	}
	if rep.Untagged.Splits > 0 {
		u := bar(rep.Untagged, "Untagged", drill("untagged", "1"))
		m.Untagged = &u
	}
	s.render(w, r, http.StatusOK, views.TagsPageView(m))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// lineNote summarises a line: its item count and any non-USD amounts it includes.
func lineNote(l ledger.TagLine) string {
	var parts []string
	var other []string
	for _, c := range l.ByCurrency {
		if c.Currency != "USD" {
			other = append(other, ledger.Format(c.Amount, c.Currency))
		}
	}
	parts = append(parts, fmt.Sprintf("%s %s", views.Thousands(l.Splits), plural(l.Splits, "item", "items")))
	if len(other) > 0 {
		parts = append(parts, "incl. "+strings.Join(other, ", "))
	}
	return strings.Join(parts, " · ")
}

func rangeLabel(from, to string) string {
	f := func(d string) string {
		t, err := time.Parse("2006-01-02", d)
		if err != nil {
			return d
		}
		return t.Format("Jan 2, 2006")
	}
	switch {
	case from == "" && to == "":
		return "all time"
	case from == "":
		return "through " + f(to)
	case to == "":
		return "since " + f(from)
	}
	return f(from) + " – " + f(to)
}
