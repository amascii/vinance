package ledger

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAmount(t *testing.T) {
	tests := []struct {
		in, cur string
		want    int64
	}{
		{"12.50", "USD", 1250},
		{"12.5", "USD", 1250},
		{"12", "USD", 1200},
		{"0.05", "USD", 5},
		{".5", "USD", 50},
		{"0", "USD", 0},
		{"1,234.56", "USD", 123456},
		{"-500.00", "MXN", -50000},
		{"+100.00", "MXN", 10000},
		{"+ 12.50", "USD", 1250},
		{"+$12.50", "USD", 1250},
		{"+0", "USD", 0},
		{"-$5", "USD", -500},
		{"$12.50", "USD", 1250},
		{"Mex$2,984.00", "MXN", 298400},
		{"-Mex$10,000.00", "MXN", -1000000},
		{"100,000.", "JPY", 100000},
		{"JP¥100,000", "JPY", 100000},
		{"-₩713,937.", "KRW", -713937},
		{"  7.25 ", "USD", 725},
		{"007.10", "USD", 710},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseAmount(tc.in, tc.cur)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParseAmountErrors(t *testing.T) {
	tests := []struct{ in, cur string }{
		{"", "USD"}, {"abc", "USD"}, {".", "USD"}, {"-", "USD"}, {"$", "USD"},
		{"1.234", "USD"}, // too many decimals
		{"100.5", "JPY"}, // yen has no decimals
		{"1.2.3", "USD"},
		{"12,5x", "USD"},
		{"--5", "USD"}, {"+-5", "USD"}, {"-+5", "USD"}, {"++5", "USD"}, {"+", "USD"},
		{"1234567890123456", "USD"}, // too large
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			_, err := ParseAmount(tc.in, tc.cur)
			assert.Error(t, err)
		})
	}
}

func TestFormat(t *testing.T) {
	tests := []struct {
		minor      int64
		cur        string
		plain, sym string
	}{
		{1250, "USD", "12.50", "$12.50"},
		{-1250, "USD", "-12.50", "-$12.50"},
		{5, "USD", "0.05", "$0.05"},
		{0, "USD", "0.00", "$0.00"},
		{123456789, "USD", "1,234,567.89", "$1,234,567.89"},
		{-50000, "MXN", "-500.00", "-MX$500.00"},
		{100000, "JPY", "100,000", "¥100,000"},
		{-713937, "KRW", "-713,937", "-₩713,937"},
		{999, "EUR", "9.99", "EUR 9.99"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.plain, FormatPlain(tc.minor, tc.cur), "plain %d %s", tc.minor, tc.cur)
		assert.Equal(t, tc.sym, Format(tc.minor, tc.cur), "sym %d %s", tc.minor, tc.cur)
	}
}

func TestParseFormatRoundTrip(t *testing.T) {
	for _, cur := range []string{"USD", "MXN", "JPY", "KRW"} {
		for _, n := range []int64{0, 1, 99, 100, 12345, -12345, 99999999} {
			got, err := ParseAmount(FormatPlain(n, cur), cur)
			require.NoError(t, err)
			assert.Equal(t, n, got, "%s %d", cur, n)
			got, err = ParseAmount(Format(n, cur), cur)
			if cur == "USD" || cur == "MXN" || cur == "JPY" || cur == "KRW" {
				require.NoError(t, err)
				assert.Equal(t, n, got, "%s %d with symbol", cur, n)
			}
		}
	}
}

func TestValidCurrency(t *testing.T) {
	assert.True(t, ValidCurrency("USD"))
	assert.False(t, ValidCurrency("usd"))
	assert.False(t, ValidCurrency("US"))
	assert.False(t, ValidCurrency(""))
}

func TestSlugs(t *testing.T) {
	tests := []struct{ in, tag, acct string }{
		{"Outside Food", "outside-food", "outside-food"},
		{"PC+Tech", "pc-tech", "pc-tech"},
		{"#Food Truck", "food-truck", "food-truck"},
		{"  --Snacks!!  ", "snacks", "snacks"},
		{"Dollars in Sam's Wallet", "dollars-in-sams-wallet", "dollars-in-sams-wallet"},
		{"Wharf Bank Active Cash", "wharf-bank-active-cash", "wharf-bank-active-cash"},
		{"Café", "café", "caf"},
		{"Seoul Bank", "seoul-bank", "seoul-bank"},
		{"Opening Balances - MXN", "opening-balances-mxn", "opening-balances-mxn"},
		{"", "", ""},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.tag, TagSlug(tc.in), "tag %q", tc.in)
		assert.Equal(t, tc.acct, AccountSlug(tc.in), "acct %q", tc.in)
	}
}

func TestFormatInputRoundTrips(t *testing.T) {
	tests := []struct {
		minor int64
		cur   string
		want  string
	}{
		{-6500, "USD", "-65.00"}, {123456789, "USD", "1234567.89"}, {5, "USD", "0.05"}, {0, "MXN", "0.00"},
		{100000, "JPY", "100000"}, {-713937, "KRW", "-713937"},
	}
	for _, tc := range tests {
		got := FormatInput(tc.minor, tc.cur)
		assert.Equal(t, tc.want, got)
		back, err := ParseAmount(got, tc.cur)
		require.NoError(t, err)
		assert.Equal(t, tc.minor, back)
	}
}
