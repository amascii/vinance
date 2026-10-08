package views

import (
	"net/url"
	"strconv"
	"strings"
	"time"
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// Thousands formats n with comma separators: 1234 -> "1,234".
func Thousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func plural(n int, singular, pluralForm string) string {
	if n == 1 {
		return singular
	}
	return pluralForm
}

// field names a form field of split line i: r-2-amount.
func field(i int, name string) string { return "r-" + strconv.Itoa(i) + "-" + name }

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func joinComma(items []string) string { return strings.Join(items, ", ") }

// manageURL is the tag management page with its filter state.
func ManageURL(q, sort string) string {
	v := url.Values{}
	if q != "" {
		v.Set("q", q)
	}
	if sort != "" && sort != "name" {
		v.Set("sort", sort)
	}
	if enc := v.Encode(); enc != "" {
		return "/tags/manage?" + enc
	}
	return "/tags/manage"
}

// DayHeading formats a YYYY-MM-DD date as "October 5, 2026" (the input itself if it doesn't parse).
func DayHeading(date string) string {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return d.Format("January 2, 2006")
}
