package ledger

import (
	"strings"
	"unicode"
)

// TagSlug normalises free text to a kebab-case tag name: "Outside Food" -> "outside-food",
// "PC+Tech" -> "pc-tech", "#Food Truck" -> "food-truck". Letters and digits (including
// non-ASCII ones) are kept; apostrophes are dropped; everything else becomes a hyphen.
func TagSlug(s string) string { return slugify(s, false) }

// AccountSlug is like TagSlug but ASCII-only, matching the accounts.slug CHECK constraint:
// "Dollars in Sam's Wallet" -> "dollars-in-sams-wallet".
func AccountSlug(s string) string { return slugify(s, true) }

func slugify(s string, asciiOnly bool) string {
	var b strings.Builder
	pendingHyphen := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r == '\'' || r == '’':
			continue
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if asciiOnly && r > unicode.MaxASCII {
				pendingHyphen = true
				continue
			}
			if pendingHyphen && b.Len() > 0 {
				b.WriteByte('-')
			}
			pendingHyphen = false
			b.WriteRune(r)
		default:
			pendingHyphen = true
		}
	}
	return b.String()
}
