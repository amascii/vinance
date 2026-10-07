package ledger_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/ledger"
)

func monthly(anchor string) ledger.Schedule {
	return ledger.Schedule{Freq: ledger.Monthly, Every: 1, Anchor: anchor}
}

// rentFor builds the transaction a rule's text would resolve to for a due date.
func (e env) rentFor(date string) ledger.TxnInput {
	return ledger.TxnInput{
		Date: date, Description: "Rent", Currency: "USD",
		Splits: []ledger.SplitInput{
			{AccountID: e.brisk, Currency: "USD", Amount: -150000, Value: -150000},
			{AccountID: e.expense, Currency: "USD", Amount: 150000, Value: 150000, Tags: []string{"rent"}},
		},
	}
}

func TestRuleCRUD(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	r, err := e.svc.CreateRule(ctx, "  1500 Rent #rent @brisk ", monthly("2026-10-01"))
	require.NoError(t, err)
	assert.Equal(t, "1500 Rent #rent @brisk", r.Text)
	assert.True(t, r.Active)
	assert.Equal(t, 0, r.NextIndex)
	assert.Equal(t, "2026-10-01", r.NextDue())

	got, err := e.svc.GetRule(ctx, r.ID)
	require.NoError(t, err)
	assert.Equal(t, r, got)

	_, err = e.svc.GetRule(ctx, 999)
	assert.ErrorIs(t, err, ledger.ErrRuleNotFound)

	e.svc.CreateRule(ctx, "9.99 Streaming #subscriptions @brisk", ledger.Schedule{Freq: ledger.Monthly, Every: 1, Anchor: "2026-10-15", End: "2027-03-15"})
	rules, err := e.svc.ListRules(ctx)
	require.NoError(t, err)
	require.Len(t, rules, 2)
	assert.Equal(t, "1500 Rent #rent @brisk", rules[0].Text, "ordered by text")
	assert.Equal(t, "9.99 Streaming #subscriptions @brisk", rules[1].Text)
	assert.Equal(t, "2027-03-15", rules[1].Schedule.End)

	require.NoError(t, e.svc.DeleteRule(ctx, r.ID))
	assert.ErrorIs(t, e.svc.DeleteRule(ctx, r.ID), ledger.ErrRuleNotFound)
}

func TestRuleValidation(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	_, err := e.svc.CreateRule(ctx, "  ", monthly("2026-10-01"))
	assert.ErrorIs(t, err, ledger.ErrInvalidRule)
	_, err = e.svc.CreateRule(ctx, "5 x @brisk", ledger.Schedule{Freq: "daily", Every: 1, Anchor: "2026-10-01"})
	assert.ErrorIs(t, err, ledger.ErrInvalidRule)
	_, err = e.svc.CreateRule(ctx, "5 x @brisk", monthly("nope"))
	assert.ErrorIs(t, err, ledger.ErrInvalidRule)
	rules, _ := e.svc.ListRules(ctx)
	assert.Empty(t, rules)
}

func TestDueListsOverdueOccurrencesOldestFirst(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	rent, _ := e.svc.CreateRule(ctx, "1500 Rent @brisk", monthly("2026-08-01"))
	claude, _ := e.svc.CreateRule(ctx, "9.99 Streaming @brisk", monthly("2026-09-15"))
	paused, _ := e.svc.CreateRule(ctx, "5 Paused @brisk", monthly("2026-01-01"))
	future, _ := e.svc.CreateRule(ctx, "5 Future @brisk", monthly("2026-12-01"))
	require.NoError(t, e.svc.SetRuleActive(ctx, paused.ID, false))
	_ = future

	due, err := e.svc.Due(ctx, "2026-10-10")
	require.NoError(t, err)
	var got []string
	for _, d := range due {
		got = append(got, d.Date+" "+d.Rule.Text)
	}
	assert.Equal(t, []string{
		"2026-08-01 1500 Rent @brisk", "2026-09-01 1500 Rent @brisk", "2026-09-15 9.99 Streaming @brisk", "2026-10-01 1500 Rent @brisk",
	}, got, "overdue months are each listed; paused and future rules are not")
	n, err := e.svc.DueCount(ctx, "2026-10-10")
	require.NoError(t, err)
	assert.Equal(t, 4, n)
	_ = rent
	_ = claude

	due, _ = e.svc.Due(ctx, "2026-07-31")
	assert.Empty(t, due, "nothing is due before the first occurrence (the paused rule aside)")
}

