package ledger_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/ledger"
)

func TestSetListDeleteBudget(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	b, err := e.svc.SetBudget(ctx, "  #Groceries ", 40000)
	require.NoError(t, err)
	assert.Equal(t, "groceries", b.Tag)
	assert.EqualValues(t, 40000, b.MonthlyUSD)
	assert.NotZero(t, b.ID)

	// Setting again changes the amount (one budget per tag).
	b2, err := e.svc.SetBudget(ctx, "groceries", 45000)
	require.NoError(t, err)
	assert.Equal(t, b.ID, b2.ID)
	_, err = e.svc.SetBudget(ctx, "fast-food", 10000)
	require.NoError(t, err)

	list, err := e.svc.ListBudgets(ctx)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, "fast-food", list[0].Tag)
	assert.EqualValues(t, 45000, list[1].MonthlyUSD)

	require.NoError(t, e.svc.DeleteBudget(ctx, "groceries"))
	assert.ErrorIs(t, e.svc.DeleteBudget(ctx, "groceries"), ledger.ErrBudgetNotFound)
	assert.ErrorIs(t, e.svc.DeleteBudget(ctx, "never-existed"), ledger.ErrBudgetNotFound)
	_, exists, _ := e.svc.TagUsage(ctx, "groceries")
	assert.True(t, exists, "removing a budget doesn't remove the tag")
}

func TestSetBudgetValidation(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	for _, tc := range []struct {
		tag string
		usd int64
	}{{"", 100}, {"  # ", 100}, {"food", 0}, {"food", -5}} {
		_, err := e.svc.SetBudget(ctx, tc.tag, tc.usd)
		assert.ErrorIs(t, err, ledger.ErrInvalidBudget, "%+v", tc)
	}
	list, _ := e.svc.ListBudgets(ctx)
	assert.Empty(t, list)
	tags, _ := e.svc.ListTags(ctx)
	assert.Empty(t, tags, "a rejected budget leaves no stray tag")
}

func TestBudgetCreatesAnUnusedTag(t *testing.T) {
	e := setup(t)
	_, err := e.svc.SetBudget(context.Background(), "new-tag", 5000)
	require.NoError(t, err)
	tags, _ := e.svc.ListTags(context.Background())
	assert.Equal(t, []ledger.TagInfo{{Name: "new-tag", Splits: 0}}, tags)
}

func TestBudgetStatus(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	neo := e.account(t, "Neo", "asset", "MXN")
	e.price(t, "MXN", "2026-01-01", "0.05")

	e.spendOn(t, e.brisk, tagSpend{"2026-09-30", "USD", 9900, []string{"groceries"}}) // last month: excluded
	e.spendOn(t, e.brisk, tagSpend{"2026-10-02", "USD", 20000, []string{"groceries", "ingredients"}})
	e.spendOn(t, neo, tagSpend{"2026-10-05", "MXN", 100000, []string{"groceries"}}) // MX$1,000 = $50
	e.spendOn(t, e.brisk, tagSpend{"2026-10-06", "USD", 3000, []string{"fast-food"}})
	e.spendOn(t, e.brisk, tagSpend{"2026-10-07", "USD", 500, nil})                    // untagged, but counts toward total spending
	e.spendOn(t, e.brisk, tagSpend{"2026-11-01", "USD", 7000, []string{"groceries"}}) // next month: excluded

	_, err := e.svc.SetBudget(ctx, "groceries", 30000)
	require.NoError(t, err)
	_, err = e.svc.SetBudget(ctx, "fast-food", 2000)
	require.NoError(t, err)
	_, err = e.svc.SetBudget(ctx, "travel", 50000) // never spent
	require.NoError(t, err)

	st, err := e.svc.BudgetStatus(ctx, "2026-10")
	require.NoError(t, err)
	assert.Equal(t, "2026-10-01", st.From)
	assert.Equal(t, "2026-10-31", st.To)
	assert.EqualValues(t, 30000+2000+50000, st.TotalBudget)
	assert.EqualValues(t, 20000+5000+3000+500, st.TotalSpending, "every spending line of the month, budgeted or not")

	require.Len(t, st.Lines, 3)
	// Ordered by percent used: fast-food 150%, groceries 83%, travel 0%.
	assert.Equal(t, "fast-food", st.Lines[0].Tag)
	assert.EqualValues(t, 3000, st.Lines[0].SpentUSD)
	assert.Equal(t, 150, st.Lines[0].Percent())
	assert.EqualValues(t, -1000, st.Lines[0].Remaining(), "over budget by $10")

	assert.Equal(t, "groceries", st.Lines[1].Tag)
	assert.EqualValues(t, 25000, st.Lines[1].SpentUSD, "$200 + MX$1,000 at 0.05 = $250; September and November excluded")
	assert.Equal(t, 2, st.Lines[1].Items)
	assert.Equal(t, 83, st.Lines[1].Percent())
	assert.EqualValues(t, 5000, st.Lines[1].Remaining())

	assert.Equal(t, "travel", st.Lines[2].Tag)
	assert.Zero(t, st.Lines[2].SpentUSD)
	assert.Equal(t, 0, st.Lines[2].Percent())

	// Another month sees only its own spending.
	st, err = e.svc.BudgetStatus(ctx, "2026-09")
	require.NoError(t, err)
	assert.EqualValues(t, 9900, st.TotalSpending)
	var groceries ledger.BudgetLine
	for _, l := range st.Lines {
		if l.Tag == "groceries" {
			groceries = l
		}
	}
	assert.EqualValues(t, 9900, groceries.SpentUSD)

	_, err = e.svc.BudgetStatus(ctx, "October")
	assert.Error(t, err)
}

