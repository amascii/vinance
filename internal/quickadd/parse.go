// Package quickadd turns a one-line entry such as
//
//	@brisk McDonald's 12.50 #fast-food yesterday
//
// (or the older amount-first form, 12.50 McDonald's #fast-food @brisk yesterday) into a ledger transaction. It is pure (no database): Parse understands the text and
// Resolve maps account references onto real accounts.
//
// Grammar: the parts may come in any order; the account usually leads, because quick-add
// remembers it between entries:
//
//		[@account] [@to-account] DESCRIPTION... [+]AMOUNT [#tag ...] [DATE]
//
//	  - AMOUNT is the first token of the line if that is a number, otherwise the last number-like
//	    word; a leading "+" means money coming in (income).
//	  - "#tag" tokens are tags; "@name" tokens are accounts (slug or unique slug prefix).
//	    One account: the account the money left (or, with "+", arrived in).
//	    Two accounts: a transfer from the first to the second.
//	  - DATE is today, yesterday, M/D, M/D/YY, M/D/YYYY or YYYY-MM-DD. Default: today.
//	  - Everything else is the description.
package quickadd

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/amascii/vinance/internal/ledger"
)

// Parsed is the result of understanding the text. Problems are human-readable; a Parsed
// with problems is still useful for a live preview.
type Parsed struct {
	Income      bool
	Amount      string // as typed, without the leading "+" (e.g. "12.50")
	Description string
	Tags        []string // normalised, deduplicated
	Accounts    []string // account references as typed, lowercase, without "@"
	Date        string   // YYYY-MM-DD
	DateGiven   bool     // false when defaulted to today
	Problems    []string
}

// OK reports whether the input parsed without problems.
func (p Parsed) OK() bool { return len(p.Problems) == 0 }

var (
	amountRe = regexp.MustCompile(`^\+?\$?(\d[\d,]*(\.\d*)?|\.\d+)$`)
	mdRe     = regexp.MustCompile(`^(\d{1,2})/(\d{1,2})$`)
	mdyRe    = regexp.MustCompile(`^(\d{1,2})/(\d{1,2})/(\d{4}|\d{2})$`)
	isoRe    = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	// yymmddRe is the date shape typed in quick-add: 261001 is 2026-10-01. Only years 2020-2039
	// look like dates; any other six-digit number (a 100000 yen withdrawal) stays an amount.
	yymmddRe = regexp.MustCompile(`^[23]\d{5}$`)
)

// futureSlack: a year-less date (M/D) further ahead than this is taken to mean last year,
// so typing "12/30" in early January records December of the previous year.
const futureSlack = 30 * 24 * time.Hour

// tokens is a line of quick-add text split into its kinds of token, in typed order within each kind.
type tokens struct {
	accounts []string // "@brisk" (raw, with the @)
	tags     []string // "#fast-food" (raw, with the #)
	date     []string // date-looking tokens
	amount   string   // the raw amount token ("+45.20", "12.50"), "" if none
	words    []string // everything else: the description
	negative bool     // a "-5"-style amount was typed
	badDates []string // date-shaped (2xxxxx/3xxxxx) numbers that aren't real dates, next to another amount
}

var negativeRe = regexp.MustCompile(`^-\$?(\d[\d,]*(\.\d*)?|\.\d+)$`)

// classify sorts the fields of a line into accounts, tags, dates, the amount and description
// words. The amount is the first token of the line when that looks like a number (the original
// "12.50 coffee @brisk" order); otherwise it is the last number-like word, so
// "@brisk Dairy Queen 12.50" and "@brisk 12.50 coffee" both work and "7-11" or "2 for 1" stay words.
func classify(fields []string) tokens {
	var t tokens
	var others []int // indices into fields of description candidates
	for i, f := range fields {
		switch {
		case strings.HasPrefix(f, "#"):
			t.tags = append(t.tags, f)
		case strings.HasPrefix(f, "@"):
			t.accounts = append(t.accounts, f)
		case isDateToken(f):
			t.date = append(t.date, f)
		default:
			others = append(others, i)
		}
	}
	// A six-digit number shaped like a date but not a real one (261032) is almost certainly a typo
	// for a date, not a $261,032 amount, when there is another number on the line to be the amount.
	var suspects []int
	for _, i := range others {
		if yymmddRe.MatchString(fields[i]) && !validYYMMDD(fields[i]) {
			suspects = append(suspects, i)
		}
	}
	if len(suspects) > 0 {
		isSuspect := map[int]bool{}
		for _, i := range suspects {
			isSuspect[i] = true
		}
		otherAmounts := 0
		for _, i := range others {
			if !isSuspect[i] && amountRe.MatchString(fields[i]) {
				otherAmounts++
			}
		}
		if otherAmounts > 0 {
			kept := others[:0:0]
			for _, i := range others {
				if isSuspect[i] {
					t.badDates = append(t.badDates, fields[i])
				} else {
					kept = append(kept, i)
				}
			}
			others = kept
		}
	}
	amountAt := -1
	if len(fields) > 0 && amountRe.MatchString(fields[0]) && containsIndex(others, 0) {
		amountAt = 0
	} else {
		for k := len(others) - 1; k >= 0; k-- {
			if amountRe.MatchString(fields[others[k]]) {
				amountAt = others[k]
				break
			}
		}
	}
	for _, i := range others {
		switch {
		case i == amountAt:
			t.amount = fields[i]
		case negativeRe.MatchString(fields[i]):
			t.negative = true
			t.words = append(t.words, fields[i])
		default:
			t.words = append(t.words, fields[i])
		}
	}
	return t
}

