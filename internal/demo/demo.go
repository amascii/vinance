// Package demo fills an empty database with a few months of invented transactions, for the README
// screenshots and for trying the app without any real data. Everything here is synthetic.
package demo

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"time"

	"github.com/amascii/vinance/internal/db/gen"
	"github.com/amascii/vinance/internal/ledger"
)

// Today is the date the demo data is written for; serve the demo with a clock fixed here.
var Today = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

// Result names a few rows the screenshots need to link to.
type Result struct {
	SplitReceiptID int64 // a grocery receipt split across three tags
}

type seeder struct {
	ctx context.Context
	svc *ledger.Service
	rng *rand.Rand

	checking, savings, card, pesos int64
	expense, income, imbalance     int64
	err                            error
	result                         Result
}

// Seed writes the demo accounts, transactions, budgets and recurring rules into an empty, migrated database.
func Seed(ctx context.Context, conn *sql.DB) (Result, error) {
	s := &seeder{ctx: ctx, svc: ledger.NewService(conn), rng: rand.New(rand.NewSource(7))}
	q := gen.New(conn)

	open := func(name, typ, cur string, opening int64) int64 {
		a, err := s.svc.CreateAccount(ctx, ledger.NewAccount{Name: name, Type: typ, Currency: cur, OpeningBalance: opening, OpeningDate: "2026-06-30"})
		if err != nil && s.err == nil {
			s.err = fmt.Errorf("account %q: %w", name, err)
		}
		return a.ID
	}
	s.checking = open("Checking", "asset", "USD", 420000)
	s.savings = open("Savings", "asset", "USD", 1250000)
	s.card = open("Rewards Card", "liability", "USD", 31000)
	s.pesos = open("Peso Account", "asset", "MXN", 1500000)
	if s.err != nil {
		return Result{}, s.err
	}
	for _, b := range []struct {
		typ string
		dst *int64
	}{{"expense", &s.expense}, {"income", &s.income}, {"imbalance", &s.imbalance}} {
		a, err := q.GetBuiltinAccount(ctx, b.typ)
		if err != nil {
			return Result{}, err
		}
		*b.dst = a.ID
	}
	for _, p := range []gen.UpsertPriceParams{
		{Currency: "MXN", Date: "2026-07-01", UsdPerUnit: "0.0545"},
		{Currency: "MXN", Date: "2026-09-01", UsdPerUnit: "0.0560"},
	} {
		if err := q.UpsertPrice(ctx, p); err != nil {
			return Result{}, err
		}
	}

	s.history()
	s.oneOffs()
	s.budgetsAndRules()
	if s.err == nil {
		// Backdate the bookkeeping timestamps so the editor's "created …" line matches the demo dates.
		if _, err := conn.ExecContext(ctx, `UPDATE transactions SET created_at = date || 'T12:00:00Z', updated_at = date || 'T12:00:00Z'`); err != nil {
			s.err = fmt.Errorf("backdate timestamps: %w", err)
		}
	}
	return s.result, s.err
}

// add creates a transaction, remembering the first error.
func (s *seeder) add(date, desc string, splits ...ledger.SplitInput) int64 {
	if s.err != nil {
		return 0
	}
	id, err := s.svc.Create(s.ctx, ledger.TxnInput{Date: date, Description: desc, Currency: "USD", Splits: splits})
	if err != nil {
		s.err = fmt.Errorf("%s %q: %w", date, desc, err)
	}
	return id
}

func usd(account, cents int64, tags ...string) ledger.SplitInput {
	return ledger.SplitInput{AccountID: account, Currency: "USD", Amount: cents, Value: cents, Tags: tags}
}

// spend records an expense paid from account.
func (s *seeder) spend(date, desc string, from, cents int64, tags ...string) int64 {
	return s.add(date, desc, usd(from, -cents), usd(s.expense, cents, tags...))
}

func (s *seeder) earn(date, desc string, to, cents int64, tags ...string) {
	s.add(date, desc, usd(to, cents), usd(s.income, -cents, tags...))
}

func (s *seeder) transfer(date, desc string, from, to, cents int64) {
	s.add(date, desc, usd(from, -cents), usd(to, cents))
}