func TestBudgetLinePercent(t *testing.T) {
	line := func(spent, budget int64) ledger.BudgetLine {
		return ledger.BudgetLine{Budget: ledger.Budget{MonthlyUSD: budget}, SpentUSD: spent}
	}
	assert.Equal(t, 0, line(0, 1000).Percent())
	assert.Equal(t, 0, line(-500, 1000).Percent(), "refunds can't produce a negative percentage")
	assert.Equal(t, 99, line(999, 1000).Percent(), "rounds down, so 100% means the budget is reached")
	assert.Equal(t, 100, line(1000, 1000).Percent())
	assert.Equal(t, 250, line(2500, 1000).Percent())
	assert.EqualValues(t, -1500, line(2500, 1000).Remaining())
}

func TestMonthBoundsAndProgress(t *testing.T) {
	from, to, err := ledger.MonthBounds("2028-02")
	require.NoError(t, err)
	assert.Equal(t, "2028-02-01", from)
	assert.Equal(t, "2028-02-29", to, "leap year")
	_, to, _ = ledger.MonthBounds("2026-12")
	assert.Equal(t, "2026-12-31", to)
	for _, bad := range []string{"", "2026", "2026-13", "2026-1", "2026-10-01"} {
		_, _, err := ledger.MonthBounds(bad)
		assert.Error(t, err, "%q", bad)
	}

	assert.InDelta(t, 0.0, ledger.MonthProgress("2026-10", "2026-09-30"), 1e-9, "month hasn't started")
	assert.InDelta(t, 1.0, ledger.MonthProgress("2026-10", "2026-11-01"), 1e-9, "month is over")
	assert.InDelta(t, 1.0/31.0, ledger.MonthProgress("2026-10", "2026-10-01"), 1e-9)
	assert.InDelta(t, 1.0, ledger.MonthProgress("2026-10", "2026-10-31"), 1e-9)
	assert.InDelta(t, 15.0/30.0, ledger.MonthProgress("2026-09", "2026-09-15"), 1e-9)
}

func TestMergingTagsMovesTheBudget(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.add(t, "2026-10-01", "a", 100, "", "ingredients")
	e.add(t, "2026-10-02", "b", 100, "", "groceries")

	// Only the old tag has a budget: it moves to the survivor.
	_, err := e.svc.SetBudget(ctx, "ingredients", 20000)
	require.NoError(t, err)
	_, err = e.svc.RenameTag(ctx, "ingredients", "groceries")
	require.NoError(t, err)
	list, _ := e.svc.ListBudgets(ctx)
	assert.Equal(t, []ledger.Budget{{ID: list[0].ID, Tag: "groceries", MonthlyUSD: 20000}}, list)

	// Both have budgets: the survivor's is kept, the other's goes away.
	e.add(t, "2026-10-03", "c", 100, "", "snacks")
	_, err = e.svc.SetBudget(ctx, "snacks", 1000)
	require.NoError(t, err)
	_, err = e.svc.RenameTag(ctx, "snacks", "groceries")
	require.NoError(t, err)
	list, _ = e.svc.ListBudgets(ctx)
	require.Len(t, list, 1)
	assert.EqualValues(t, 20000, list[0].MonthlyUSD)

	// A plain rename keeps the budget with the tag.
	_, err = e.svc.RenameTag(ctx, "groceries", "food")
	require.NoError(t, err)
	list, _ = e.svc.ListBudgets(ctx)
	assert.Equal(t, "food", list[0].Tag)
}

func TestDeletingAnUnusedTagRemovesItsBudget(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	_, err := e.svc.SetBudget(ctx, "ghost", 1000)
	require.NoError(t, err)
	require.NoError(t, e.svc.DeleteTag(ctx, "ghost"))
	list, _ := e.svc.ListBudgets(ctx)
	assert.Empty(t, list)
}
