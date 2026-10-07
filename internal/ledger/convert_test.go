package ledger

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertToUSD(t *testing.T) {
	tests := []struct {
		name  string
		minor int64
		cur   string
		rate  string
		want  int64
	}{
		{"USD passes through", 12345, "USD", "", 12345},
		{"USD ignores a bogus rate", 12345, "usd", "garbage", 12345},
		{"yen: 100,000 JPY at 0.0062555", 100000, "JPY", "0.0062555", 62555},
		{"won: 713,937 KRW", 713937, "KRW", "0.0007000057", 49976},
		{"pesos: 500.00 MXN", 50000, "MXN", "0.0562050599", 2810},
		{"negative balances convert symmetrically", -50000, "MXN", "0.0562050599", -2810},
		{"half rounds away from zero (positive)", 1, "MXN", "0.5", 1},   // 0.01 MXN * 0.5 = $0.005
		{"half rounds away from zero (negative)", -1, "MXN", "0.5", -1}, // -$0.005
		{"just under half rounds down", 1, "MXN", "0.4999", 0},
		{"zero stays zero", 0, "MXN", "0.05", 0},
		{"rate with surrounding spaces", 100, "MXN", " 0.05 ", 5},
		{"rate as integer (100 won at $1/won = $100.00)", 100, "KRW", "1", 10000},
		{"large balance stays exact", 123456789012, "MXN", "0.05", 6172839451}, // 1,234,567,890.12 * 0.05
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ConvertToUSD(tc.minor, tc.cur, tc.rate)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestConvertToUSDRejectsBadRates(t *testing.T) {
	for _, rate := range []string{"", "abc", "0", "-0.05", "0.0.5", "1/0"} {
		_, err := ConvertToUSD(100, "MXN", rate)
		assert.Error(t, err, "%q", rate)
	}
}