func day(m time.Month, d int) string {
	return time.Date(2026, m, d, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
}

// pick returns a random element and a random amount in [lo, hi] cents.
func (s *seeder) pick(names []string) string { return names[s.rng.Intn(len(names))] }
func (s *seeder) between(lo, hi int64) int64 { return lo + s.rng.Int63n(hi-lo+1) }

// history writes the regular monthly pattern for July through the demo date.
func (s *seeder) history() {
	for m := time.July; m <= time.September; m++ {
		s.earn(day(m, 1), "Paycheck", s.checking, 285000, "salary")
		s.earn(day(m, 15), "Paycheck", s.checking, 285000, "salary")
		if m != time.September { // the 28th is still in the future on the demo date
			s.earn(day(m, 28), "Savings interest", s.savings, s.between(1500, 2400), "interest")
		}
		s.spend(day(m, 1), "Rent", s.checking, 145000, "rent")
		s.spend(day(m, 5), "Internet", s.checking, 6000, "utilities")
		s.spend(day(m, 10), "Electric", s.checking, s.between(5800, 9600), "utilities")
		s.spend(day(m, 12), "Phone", s.checking, 3500, "utilities")
		s.transfer(day(m, 2), "Move to savings", s.checking, s.savings, 50000)
		if m != time.September {
			s.spend(day(m, 20), "Gym", s.card, 3900, "health")
			s.spend(day(m, 22), "Streaming", s.card, 999, "subscriptions")
			s.transfer(day(m, 28), "Card payment", s.checking, s.card, 65000)
		}
		s.spend(day(m, 3), "Transit pass", s.card, 3300, "transport")
		s.spend(day(m, 15), "Music", s.card, 599, "subscriptions")

		last := 30
		if m == time.September {
			last = 24
		}
		for d := 2; d <= last; d++ {
			date := day(m, d)
			if s.rng.Intn(3) == 0 {
				s.spend(date, s.pick([]string{"Grocery Mart", "FreshCo", "Corner Market"}), s.card, s.between(2200, 7500), "groceries")
			}
			if s.rng.Intn(2) == 0 {
				switch s.rng.Intn(3) {
				case 0:
					s.spend(date, "Coffee Shop", s.card, s.between(400, 650), "dining", "coffee")
				case 1:
					s.spend(date, s.pick([]string{"Noodle House", "Pizza Place", "Taco Stand"}), s.card, s.between(1400, 3200), "dining")
				default:
					s.spend(date, "Lunch Counter", s.card, s.between(900, 1600), "dining")
				}
			}
			if s.rng.Intn(6) == 0 {
				s.spend(date, s.pick([]string{"Rideshare", "Gas Station"}), s.card, s.between(800, 5500), "transport")
			}
			if s.rng.Intn(14) == 0 {
				s.spend(date, s.pick([]string{"Cinema", "Bookshop", "Concert tickets"}), s.card, s.between(1200, 4800), "fun")
			}
			if s.rng.Intn(20) == 0 {
				s.spend(date, "Pharmacy", s.card, s.between(600, 2800), "health")
			}
		}
	}
}

// oneOffs adds the rows the screenshots call attention to: a split receipt, pesos, an imbalance.
func (s *seeder) oneOffs() {
	s.result.SplitReceiptID = s.add(day(time.September, 21), "Grocery Mart",
		usd(s.card, -8745),
		usd(s.expense, 5210, "groceries"),
		usd(s.expense, 2135, "household"),
		usd(s.expense, 1400, "snacks"))

	// A trip paid in pesos: the transaction is in MXN, each expense line tracks its USD cost.
	peso := func(date, desc string, mxn int64, tags ...string) {
		if s.err != nil {
			return
		}
		cents := mxn * 56 / 1000 // 0.0560 USD per peso
		_, err := s.svc.Create(s.ctx, ledger.TxnInput{Date: date, Description: desc, Currency: "MXN", Splits: []ledger.SplitInput{
			{AccountID: s.pesos, Currency: "MXN", Amount: -mxn, Value: -mxn},
			{AccountID: s.expense, Currency: "USD", Amount: cents, Value: mxn, Tags: tags},
		}})
		if err != nil {
			s.err = fmt.Errorf("%s %q: %w", date, desc, err)
		}
	}
	peso(day(time.August, 10), "Hotel", 240000, "travel")
	peso(day(time.August, 11), "Taqueria", 18000, "dining", "travel")
	peso(day(time.August, 12), "Market", 45000, "groceries", "travel")
	peso(day(time.August, 13), "Taxi", 9500, "transport", "travel")
	peso(day(time.August, 15), "Museum", 12000, "fun", "travel")

	// A charge nobody has categorised yet, so the "needs fixing" banner shows.
	s.add(day(time.September, 22), "Unknown charge", usd(s.card, -2340), usd(s.imbalance, 2340))
}

func (s *seeder) budgetsAndRules() {
	if s.err != nil {
		return
	}
	for tag, cents := range map[string]int64{"groceries": 45000, "dining": 25000, "transport": 15000, "subscriptions": 3000, "fun": 8000} {
		if _, err := s.svc.SetBudget(s.ctx, tag, cents); err != nil {
			s.err = fmt.Errorf("budget %q: %w", tag, err)
			return
		}
	}
	monthly := func(anchor string) ledger.Schedule {
		return ledger.Schedule{Freq: ledger.Monthly, Every: 1, Anchor: anchor}
	}
	for _, r := range []struct{ text, anchor string }{
		{"1450 Rent #rent @checking", "2026-10-01"},
		{"9.99 Streaming #subscriptions @rewards-card", "2026-09-22"}, // due: two days ago
		{"39 Gym #health @rewards-card", "2026-09-20"},                // due: four days ago
		{"5.99 Music #subscriptions @rewards-card", "2026-10-15"},
	} {
		if _, err := s.svc.CreateRule(s.ctx, r.text, monthly(r.anchor)); err != nil {
			s.err = fmt.Errorf("rule %q: %w", r.text, err)
			return
		}
	}
}
