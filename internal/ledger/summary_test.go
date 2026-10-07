package ledger

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func sp(typ, name string, value int64, tags ...string) Split {
	return Split{AccountType: typ, AccountName: name, Value: value, Tags: tags}
}

func TestSummary(t *testing.T) {
	tests := []struct {
		name         string
		splits       []Split
		dir          Direction
		net, gross   int64
		headline     int64
		accounts     []string
		tags         []string
		hasImbalance bool
	}{
		{
			"expense", []Split{sp("liability", "BRISK", -1250), sp("expense", "Expenses", 1250, "fast-food")},
			DirOut, -1250, 1250, -1250, []string{"BRISK"}, []string{"fast-food"}, false,
		},
		{
			"income", []Split{sp("asset", "Wharf Bank", 500), sp("income", "Income", -500, "cashback")},
			DirIn, 500, 500, 500, []string{"Wharf Bank"}, []string{"cashback"}, false,
		},
		{
			"transfer", []Split{sp("asset", "Wharf Bank", -6500), sp("liability", "BRISK", 6500)},
			DirTransfer, 0, 6500, 6500, []string{"Wharf Bank", "BRISK"}, nil, false,
		},
		{
			"split receipt: tags union in first-seen order, deduped",
			[]Split{
				sp("liability", "BRISK", -6500),
				sp("expense", "Expenses", 1000, "drinks"),
				sp("expense", "Expenses", 2000, "snacks", "drinks"),
				sp("expense", "Expenses", 3500, "stationary"),
			},
			DirOut, -6500, 6500, -6500, []string{"BRISK"}, []string{"drinks", "snacks", "stationary"}, false,
		},
		{
			"imbalance flagged", []Split{sp("liability", "BRISK", -12000), sp("imbalance", "Imbalance", 12000)},
			DirOut, -12000, 12000, -12000, []string{"BRISK"}, nil, true,
		},
		{
			"opening balance counts as inflow", []Split{sp("asset", "Bravo", 298400), sp("equity", "Equity", -298400, "opening-balance")},
			DirIn, 298400, 298400, 298400, []string{"Bravo"}, []string{"opening-balance"}, false,
		},
		{
			"no real account", []Split{sp("expense", "Expenses", 100), sp("income", "Income", -100)},
			DirOther, 0, 100, 100, nil, nil, false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := Transaction{Splits: tc.splits}.Summary()
			assert.Equal(t, tc.dir, s.Direction)
			assert.Equal(t, tc.net, s.Net)
			assert.Equal(t, tc.gross, s.Gross)
			assert.Equal(t, tc.headline, s.Headline())
			assert.Equal(t, tc.accounts, s.Accounts)
			assert.Equal(t, tc.tags, s.Tags)
			assert.Equal(t, tc.hasImbalance, s.HasImbalance)
		})
	}
}
