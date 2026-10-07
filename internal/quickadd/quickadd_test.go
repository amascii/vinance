package quickadd

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/ledger"
)

// Thursday 2026-10-01.
var today = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func TestParseBasics(t *testing.T) {
	tests := []struct {
		name, in string
		want     Parsed
	}{
		{
			"expense with tag and account",
			"12.50 McDonald's #fast-food @brisk",
			Parsed{Amount: "12.50", Description: "McDonald's", Tags: []string{"fast-food"}, Accounts: []string{"brisk"}, Date: "2026-10-01"},
		},
		{
			"income",
			"+2000 Employer paycheck #salary @wharf-bank",
			Parsed{Income: true, Amount: "2000", Description: "Employer paycheck", Tags: []string{"salary"}, Accounts: []string{"wharf-bank"}, Date: "2026-10-01"},
		},
		{
			"transfer",
			"500 Card payment @wharf-bank @brisk",
			Parsed{Amount: "500", Description: "Card payment", Accounts: []string{"wharf-bank", "brisk"}, Date: "2026-10-01"},
		},
		{
			"multiple tags, tags before description, mixed order",
			"4 #snack #bakery croissant @neo",
			Parsed{Amount: "4", Description: "croissant", Tags: []string{"snack", "bakery"}, Accounts: []string{"neo"}, Date: "2026-10-01"},
		},
		{
			"tags are normalised and deduplicated",
			"4 x #Snack #snack #Food-Truck #SNACK",
			Parsed{Amount: "4", Description: "x", Tags: []string{"snack", "food-truck"}, Date: "2026-10-01"},
		},
		{
			"account refs lowercase",
			"4 x @BRISK",
			Parsed{Amount: "4", Description: "x", Accounts: []string{"brisk"}, Date: "2026-10-01"},
		},
		{
			"dollar sign and thousands commas",
			"$1,234.50 Rent",
			Parsed{Amount: "1,234.50", Description: "Rent", Date: "2026-10-01"},
		},
		{
			"extra whitespace collapses",
			"  7   Big   Mac   ",
			Parsed{Amount: "7", Description: "Big Mac", Date: "2026-10-01"},
		},
		{
			"digits and dashes in description are not dates",
			"5 7-11 run 2 for 1",
			Parsed{Amount: "5", Description: "7-11 run 2 for 1", Date: "2026-10-01"},
		},
		{
			"leading decimal",
			".5 gum",
			Parsed{Amount: ".5", Description: "gum", Date: "2026-10-01"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Parse(tc.in, today)
			assert.True(t, got.OK(), "problems: %v", got.Problems)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParseDates(t *testing.T) {
	tests := []struct {
		tok, want string
		given     bool
	}{
		{"today", "2026-10-01", true},
		{"Yesterday", "2026-09-30", true},
		{"9/24", "2026-09-24", true},
		{"09/05", "2026-09-05", true},
		{"9/24/2025", "2025-09-24", true},
		{"9/24/25", "2025-09-24", true},
		{"2026-09-24", "2026-09-24", true},
		{"10/15", "2026-10-15", true},      // slightly ahead is fine
		{"12/30", "2025-12-30", true},      // far ahead means last year
		{"12/30/2026", "2026-12-30", true}, // an explicit year is never second-guessed
		{"2/29", "", false},                // 2026 is not a leap year
	}
	for _, tc := range tests {
		t.Run(tc.tok, func(t *testing.T) {
			got := Parse("5 lunch "+tc.tok, today)
			if tc.want == "" {
				assert.False(t, got.OK())
				assert.Contains(t, got.Problems[0], "not a real date")
				return
			}
			require.True(t, got.OK(), "problems: %v", got.Problems)
			assert.Equal(t, tc.want, got.Date)
			assert.Equal(t, tc.given, got.DateGiven)
			assert.Equal(t, "lunch", got.Description, "date token is not part of the description")
		})
	}

	t.Run("date can sit anywhere after the amount", func(t *testing.T) {
		got := Parse("5 9/24 lunch #food", today)
		assert.Equal(t, "2026-09-24", got.Date)
		assert.Equal(t, "lunch", got.Description)
	})
	t.Run("defaults to today and says so", func(t *testing.T) {
		got := Parse("5 lunch", today)
		assert.Equal(t, "2026-10-01", got.Date)
		assert.False(t, got.DateGiven)
	})
	t.Run("rolls over year boundaries", func(t *testing.T) {
		jan := time.Date(2027, 1, 3, 0, 0, 0, 0, time.UTC)
		assert.Equal(t, "2026-12-30", Parse("5 x 12/30", jan).Date)
		assert.Equal(t, "2027-01-02", Parse("5 x yesterday", jan).Date)
		assert.Equal(t, "2026-12-31", Parse("5 x 2026-12-31", jan).Date)
	})
}

func TestParseProblems(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"empty", "", "type an amount"},
		{"blank", "   ", "type an amount"},
		{"no amount", "coffee", "add an amount"},
		{"negative", "-5 refund", "positive amount"},
		{"description missing", "5", "add a description"},
		{"description missing with tags", "5 #coffee @brisk", "add a description"},
		{"bare hash", "5 coffee #", "needs a tag name"},
		{"bare at", "5 coffee @", "needs an account name"},
		{"hash of punctuation", "5 coffee #!!", "needs a tag name"},
		{"three accounts", "5 x @a @b @c", "at most two"},
		{"two dates", "5 x 9/1 9/2", "only one date"},
		{"bad date", "5 x 13/45", "not a real date"},
		{"bad iso date", "5 x 2026-02-30", "not a real date"},
		{"amount with letters", "12abc coffee", "add an amount"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Parse(tc.in, today)
			require.False(t, got.OK())
			assert.Contains(t, got.Problems[0], tc.want)
		})
	}
}

func TestParseKeepsPartialResultForLivePreview(t *testing.T) {
	got := Parse("12.50 coffee #cafes @", today)
	assert.False(t, got.OK())
	assert.Equal(t, "12.50", got.Amount)
	assert.Equal(t, "coffee", got.Description)
	assert.Equal(t, []string{"cafes"}, got.Tags)
}

// ---- Resolve ----

var (
	exp       = Account{ID: 1, Slug: "expenses", Name: "Expenses", Type: "expense", Builtin: true}
	inc       = Account{ID: 2, Slug: "income", Name: "Income", Type: "income", Builtin: true}
	eq        = Account{ID: 3, Slug: "equity", Name: "Equity", Type: "equity", Builtin: true}
	brisk     = Account{ID: 10, Slug: "brisk", Name: "BRISK", Type: "liability", Currency: "USD"}
	wharf     = Account{ID: 11, Slug: "wharf-bank", Name: "Wharf Bank", Type: "asset", Currency: "USD"}
	wharfCash = Account{ID: 12, Slug: "wharf-bank-active-cash", Name: "Wharf Bank Active Cash", Type: "liability", Currency: "USD"}
	neo       = Account{ID: 13, Slug: "neo", Name: "Neo", Type: "asset", Currency: "MXN"}
	neoCredit = Account{ID: 14, Slug: "neo-credit", Name: "Neo Credit", Type: "liability", Currency: "MXN"}
	bravo     = Account{ID: 15, Slug: "bravo", Name: "Bravo", Type: "asset", Currency: "MXN"}
	yen       = Account{ID: 16, Slug: "yen-wallet", Name: "Yen Wallet", Type: "asset", Currency: "JPY"}
	old       = Account{ID: 17, Slug: "old-card", Name: "Old Card", Type: "liability", Currency: "USD", Archived: true}
	all       = []Account{exp, inc, eq, brisk, wharf, wharfCash, neo, neoCredit, bravo, yen, old}
)

func resolve(t *testing.T, in string, def int64) (Result, error) {
	t.Helper()
	return Resolve(Parse(in, today), all, def)
}

func TestResolveExpense(t *testing.T) {
	r, err := resolve(t, "12.50 McDonald's #fast-food @brisk", 0)
	require.NoError(t, err)
	assert.Equal(t, KindExpense, r.Kind)
	assert.Equal(t, "USD", r.Currency)
	assert.EqualValues(t, 1250, r.Amount)
	assert.Equal(t, []Account{brisk}, r.Accounts)
	in := r.Input
	assert.Equal(t, "2026-10-01", in.Date)
	assert.Equal(t, "McDonald's", in.Description)
	assert.Equal(t, "USD", in.Currency)
	require.Len(t, in.Splits, 2)
	assert.Equal(t, ledger.SplitInput{AccountID: 10, Currency: "USD", Amount: -1250, Value: -1250}, in.Splits[0])
	assert.Equal(t, ledger.SplitInput{AccountID: 1, Currency: "USD", Amount: 1250, Value: 1250, Tags: []string{"fast-food"}}, in.Splits[1])
	assert.NoError(t, ledger.Validate(ledger.Normalize(in), nil), "result is a valid balanced transaction")
	assert.Zero(t, ledger.Remaining(in.Splits))
}

func TestResolveIncome(t *testing.T) {
	r, err := resolve(t, "+2000 Employer paycheck #salary @wharf-bank", 0)
	require.NoError(t, err)
	assert.Equal(t, KindIncome, r.Kind)
	assert.Equal(t, ledger.SplitInput{AccountID: 11, Currency: "USD", Amount: 200000, Value: 200000}, r.Input.Splits[0])
	assert.Equal(t, ledger.SplitInput{AccountID: 2, Currency: "USD", Amount: -200000, Value: -200000, Tags: []string{"salary"}}, r.Input.Splits[1])
	assert.NoError(t, ledger.Validate(ledger.Normalize(r.Input), nil))
}

func TestResolveTransfer(t *testing.T) {
	r, err := resolve(t, "500 Card payment #cc-payment @wharf-bank @brisk", 0)
	require.NoError(t, err)
	assert.Equal(t, KindTransfer, r.Kind)
	assert.Equal(t, []Account{wharf, brisk}, r.Accounts)
	assert.Equal(t, ledger.SplitInput{AccountID: 11, Currency: "USD", Amount: -50000, Value: -50000}, r.Input.Splits[0])
	assert.Equal(t, ledger.SplitInput{AccountID: 10, Currency: "USD", Amount: 50000, Value: 50000, Tags: []string{"cc-payment"}}, r.Input.Splits[1])
	assert.NoError(t, ledger.Validate(ledger.Normalize(r.Input), nil))
}

func TestResolveUsesAccountCurrencyAndDecimals(t *testing.T) {
	r, err := resolve(t, "500 Internet bill #internet @bravo", 0)
	require.NoError(t, err)
	assert.Equal(t, "MXN", r.Currency)
	assert.EqualValues(t, 50000, r.Amount)

	r, err = resolve(t, "1500 ramen @yen-wallet", 0)
	require.NoError(t, err)
	assert.Equal(t, "JPY", r.Currency)
	assert.EqualValues(t, 1500, r.Amount, "yen has no minor unit")

	_, err = resolve(t, "15.5 ramen @yen-wallet", 0)
	assert.ErrorContains(t, err, "decimal places")
}

func TestResolveDefaultAccount(t *testing.T) {
	r, err := resolve(t, "3 gum #snack", brisk.ID)
	require.NoError(t, err)
	assert.Equal(t, []Account{brisk}, r.Accounts)

	_, err = resolve(t, "3 gum #snack", 0)
	assert.ErrorContains(t, err, "add an @account")

	_, err = resolve(t, "3 gum", old.ID) // archived default is not usable
	assert.ErrorContains(t, err, "add an @account")

	// An explicit account beats the default.
	r, err = resolve(t, "3 gum @neo", brisk.ID)
	require.NoError(t, err)
	assert.Equal(t, []Account{neo}, r.Accounts)
}

func TestResolveAccountMatching(t *testing.T) {
	tests := []struct {
		ref  string
		want Account
	}{
		{"@brisk", brisk},
		{"@bri", brisk},              // unique prefix
		{"@wharf-bank", wharf},       // exact beats being a prefix of wharf-bank-active-cash
		{"@wharf-bank-a", wharfCash}, // prefix
		{"@neo", neo},                // exact beats neo-credit
		{"@neo-c", neoCredit},
		{"@Bravo", bravo},
	}
	for _, tc := range tests {
		t.Run(tc.ref, func(t *testing.T) {
			r, err := resolve(t, "3 x "+tc.ref, 0)
			require.NoError(t, err)
			assert.Equal(t, tc.want, r.Accounts[0])
		})
	}

	_, err := resolve(t, "3 x @w", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "@w is ambiguous")
	assert.Contains(t, err.Error(), "@wharf-bank, @wharf-bank-active-cash")

	_, err = resolve(t, "3 x @zzz", 0)
	assert.ErrorContains(t, err, "no account matches @zzz")

	_, err = resolve(t, "3 x @old-card", 0)
	assert.ErrorContains(t, err, "no account matches", "archived accounts are not selectable")

	_, err = resolve(t, "3 x @expenses", 0)
	assert.ErrorContains(t, err, "no account matches", "built-ins are not selectable")
}

func TestResolveRejections(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"same account twice", "5 x @brisk @brisk", "two different accounts"},
		{"income with two accounts", "+5 x @brisk @wharf-bank", "drop the +"},
		{"cross-currency transfer", "5 x @brisk @neo", "currency conversion"},
		{"zero amount", "0 x @brisk", "greater than zero"},
		{"zero decimal amount", "0.00 x @brisk", "greater than zero"},
		{"parse problem surfaces", "-5 x @brisk", "positive amount"},
		{"no description", "5 @brisk", "add a description"},
		{"too many decimals", "5.123 x @brisk", "decimal places"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolve(t, tc.in, 0)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			var qe *Error
			assert.ErrorAs(t, err, &qe)
		})
	}
}

