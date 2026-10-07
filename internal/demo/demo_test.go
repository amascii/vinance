package demo_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/demo"
	"github.com/amascii/vinance/internal/ledger"
	"github.com/amascii/vinance/internal/testutil"
)

func TestSeedBuildsAUsableDemoLedger(t *testing.T) {
	ctx := context.Background()
	conn := testutil.NewDB(t)

	res, err := demo.Seed(ctx, conn)
	require.NoError(t, err)

	svc := ledger.NewService(conn)
	accounts, err := svc.ListAccounts(ctx)
	require.NoError(t, err)
	var real []string
	for _, a := range accounts {
		real = append(real, a.Name)
	}
	assert.ElementsMatch(t, []string{"Checking", "Savings", "Rewards Card", "Peso Account"}, real)

	split, err := svc.Get(ctx, res.SplitReceiptID)
	require.NoError(t, err)
	assert.Equal(t, "Grocery Mart", split.Description)
	assert.Len(t, split.Splits, 4, "card line plus three tagged item lines")

	today := demo.Today.Format("2006-01-02")
	page, err := svc.List(ctx, ledger.Filter{Limit: 1})
	require.NoError(t, err)
	require.NotEmpty(t, page.Transactions)
	assert.LessOrEqual(t, page.Transactions[0].Date, today, "nothing is dated after the demo's today")

	n, err := svc.Count(ctx, ledger.Filter{Imbalance: true})
	require.NoError(t, err)
	assert.Equal(t, 1, n, "one uncategorised charge for the needs-fixing banner")

	due, err := svc.DueCount(ctx, today)
	require.NoError(t, err)
	assert.Equal(t, 2, due)

	budgets, err := svc.ListBudgets(ctx)
	require.NoError(t, err)
	assert.Len(t, budgets, 5)
}

func TestSeedIsDeterministic(t *testing.T) {
	ctx := context.Background()
	count := func() int {
		conn := testutil.NewDB(t)
		_, err := demo.Seed(ctx, conn)
		require.NoError(t, err)
		n, err := ledger.NewService(conn).Count(ctx, ledger.Filter{})
		require.NoError(t, err)
		return n
	}
	assert.Equal(t, count(), count())
}
