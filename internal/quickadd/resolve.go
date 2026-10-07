package quickadd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/amascii/vinance/internal/ledger"
)

// Account is the slice of an account the resolver needs.
type Account struct {
	ID       int64
	Slug     string
	Name     string
	Type     string // asset | liability | income | expense | equity | imbalance
	Currency string // empty for built-ins
	Builtin  bool
	Archived bool
}

// Kind says what a resolved entry is.
type Kind string

const (
	KindExpense  Kind = "expense"
	KindIncome   Kind = "income"
	KindTransfer Kind = "transfer"
)

// Result is a resolved entry: a balanced, validated-shape transaction ready for
// ledger.Service.Create, plus what the preview needs.
type Result struct {
	Kind     Kind
	Input    ledger.TxnInput
	Accounts []Account // the real accounts used, in order (from, to)
	Amount   int64     // minor units of Currency, always positive
	Currency string
}

// Resolve maps a Parsed entry onto real accounts. accounts is every known account;
// defaultAccountID is used when no @account is typed (0 = none). It returns an error listing
// every problem found, including any from Parse.
func Resolve(p Parsed, accounts []Account, defaultAccountID int64) (Result, error) {
	problems := append([]string(nil), p.Problems...)
	fail := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }

	var real []Account
	var expenseAcct, incomeAcct Account
	for _, a := range accounts {
		switch {
		case a.Builtin && a.Type == "expense":
			expenseAcct = a
		case a.Builtin && a.Type == "income":
			incomeAcct = a
		case !a.Builtin && !a.Archived:
			real = append(real, a)
		}
	}

	var used []Account
	for _, ref := range p.Accounts {
		a, err := matchAccount(ref, real)
		if err != nil {
			fail("%v", err)
			continue
		}
		used = append(used, a)
	}
	if len(p.Accounts) == 0 {
		found := false
		for _, a := range real {
			if a.ID == defaultAccountID {
				used, found = []Account{a}, true
			}
		}
		if !found {
			fail("add an @account")
		}
	}
	if len(used) == 2 && used[0].ID == used[1].ID {
		fail("a transfer needs two different accounts")
	}
	if len(used) == 2 && p.Income {
		fail("+ means money coming in; for a transfer between two accounts drop the +")
	}
	if len(used) == 2 && used[0].Currency != used[1].Currency {
		fail("%s is %s but %s is %s; edit the transaction to record a currency conversion",
			used[0].Name, used[0].Currency, used[1].Name, used[1].Currency)
	}

	var amount int64
	if p.Amount != "" && len(used) > 0 {
		var err error
		amount, err = ledger.ParseAmount(p.Amount, used[0].Currency)
		switch {
		case err != nil:
			fail("%v", err)
		case amount <= 0:
			fail("the amount must be greater than zero")
		}
	}
	if len(problems) > 0 {
		return Result{}, &Error{Problems: problems}
	}

	cur := used[0].Currency
	split := func(a Account, amt int64, tags []string) ledger.SplitInput {
		return ledger.SplitInput{AccountID: a.ID, Currency: cur, Amount: amt, Value: amt, Tags: tags}
	}
	in := ledger.TxnInput{Date: p.Date, Description: p.Description, Currency: cur}
	res := Result{Input: in, Accounts: used, Amount: amount, Currency: cur}
	switch {
	case len(used) == 2:
		res.Kind = KindTransfer
		res.Input.Splits = []ledger.SplitInput{split(used[0], -amount, nil), split(used[1], amount, p.Tags)}
	case p.Income:
		if incomeAcct.ID == 0 {
			return Result{}, &Error{Problems: []string{"built-in Income account is missing"}}
		}
		res.Kind = KindIncome
		res.Input.Splits = []ledger.SplitInput{split(used[0], amount, nil), split(incomeAcct, -amount, p.Tags)}
	default:
		if expenseAcct.ID == 0 {
			return Result{}, &Error{Problems: []string{"built-in Expenses account is missing"}}
		}
		res.Kind = KindExpense
		res.Input.Splits = []ledger.SplitInput{split(used[0], -amount, nil), split(expenseAcct, amount, p.Tags)}
	}
	return res, nil
}

// matchAccount finds the real account for a typed reference: exact slug first, otherwise a
// unique slug prefix.
func matchAccount(ref string, real []Account) (Account, error) {
	var prefix []Account
	for _, a := range real {
		if a.Slug == ref {
			return a, nil
		}
		if strings.HasPrefix(a.Slug, ref) {
			prefix = append(prefix, a)
		}
	}
	switch len(prefix) {
	case 0:
		return Account{}, fmt.Errorf("no account matches @%s", ref)
	case 1:
		return prefix[0], nil
	}
	names := make([]string, len(prefix))
	for i, a := range prefix {
		names[i] = "@" + a.Slug
	}
	sort.Strings(names)
	return Account{}, fmt.Errorf("@%s is ambiguous: %s", ref, strings.Join(names, ", "))
}

// Error carries every problem found in an entry.
type Error struct{ Problems []string }

func (e *Error) Error() string { return strings.Join(e.Problems, "; ") }
