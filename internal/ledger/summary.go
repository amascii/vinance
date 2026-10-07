package ledger

// Direction says which way money moved across the user's real accounts.
type Direction string

const (
	DirOut      Direction = "out"      // net outflow (an expense)
	DirIn       Direction = "in"       // net inflow (income, refund, opening balance)
	DirTransfer Direction = "transfer" // moved between real accounts, net zero
	DirOther    Direction = ""         // touches no real account, or nets to zero on one
)

// Summary is the at-a-glance view of a transaction used by list rows.
type Summary struct {
	Direction    Direction
	Net          int64    // net change across real accounts, in the transaction currency (negative = spent)
	Gross        int64    // sum of positive split values, in the transaction currency
	Accounts     []string // real account names in split order, deduplicated
	Tags         []string // union of split tags in first-seen order
	HasImbalance bool     // some split sits in the Imbalance account and needs fixing
}

// Headline is the amount to show for the row: the net for in/out, the moved total for transfers.
func (s Summary) Headline() int64 {
	if s.Direction == DirIn || s.Direction == DirOut {
		return s.Net
	}
	return s.Gross
}

func isReal(accountType string) bool { return accountType == "asset" || accountType == "liability" }

// Summary computes the Summary for a transaction.
func (t Transaction) Summary() Summary {
	var sum Summary
	seenAcct, seenTag := map[string]bool{}, map[string]bool{}
	realSplits := 0
	for _, sp := range t.Splits {
		if sp.Value > 0 {
			sum.Gross += sp.Value
		}
		if sp.AccountType == "imbalance" {
			sum.HasImbalance = true
		}
		if isReal(sp.AccountType) {
			realSplits++
			sum.Net += sp.Value
			if !seenAcct[sp.AccountName] {
				seenAcct[sp.AccountName] = true
				sum.Accounts = append(sum.Accounts, sp.AccountName)
			}
		}
		for _, tag := range sp.Tags {
			if !seenTag[tag] {
				seenTag[tag] = true
				sum.Tags = append(sum.Tags, tag)
			}
		}
	}
	switch {
	case sum.Net < 0:
		sum.Direction = DirOut
	case sum.Net > 0:
		sum.Direction = DirIn
	case realSplits >= 2:
		sum.Direction = DirTransfer
	}
	return sum
}
