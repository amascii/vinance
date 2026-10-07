package ledger

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func goodInput() TxnInput {
	return TxnInput{
		Date: "2026-09-24", Description: "Walmart", Currency: "USD",
		Splits: []SplitInput{
			{AccountID: 1, Currency: "USD", Amount: -6500, Value: -6500},
			{AccountID: 2, Currency: "USD", Amount: 6500, Value: 6500},
		},
	}
}

func TestValidateAcceptsGoodInput(t *testing.T) {
	assert.NoError(t, Validate(Normalize(goodInput()), nil))
}

func TestValidateProblems(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*TxnInput)
		want   string
	}{
		{"bad date", func(in *TxnInput) { in.Date = "09/24/2026" }, "date"},
		{"impossible date", func(in *TxnInput) { in.Date = "2026-02-30" }, "date"},
		{"empty description", func(in *TxnInput) { in.Description = "  " }, "description"},
		{"bad txn currency", func(in *TxnInput) { in.Currency = "dollars" }, "transaction currency"},
		{"one split", func(in *TxnInput) { in.Splits = in.Splits[:1] }, "at least 2 splits"},
		{"no splits", func(in *TxnInput) { in.Splits = nil }, "at least 2 splits"},
		{"bad split currency", func(in *TxnInput) { in.Splits[0].Currency = "" }, "split 1: currency"},
		{"amount != value same currency", func(in *TxnInput) { in.Splits[0].Amount = -1 }, "amount and value must match"},
		{"bad reconciled", func(in *TxnInput) { in.Splits[1].Reconciled = "x" }, "reconciled"},
		{"unbalanced", func(in *TxnInput) { in.Splits[1].Value, in.Splits[1].Amount = 6000, 6000 }, "off by 5.00"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := goodInput()
			tc.mutate(&in)
			err := Validate(Normalize(in), nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			var ve *ValidationError
			require.ErrorAs(t, err, &ve)
		})
	}
}

func TestValidateUnbalancedIsDetectable(t *testing.T) {
	in := goodInput()
	in.Splits[1].Value, in.Splits[1].Amount = 6000, 6000
	assert.ErrorIs(t, Validate(Normalize(in), nil), ErrUnbalanced)

	in = goodInput()
	in.Description = ""
	assert.NotErrorIs(t, Validate(Normalize(in), nil), ErrUnbalanced)
}

func TestValidateReportsAllProblems(t *testing.T) {
	in := goodInput()
	in.Date, in.Description = "nope", ""
	var ve *ValidationError
	require.ErrorAs(t, Validate(Normalize(in), nil), &ve)
	assert.Len(t, ve.Problems, 2)
}

func TestValidateCrossCurrencySplit(t *testing.T) {
	// KRW-denominated transaction paying for a USD-tracked expense: amount differs from value.
	in := TxnInput{
		Date: "2026-03-01", Description: "Korean Air", Currency: "KRW",
		Splits: []SplitInput{
			{AccountID: 1, Currency: "KRW", Amount: -713937, Value: -713937},
			{AccountID: 2, Currency: "USD", Amount: 49976, Value: 713937},
		},
	}
	assert.NoError(t, Validate(Normalize(in), nil))
}

func TestValidateAccountLookup(t *testing.T) {
	accounts := map[int64]string{1: "USD", 2: ""} // 2 is a built-in (any currency)
	lookup := func(id int64) (string, bool) { c, ok := accounts[id]; return c, ok }

	assert.NoError(t, Validate(Normalize(goodInput()), lookup))

	in := goodInput()
	in.Splits[0].Currency = "MXN"
	in.Splits[0].Amount, in.Splits[0].Value = -6500, -6500
	assert.ErrorContains(t, Validate(Normalize(in), lookup), "account currency is USD")

	in = goodInput()
	in.Splits[1].AccountID = 99
	assert.ErrorContains(t, Validate(Normalize(in), lookup), "account 99 does not exist")
}

func TestNormalize(t *testing.T) {
	in := goodInput()
	in.Date, in.Description, in.Currency = " 2026-09-24 ", "  Walmart ", " usd "
	in.Splits[0].Tags = []string{"Snacks", "#snacks", "Food Truck", "", "  "}
	out := Normalize(in)
	assert.Equal(t, "2026-09-24", out.Date)
	assert.Equal(t, "Walmart", out.Description)
	assert.Equal(t, "USD", out.Currency)
	assert.Equal(t, "n", out.Splits[0].Reconciled)
	assert.Equal(t, []string{"snacks", "food-truck"}, out.Splits[0].Tags)
	assert.Equal(t, "Snacks", in.Splits[0].Tags[0], "Normalize must not mutate its input")
}

func TestRemaining(t *testing.T) {
	assert.EqualValues(t, 0, Remaining(goodInput().Splits))
	assert.EqualValues(t, 6500, Remaining([]SplitInput{{Value: -6500}}))
	assert.EqualValues(t, -100, Remaining([]SplitInput{{Value: -6500}, {Value: 6600}}))
}
