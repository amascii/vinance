package web

import (
	"net/http"
	"net/url"
	"sort"

	"github.com/amascii/vinance/internal/ledger"
	"github.com/amascii/vinance/internal/web/views"
)

func (s *Server) accounts(w http.ResponseWriter, r *http.Request) {
	bs, err := s.ledger.BalanceSheet(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	m := views.Balances{
		NetWorth:      ledger.Format(bs.NetWorthUSD, "USD"),
		NetNegative:   bs.NetWorthUSD < 0,
		Assets:        ledger.Format(bs.AssetsUSD, "USD"),
		Owed:          ledger.Format(bs.OwedUSD, "USD"),
		AssetRows:     balanceRows(bs.Assets),
		LiabilityRows: balanceRows(bs.Liabilities),
		MissingRates:  bs.MissingRates,
	}
	// Say which rates were used, for the currencies that actually appear.
	used := map[string]bool{}
	for _, list := range [][]ledger.AccountBalance{bs.Assets, bs.Liabilities} {
		for _, a := range list {
			if a.Rate != "" && a.Balance != 0 {
				used[a.Currency] = true
			}
		}
	}
	for cur := range used {
		m.RateNotes = append(m.RateNotes, cur+" as of "+bs.RateDates[cur])
	}
	sort.Strings(m.RateNotes)
	s.render(w, r, http.StatusOK, views.BalancesPage(m))
}

func balanceRows(list []ledger.AccountBalance) []views.BalanceRow {
	rows := make([]views.BalanceRow, len(list))
	for i, a := range list {
		row := views.BalanceRow{
			Name: a.Name, Href: "/transactions?account=" + url.QueryEscape(a.Slug),
			Balance: ledger.Format(a.Display(), a.Currency), Zero: a.Balance == 0, Archived: a.Archived,
		}
		switch {
		case a.Currency == "USD" || a.Balance == 0:
		case a.HasUSD:
			row.USD = "≈ " + ledger.Format(a.DisplayUSD(), "USD")
		default:
			row.NoRate = true
		}
		rows[i] = row
	}
	return rows
}