func containsIndex(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Parse understands one line of quick-add text. today is injected for testability.
func Parse(input string, today time.Time) Parsed {
	p := Parsed{Date: today.Format("2006-01-02")}
	fields := strings.Fields(input)
	if len(fields) == 0 {
		p.Problems = append(p.Problems, "type an amount, a description, #tags and an @account")
		return p
	}
	t := classify(fields)

	switch {
	case t.amount != "":
		p.Income = strings.HasPrefix(t.amount, "+")
		p.Amount = strings.TrimPrefix(strings.TrimPrefix(t.amount, "+"), "$")
	case t.negative:
		p.Problems = append(p.Problems, "use a positive amount; use + for money coming in")
	default:
		p.Problems = append(p.Problems, "add an amount, e.g. 12.50")
	}

	seenTag := map[string]bool{}
	for _, raw := range t.tags {
		tag := ledger.TagSlug(raw)
		switch {
		case tag == "":
			p.Problems = append(p.Problems, "a # needs a tag name")
		case !seenTag[tag]:
			seenTag[tag] = true
			p.Tags = append(p.Tags, tag)
		}
	}
	for _, raw := range t.accounts {
		ref := strings.ToLower(strings.TrimPrefix(raw, "@"))
		if ref == "" {
			p.Problems = append(p.Problems, "an @ needs an account name")
		} else {
			p.Accounts = append(p.Accounts, ref)
		}
	}
	for _, raw := range t.badDates {
		p.Problems = append(p.Problems, fmt.Sprintf("%s isn't a real date (type dates as YYMMDD, e.g. 261001)", raw))
	}
	for _, raw := range t.date {
		d, err := parseDate(raw, today)
		switch {
		case err != nil:
			p.Problems = append(p.Problems, err.Error())
		case p.DateGiven:
			p.Problems = append(p.Problems, "only one date is allowed")
		default:
			p.Date, p.DateGiven = d, true
		}
	}
	p.Description = strings.Join(t.words, " ")

	if p.Description == "" && len(p.Problems) == 0 {
		p.Problems = append(p.Problems, "add a description")
	}
	if len(p.Accounts) > 2 {
		p.Problems = append(p.Problems, "at most two @accounts (from and to)")
	}
	return p
}

func isDateToken(tok string) bool {
	l := strings.ToLower(tok)
	// Only unambiguous shapes count as dates, so "7-11" or "2 for 1" stay description text.
	return l == "today" || l == "yesterday" || isoRe.MatchString(tok) || mdyRe.MatchString(tok) || mdRe.MatchString(tok) || validYYMMDD(tok)
}

// validYYMMDD reports whether tok is a six-digit date like 261001 (2026-10-01).
func validYYMMDD(tok string) bool {
	if !yymmddRe.MatchString(tok) {
		return false
	}
	_, err := validDate(tok, 2000+atoi(tok[0:2]), atoi(tok[2:4]), atoi(tok[4:6]))
	return err == nil
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

func parseDate(tok string, today time.Time) (string, error) {
	switch strings.ToLower(tok) {
	case "today":
		return today.Format("2006-01-02"), nil
	case "yesterday":
		return today.AddDate(0, 0, -1).Format("2006-01-02"), nil
	}
	var y, m, d int
	switch {
	case validYYMMDD(tok):
		return validDate(tok, 2000+atoi(tok[0:2]), atoi(tok[2:4]), atoi(tok[4:6]))
	case isoRe.MatchString(tok):
		fmt.Sscanf(tok, "%d-%d-%d", &y, &m, &d)
		return validDate(tok, y, m, d)
	case mdyRe.MatchString(tok):
		fmt.Sscanf(tok, "%d/%d/%d", &m, &d, &y)
		if y < 100 {
			y += 2000
		}
		return validDate(tok, y, m, d)
	case mdRe.MatchString(tok):
		fmt.Sscanf(tok, "%d/%d", &m, &d)
		got, err := validDate(tok, today.Year(), m, d)
		if err != nil {
			return "", err
		}
		t, _ := time.Parse("2006-01-02", got)
		if t.After(today.Add(futureSlack)) {
			return validDate(tok, today.Year()-1, m, d)
		}
		return got, nil
	}
	return "", fmt.Errorf("unrecognised date %q (use M/D, M/D/YYYY, YYYY-MM-DD, today or yesterday)", tok)
}

func validDate(tok string, y, m, d int) (string, error) {
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	if t.Year() != y || int(t.Month()) != m || t.Day() != d {
		return "", fmt.Errorf("%q is not a real date", tok)
	}
	return t.Format("2006-01-02"), nil
}
