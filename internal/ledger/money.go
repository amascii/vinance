// Package ledger holds money handling, validation and the transaction service.
package ledger

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Amounts are int64 minor units (cents, centavos, yen). Never use floats for money.

// Decimals returns the number of minor-unit decimal places for a currency.
func Decimals(currency string) int {
	switch strings.ToUpper(currency) {
	case "JPY", "KRW":
		return 0
	default:
		return 2
	}
}

var symbols = map[string]string{"USD": "$", "MXN": "MX$", "JPY": "¥", "KRW": "₩"}

// Symbol returns the display prefix for a currency ("$", "MX$", ...), or "CODE " when unknown.
func Symbol(currency string) string {
	if s, ok := symbols[strings.ToUpper(currency)]; ok {
		return s
	}
	return strings.ToUpper(currency) + " "
}

var symbolPrefix = regexp.MustCompile(`^(?:Mex\$|MX\$|US\$|JP¥|\$|¥|₩)`)

const maxDigits = 15 // keeps results well inside int64

// ParseAmount converts text such as "12.50", "1,234.5", "-$5", "100,000." into minor units.
// A leading "-" or "+", a currency symbol prefix and thousands commas are accepted. Too many decimals is an error.
func ParseAmount(s, currency string) (int64, error) {
	orig := s
	s = strings.TrimSpace(s)
	neg := false
	switch {
	case strings.HasPrefix(s, "-"):
		neg, s = true, strings.TrimSpace(s[1:])
	case strings.HasPrefix(s, "+"):
		s = strings.TrimSpace(s[1:]) // an explicit plus ("+100.00") means the same as no sign
	}
	s = symbolPrefix.ReplaceAllString(s, "")
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
	if s == "" {
		return 0, fmt.Errorf("invalid amount %q", orig)
	}

	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" && frac == "" {
		return 0, fmt.Errorf("invalid amount %q", orig)
	}
	if !allDigits(whole) || !allDigits(frac) {
		return 0, fmt.Errorf("invalid amount %q", orig)
	}
	dec := Decimals(currency)
	if len(frac) > dec {
		return 0, fmt.Errorf("invalid amount %q: %s has %d decimal places", orig, strings.ToUpper(currency), dec)
	}
	frac += strings.Repeat("0", dec-len(frac))
	digits := strings.TrimLeft(whole+frac, "0")
	if len(digits) > maxDigits {
		return 0, fmt.Errorf("invalid amount %q: too large", orig)
	}
	var n int64
	for _, c := range digits {
		n = n*10 + int64(c-'0')
	}
	if neg {
		n = -n
	}
	return n, nil
}

func allDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// FormatPlain renders minor units with thousands separators and no symbol: "-1,234.50".
func FormatPlain(minor int64, currency string) string {
	neg := minor < 0
	u := uint64(minor)
	if neg {
		u = -u
	}
	dec := Decimals(currency)
	digits := fmt.Sprintf("%d", u)
	for len(digits) <= dec {
		digits = "0" + digits
	}
	whole, frac := digits[:len(digits)-dec], digits[len(digits)-dec:]
	var b strings.Builder
	for i, c := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	out := b.String()
	if dec > 0 {
		out += "." + frac
	}
	if neg {
		out = "-" + out
	}
	return out
}

// Format renders minor units with the currency symbol: "$1,234.50", "-MX$500.00", "¥100,000".
func Format(minor int64, currency string) string {
	plain := FormatPlain(minor, currency)
	if strings.HasPrefix(plain, "-") {
		return "-" + Symbol(currency) + plain[1:]
	}
	return Symbol(currency) + plain
}

// ErrInvalidCurrency is returned for currency codes that aren't 3 uppercase letters.
var ErrInvalidCurrency = errors.New("invalid currency code")

// ValidCurrency reports whether s looks like an ISO 4217 code (three uppercase letters).
func ValidCurrency(s string) bool {
	if len(s) != 3 {
		return false
	}
	for _, c := range s {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}

// FormatInput renders minor units the way a text field should hold them: plain digits with
// the currency's decimals, a leading "-" when negative, and no thousands separators or symbol
// ("-65.00", "100000"). ParseAmount reads it back exactly.
func FormatInput(minor int64, currency string) string {
	return strings.ReplaceAll(FormatPlain(minor, currency), ",", "")
}