func TestDueRespectsEndDateAndCap(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.svc.CreateRule(ctx, "5 Ends @brisk", ledger.Schedule{Freq: ledger.Monthly, Every: 1, Anchor: "2026-01-01", End: "2026-03-15"})
	due, err := e.svc.Due(ctx, "2026-12-31")
	require.NoError(t, err)
	assert.Len(t, due, 3, "Jan 1, Feb 1, Mar 1; nothing after the end date")

	e2 := setup(t)
	e2.svc.CreateRule(ctx, "1 Old @brisk", ledger.Schedule{Freq: ledger.Weekly, Every: 1, Anchor: "2020-01-06"})
	due, _ = e2.svc.Due(ctx, "2026-10-10")
	assert.Len(t, due, 12, "a long-neglected rule lists 12 at a time rather than hundreds")
}

func TestAcceptDueCreatesTheTransactionAndAdvances(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	r, _ := e.svc.CreateRule(ctx, "1500 Rent #rent @brisk", monthly("2026-09-01"))

	id, err := e.svc.AcceptDue(ctx, r.ID, "2026-09-01", e.rentFor("2026-09-01"))
	require.NoError(t, err)
	got, err := e.svc.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "Rent", got.Description)
	assert.Equal(t, "2026-09-01", got.Date)

	after, _ := e.svc.GetRule(ctx, r.ID)
	assert.Equal(t, 1, after.NextIndex)
	assert.Equal(t, "2026-10-01", after.NextDue())

	var rid int64
	var rfor string
	require.NoError(t, e.db.QueryRow("SELECT recurring_id, recurring_for FROM transactions WHERE id = ?", id).Scan(&rid, &rfor))
	assert.Equal(t, r.ID, rid)
	assert.Equal(t, "2026-09-01", rfor)
}

func TestAcceptDueIsIdempotentAndGuardsStalePages(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	r, _ := e.svc.CreateRule(ctx, "1500 Rent #rent @brisk", monthly("2026-09-01"))

	_, err := e.svc.AcceptDue(ctx, r.ID, "2026-09-01", e.rentFor("2026-09-01"))
	require.NoError(t, err)

	// A double-click / reloaded form posts the same occurrence again.
	_, err = e.svc.AcceptDue(ctx, r.ID, "2026-09-01", e.rentFor("2026-09-01"))
	assert.ErrorIs(t, err, ledger.ErrNotDue)
	// An occurrence that isn't next (a stale page, or a future date).
	_, err = e.svc.AcceptDue(ctx, r.ID, "2026-11-01", e.rentFor("2026-11-01"))
	assert.ErrorIs(t, err, ledger.ErrNotDue)
	assert.Equal(t, 1, count(t, e.db, "transactions"), "no duplicates")

	_, err = e.svc.AcceptDue(ctx, 999, "2026-09-01", e.rentFor("2026-09-01"))
	assert.ErrorIs(t, err, ledger.ErrRuleNotFound)
}

func TestAcceptDueIsAtomic(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	r, _ := e.svc.CreateRule(ctx, "1500 Rent #rent @brisk", monthly("2026-09-01"))

	bad := e.rentFor("2026-09-01")
	bad.Splits[1].Amount, bad.Splits[1].Value = 1, 1 // doesn't balance
	_, err := e.svc.AcceptDue(ctx, r.ID, "2026-09-01", bad)
	require.ErrorIs(t, err, ledger.ErrUnbalanced)
	after, _ := e.svc.GetRule(ctx, r.ID)
	assert.Equal(t, 0, after.NextIndex, "the rule did not advance when the transaction failed")
	assert.Equal(t, 0, count(t, e.db, "transactions"))
}

func TestSkipDue(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	r, _ := e.svc.CreateRule(ctx, "1500 Rent @brisk", monthly("2026-09-01"))

	require.NoError(t, e.svc.SkipDue(ctx, r.ID, "2026-09-01"))
	after, _ := e.svc.GetRule(ctx, r.ID)
	assert.Equal(t, "2026-10-01", after.NextDue())
	assert.Equal(t, 0, count(t, e.db, "transactions"))
	assert.ErrorIs(t, e.svc.SkipDue(ctx, r.ID, "2026-09-01"), ledger.ErrNotDue, "skipping twice does nothing")
}

func TestPausedRulesCannotBeAccepted(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	r, _ := e.svc.CreateRule(ctx, "1500 Rent @brisk", monthly("2026-09-01"))
	require.NoError(t, e.svc.SetRuleActive(ctx, r.ID, false))
	_, err := e.svc.AcceptDue(ctx, r.ID, "2026-09-01", e.rentFor("2026-09-01"))
	assert.ErrorIs(t, err, ledger.ErrNotDue)

	require.NoError(t, e.svc.SetRuleActive(ctx, r.ID, true))
	_, err = e.svc.AcceptDue(ctx, r.ID, "2026-09-01", e.rentFor("2026-09-01"))
	assert.NoError(t, err, "on resume the missed occurrence is offered")
	assert.ErrorIs(t, e.svc.SetRuleActive(ctx, 999, true), ledger.ErrRuleNotFound)
}