func TestResolveReportsAllProblems(t *testing.T) {
	_, err := resolve(t, "5.123 x @zzz", 0)
	var qe *Error
	require.ErrorAs(t, err, &qe)
	assert.Len(t, qe.Problems, 1, "amount is not checked when the account is unknown, to avoid noise")

	_, err = Resolve(Parse("5 x @zzz #", today), all, 0)
	require.ErrorAs(t, err, &qe)
	assert.Len(t, qe.Problems, 2) // bad tag + unknown account
}

func TestResolveMissingBuiltins(t *testing.T) {
	_, err := Resolve(Parse("5 x @brisk", today), []Account{brisk}, 0)
	assert.ErrorContains(t, err, "Expenses account is missing")
	_, err = Resolve(Parse("+5 x @brisk", today), []Account{brisk}, 0)
	assert.ErrorContains(t, err, "Income account is missing")
}

func TestParseAccountFirstAndAmountAnywhere(t *testing.T) {
	tests := []struct {
		name, in string
		want     Parsed
	}{
		{
			"account, description, amount (the new natural order)",
			"@brisk Dairy Queen 12.50 #fast-food",
			Parsed{Amount: "12.50", Description: "Dairy Queen", Tags: []string{"fast-food"}, Accounts: []string{"brisk"}, Date: "2026-10-01"},
		},
		{
			"income marker on a trailing amount",
			"@summit FNDXX Dividends +45.20 #dividends",
			Parsed{Income: true, Amount: "45.20", Description: "FNDXX Dividends", Tags: []string{"dividends"}, Accounts: []string{"summit"}, Date: "2026-10-01"},
		},
		{
			"account, amount, description",
			"@brisk 12.50 coffee",
			Parsed{Amount: "12.50", Description: "coffee", Accounts: []string{"brisk"}, Date: "2026-10-01"},
		},
		{
			"description then amount, nothing else",
			"coffee 5",
			Parsed{Amount: "5", Description: "coffee", Date: "2026-10-01"},
		},
		{
			"the last number wins when the description has numbers too",
			"@brisk 2 Guys Pizza 10",
			Parsed{Amount: "10", Description: "2 Guys Pizza", Accounts: []string{"brisk"}, Date: "2026-10-01"},
		},
		{
			"an amount in the middle, words after it",
			"Walmart 65.00 groceries @brisk",
			Parsed{Amount: "65.00", Description: "Walmart groceries", Accounts: []string{"brisk"}, Date: "2026-10-01"},
		},
		{
			"a leading amount still wins (the original order)",
			"5 7-11 run 2 for 1",
			Parsed{Amount: "5", Description: "7-11 run 2 for 1", Date: "2026-10-01"},
		},
		{
			"date and tags anywhere",
			"@brisk yesterday Lunch #food 14.20",
			Parsed{Amount: "14.20", Description: "Lunch", Tags: []string{"food"}, Accounts: []string{"brisk"}, Date: "2026-09-30", DateGiven: true},
		},
		{
			"transfer with two accounts first",
			"@wharf-bank @brisk Card payment 500",
			Parsed{Amount: "500", Description: "Card payment", Accounts: []string{"wharf-bank", "brisk"}, Date: "2026-10-01"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Parse(tc.in, today)
			assert.True(t, got.OK(), "problems: %v", got.Problems)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParseAccountOnlyIsAWorkInProgress(t *testing.T) {
	// What the input holds right after a successful add: just the remembered account.
	got := Parse("@wharf-bank ", today)
	assert.False(t, got.OK())
	assert.Equal(t, []string{"wharf-bank"}, got.Accounts)
	assert.Equal(t, "", got.Description)

	got = Parse("@wharf-bank fndxx div", today)
	assert.Equal(t, "fndxx div", got.Description, "half-typed description is kept for live suggestions")
	assert.Contains(t, got.Problems[0], "add an amount")
}

func TestParseNegativeAmountAnywhere(t *testing.T) {
	got := Parse("@brisk refund -12.50", today)
	require.False(t, got.OK())
	assert.Contains(t, got.Problems[0], "positive amount")
}

func TestParseYYMMDDDates(t *testing.T) {
	tests := []struct {
		name, in, date, amount, desc string
	}{
		{"date last", "@brisk coffee 4.50 261001", "2026-10-01", "4.50", "coffee"},
		{"date right after the account (the pre-filled form)", "@brisk 260924 coffee 4.50", "2026-09-24", "4.50", "coffee"},
		{"date first, legacy amount after", "260924 4.50 coffee @brisk", "2026-09-24", "4.50", "coffee"},
		{"leap day", "@brisk x 4 280229", "2028-02-29", "4", "x"},
		{"year 2039 is the edge", "@brisk x 4 391231", "2039-12-31", "4", "x"},
		{"yen amount of six digits stays an amount", "@yen withdrawal 100000", "2026-10-01", "100000", "withdrawal"},
		{"yen amount plus a date", "@yen withdrawal 100000 260611", "2026-06-11", "100000", "withdrawal"},
		{"a won amount outside 2020-2039 is an amount", "@kbank flight 713937", "2026-10-01", "713937", "flight"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Parse(tc.in, today)
			require.True(t, got.OK(), "problems: %v", got.Problems)
			assert.Equal(t, tc.date, got.Date)
			assert.Equal(t, tc.amount, got.Amount)
			assert.Equal(t, tc.desc, got.Description)
		})
	}
	assert.True(t, Parse("@brisk x 4 260924", today).DateGiven)
	assert.False(t, Parse("@brisk x 4", today).DateGiven)
}

func TestParseTheRememberedAccountAndDateAlone(t *testing.T) {
	// What the input holds right after an add: just the remembered account and date.
	got := Parse("@wharf-bank 260924 ", today)
	assert.False(t, got.OK(), "still needs a description and amount")
	assert.Equal(t, "2026-09-24", got.Date)
	assert.True(t, got.DateGiven)
	assert.Equal(t, "", got.Amount, "the date is not mistaken for an amount")
	assert.Equal(t, "", got.Description)

	got = Parse("@wharf-bank 260924 fndxx div", today)
	assert.Equal(t, "fndxx div", got.Description)
	assert.Equal(t, "2026-09-24", got.Date)
}

func TestParseImpossibleYYMMDDIsNeverAnAmount(t *testing.T) {
	// 261032 (day 32) next to a real amount is a typo'd date: refuse it rather than book $261,032.
	for _, in := range []string{"@brisk coffee 4.50 261032", "@brisk coffee 261032 4.50", "261032 @brisk coffee 4.50", "@brisk coffee 4.50 261301", "@brisk coffee 4.50 260230"} {
		got := Parse(in, today)
		require.False(t, got.OK(), in)
		assert.Contains(t, got.Problems[0], "isn't a real date", in)
		assert.Equal(t, "4.50", got.Amount, "the real amount is still understood: %s", in)
	}
	// With no other number it can only be an amount (a 300000 yen withdrawal).
	got := Parse("@yen withdrawal 300000", today)
	require.True(t, got.OK(), "problems: %v", got.Problems)
	assert.Equal(t, "300000", got.Amount)
	// ...and comma-grouped amounts are never dates.
	got = Parse("@yen withdrawal 300,000 260611", today)
	require.True(t, got.OK(), "problems: %v", got.Problems)
	assert.Equal(t, "300,000", got.Amount)
	assert.Equal(t, "2026-06-11", got.Date)
}
