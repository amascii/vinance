package web

import (
	"net/url"
	"strconv"

	"github.com/amascii/vinance/internal/ledger"
	"github.com/amascii/vinance/internal/web/views"
)

// rowFor turns a stored transaction into a list row. next is the page to come back to
// after editing.
func rowFor(t ledger.Transaction, next string) views.TxnRow {
	sum := t.Summary()
	amount := ledger.Format(sum.Headline(), t.Currency)
	if sum.Direction == ledger.DirIn {
		amount = "+" + amount
	}
	row := views.TxnRow{
		ID: t.ID, Date: t.Date, Description: t.Description, Direction: string(sum.Direction),
		Amount: amount, Accounts: sum.Accounts, Tags: sum.Tags, HasImbalance: sum.HasImbalance,
		EditURL: "/transactions/" + strconv.FormatInt(t.ID, 10) + "?next=" + url.QueryEscape(next),
	}
	hasMemo := false
	for _, sp := range t.Splits {
		hasMemo = hasMemo || sp.Memo != ""
		row.Splits = append(row.Splits, splitRow(sp))
	}
	row.ShowSplits = len(t.Splits) > 2 || hasMemo
	return row
}

// splitRow renders one split. Real accounts are labelled by name; built-in category splits
// (Expenses, Income, Equity) are identified by their tags and memo instead, falling back to
// the account name when they have neither.
func splitRow(sp ledger.Split) views.SplitRow {
	r := views.SplitRow{Memo: sp.Memo, Tags: sp.Tags, Amount: ledger.Format(sp.Amount, sp.Currency), Warn: sp.AccountType == "imbalance"}
	switch sp.AccountType {
	case "asset", "liability", "imbalance":
		r.Label = sp.AccountName
	default:
		if sp.Memo == "" && len(sp.Tags) == 0 {
			r.Label = sp.AccountName
		}
	}
	return r
}

func rowsFor(ts []ledger.Transaction, next string) []views.TxnRow {
	rows := make([]views.TxnRow, len(ts))
	for i, t := range ts {
		rows[i] = rowFor(t, next)
	}
	return rows
}
