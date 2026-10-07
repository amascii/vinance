package ledger

import (
	"context"
	"sort"
)

// AccountBalance is one real account's balance. Balance is in the account's own currency,
// debit-positive (so a credit card you owe money on is negative); Owed flips liabilities for display.
type AccountBalance struct {
	ID       int64
	Name     string
	Slug     string
	Type     string // asset | liability
	Currency string
	Archived bool
	Balance  int64 // minor units of Currency, debit-positive
	Splits   int64
	// USD is Balance converted at the latest known rate; HasUSD is false when no rate exists.
	USD    int64
	HasUSD bool
	Rate   string // USD per unit used (empty for USD itself or when unknown)
}

// Display is the balance as the user thinks of it: assets as-is, liabilities as the amount owed.
func (b AccountBalance) Display() int64 {
	if b.Type == "liability" {
		return -b.Balance
	}
	return b.Balance
}

// DisplayUSD is Display for the USD equivalent.
func (b AccountBalance) DisplayUSD() int64 {
	if b.Type == "liability" {
		return -b.USD
	}
	return b.USD
}

// BalanceSheet groups the real accounts and totals them in USD.
type BalanceSheet struct {
	Assets       []AccountBalance
	Liabilities  []AccountBalance
	AssetsUSD    int64    // sum of asset balances (USD)
	OwedUSD      int64    // sum of amounts owed on liabilities (USD, positive when you owe)
	NetWorthUSD  int64    // AssetsUSD - OwedUSD
	MissingRates []string // currencies with a non-zero balance but no known rate (excluded from totals)
	RateDates    map[string]string
}

// BalanceSheet computes balances for every real account. Archived accounts are included
// only when they still hold a balance.
func (s *Service) BalanceSheet(ctx context.Context) (BalanceSheet, error) {
	rows, err := s.q.AccountBalances(ctx)
	if err != nil {
		return BalanceSheet{}, err
	}
	prices, err := s.q.LatestPrices(ctx)
	if err != nil {
		return BalanceSheet{}, err
	}
	rates := map[string]string{}
	bs := BalanceSheet{RateDates: map[string]string{}}
	for _, p := range prices {
		rates[p.Currency] = p.UsdPerUnit
		bs.RateDates[p.Currency] = p.Date
	}

	missing := map[string]bool{}
	for _, r := range rows {
		if r.Archived == 1 && r.Balance == 0 {
			continue
		}
		b := AccountBalance{
			ID: r.ID, Name: r.Name, Slug: r.Slug, Type: r.Type, Currency: r.Currency.String,
			Archived: r.Archived == 1, Balance: r.Balance, Splits: r.SplitCount,
		}
		switch rate, known := rates[b.Currency]; {
		case b.Currency == "USD":
			b.USD, b.HasUSD = b.Balance, true
		case known:
			if usd, err := ConvertToUSD(b.Balance, b.Currency, rate); err == nil {
				b.USD, b.HasUSD, b.Rate = usd, true, rate
			}
		}
		if !b.HasUSD && b.Balance != 0 {
			missing[b.Currency] = true
		}
		switch b.Type {
		case "asset":
			bs.Assets = append(bs.Assets, b)
			bs.AssetsUSD += b.USD
		case "liability":
			bs.Liabilities = append(bs.Liabilities, b)
			bs.OwedUSD += b.DisplayUSD()
		}
	}
	bs.NetWorthUSD = bs.AssetsUSD - bs.OwedUSD
	for c := range missing {
		bs.MissingRates = append(bs.MissingRates, c)
	}
	sort.Strings(bs.MissingRates)
	return bs, nil
}

// Movement is how one transaction changed an account: the net of the transaction's lines in
// that account (Delta) and the account's balance after it (Balance), both in the account's own
// currency and debit-positive.
type Movement struct {
	Delta   int64
	Balance int64
}

// RunningBalances returns, for each of the given transactions that touches the account, the
// account's balance right after it. The balance is a running total over the account's whole
// history in (date, id) order, the same order lists use, so it is correct for any page or
// filtered view: only the requested transactions are returned, but earlier ones are counted.
func (s *Service) RunningBalances(ctx context.Context, accountID int64, txnIDs []int64) (map[int64]Movement, error) {
	out := map[int64]Movement{}
	if len(txnIDs) == 0 {
		return out, nil
	}
	args := []any{accountID}
	for _, id := range txnIDs {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, `WITH running AS (
			SELECT t.id AS tid,
			       SUM(s.amount) AS delta,
			       SUM(SUM(s.amount)) OVER (ORDER BY t.date, t.id) AS balance
			FROM splits s JOIN transactions t ON t.id = s.transaction_id
			WHERE s.account_id = ?
			GROUP BY t.id)
		SELECT tid, delta, balance FROM running WHERE tid IN (`+placeholders(len(txnIDs))+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var m Movement
		if err := rows.Scan(&id, &m.Delta, &m.Balance); err != nil {
			return nil, err
		}
		out[id] = m
	}
	return out, rows.Err()
}

// AccountBalance returns an account's current balance (all transactions), debit-positive.
func (s *Service) AccountBalance(ctx context.Context, accountID int64) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, "SELECT COALESCE(SUM(amount), 0) FROM splits WHERE account_id = ?", accountID).Scan(&n)
	return n, err
}
