package views

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestThousands(t *testing.T) {
	for in, want := range map[int]string{0: "0", 7: "7", 999: "999", 1000: "1,000", 1424: "1,424", 1234567: "1,234,567"} {
		assert.Equal(t, want, Thousands(in))
	}
}

func TestDayHeading(t *testing.T) {
	assert.Equal(t, "October 5, 2026", DayHeading("2026-10-05"))
	assert.Equal(t, "January 1, 2027", DayHeading("2027-01-01"))
	assert.Equal(t, "garbage", DayHeading("garbage"))
}
