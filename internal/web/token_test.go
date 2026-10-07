package web

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTokenAt(t *testing.T) {
	tests := []struct {
		text string
		pos  int
		want string
	}{
		{"12.50 coffee #sn", 16, "#sn"},
		{"12.50 coffee #sn", 14, "#sn"}, // caret just before '#'
		{"5 #sn x @bi", 5, "#sn"},       // caret at the end of a middle token
		{"5 #sn x @bi", 3, "#sn"},       // caret inside it
		{"5 #sn x @bi", 11, "@bi"},
		{"5 x ", 4, ""},  // after a trailing space: empty token
		{"5 x", 99, "x"}, // out-of-range clamps
		{"5 x", -3, "5"},
		{"", 0, ""},
		{"🍔 #bu", 6, "#bu"}, // the burger is two UTF-16 units
		{"🍔 #bu", 2, "🍔"},   // caret right after the burger
		{"🍔 #bu", 4, "#bu"}, // caret inside the tag token
		{"café #bu", 8, "#bu"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, tokenAt(tc.text, tc.pos), "%q @%d", tc.text, tc.pos)
	}
}

func TestSanitizePrefix(t *testing.T) {
	assert.Equal(t, "snack-bar", sanitizePrefix("snack-bar"))
	assert.Equal(t, "100", sanitizePrefix("100%"))
	assert.Equal(t, "", sanitizePrefix("_%\\"))
	assert.Equal(t, "café", sanitizePrefix("café"))
}