func TestEndedRuleOffersNothing(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	r, _ := e.svc.CreateRule(ctx, "5 x @brisk", ledger.Schedule{Freq: ledger.Monthly, Every: 1, Anchor: "2026-09-01", End: "2026-09-30"})
	require.NoError(t, e.svc.SkipDue(ctx, r.ID, "2026-09-01"))
	after, _ := e.svc.GetRule(ctx, r.ID)
	assert.Equal(t, "", after.NextDue(), "the series is over")
	err := e.svc.SkipDue(ctx, r.ID, "2026-10-01")
	assert.ErrorIs(t, err, ledger.ErrNotDue)
}

func TestUpdateRule(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	r, _ := e.svc.CreateRule(ctx, "1500 Rent @brisk", monthly("2026-01-01"))
	require.NoError(t, e.svc.SkipDue(ctx, r.ID, "2026-01-01"))
	require.NoError(t, e.svc.SkipDue(ctx, r.ID, "2026-02-01"))

	// Text-only change keeps your place.
	got, err := e.svc.UpdateRule(ctx, r.ID, "1550 Rent @brisk", monthly("2026-01-01"), "2026-10-10")
	require.NoError(t, err)
	assert.Equal(t, "1550 Rent @brisk", got.Text)
	assert.Equal(t, 2, got.NextIndex)
	assert.Equal(t, "2026-03-01", got.NextDue())

	// Adding an end date also keeps your place.
	got, err = e.svc.UpdateRule(ctx, r.ID, "1550 Rent @brisk", ledger.Schedule{Freq: ledger.Monthly, Every: 1, Anchor: "2026-01-01", End: "2027-01-01"}, "2026-10-10")
	require.NoError(t, err)
	assert.Equal(t, 2, got.NextIndex)

	// Moving the day restarts at the first occurrence on/after today: old dates aren't resurrected.
	got, err = e.svc.UpdateRule(ctx, r.ID, "1550 Rent @brisk", monthly("2026-01-05"), "2026-10-10")
	require.NoError(t, err)
	assert.Equal(t, "2026-11-05", got.NextDue())
	got, err = e.svc.UpdateRule(ctx, r.ID, "1550 Rent @brisk", monthly("2026-01-05"), "2026-10-05")
	require.NoError(t, err)
	assert.Equal(t, "2026-11-05", got.NextDue(), "unchanged schedule: no reset")
	got, err = e.svc.UpdateRule(ctx, r.ID, "1550 Rent @brisk", ledger.Schedule{Freq: ledger.Weekly, Every: 1, Anchor: "2026-01-05"}, "2026-10-05")
	require.NoError(t, err)
	assert.Equal(t, "2026-10-05", got.NextDue(), "a date equal to today is still due today")

	_, err = e.svc.UpdateRule(ctx, r.ID, "", monthly("2026-01-05"), "2026-10-05")
	assert.ErrorIs(t, err, ledger.ErrInvalidRule)
	_, err = e.svc.UpdateRule(ctx, 999, "x", monthly("2026-01-05"), "2026-10-05")
	assert.ErrorIs(t, err, ledger.ErrRuleNotFound)
}

func TestChangedScheduleCannotDoubleBookAnOccurrence(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	r, _ := e.svc.CreateRule(ctx, "1500 Rent @brisk", monthly("2026-09-01"))
	_, err := e.svc.AcceptDue(ctx, r.ID, "2026-09-01", e.rentFor("2026-09-01"))
	require.NoError(t, err)

	// Re-anchor so that 2026-09-01 comes up again as an occurrence: it must be refused, not duplicated.
	_, err = e.svc.UpdateRule(ctx, r.ID, "1500 Rent @brisk", ledger.Schedule{Freq: ledger.Weekly, Every: 1, Anchor: "2026-08-25"}, "2026-08-30")
	require.NoError(t, err)
	got, _ := e.svc.GetRule(ctx, r.ID)
	require.Equal(t, "2026-09-01", got.NextDue())
	_, err = e.svc.AcceptDue(ctx, r.ID, "2026-09-01", e.rentFor("2026-09-01"))
	assert.ErrorIs(t, err, ledger.ErrAlreadyAdded)
	assert.Equal(t, 1, count(t, e.db, "transactions"))
}

func TestDeletingARuleKeepsItsTransactions(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	r, _ := e.svc.CreateRule(ctx, "1500 Rent @brisk", monthly("2026-09-01"))
	id, err := e.svc.AcceptDue(ctx, r.ID, "2026-09-01", e.rentFor("2026-09-01"))
	require.NoError(t, err)
	require.NoError(t, e.svc.DeleteRule(ctx, r.ID))

	got, err := e.svc.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "Rent", got.Description)
	var rid *int64
	require.NoError(t, e.db.QueryRow("SELECT recurring_id FROM transactions WHERE id = ?", id).Scan(&rid))
	assert.Nil(t, rid, "the link is cleared, the transaction stays")
}
