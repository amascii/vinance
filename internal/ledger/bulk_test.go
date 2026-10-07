package ledger_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/ledger"
)

func TestBulkAddTagsCategoryLinesOnly(t *testing.T) {
	e := setup(t)
	a := e.add(t, "2026-09-01", "a", 100, "")
	b := e.add(t, "2026-09-02", "b", 100, "", "snacks")
	untouched := e.add(t, "2026-09-03", "untouched", 100, "")
	ctx := context.Background()

	res, err := e.svc.BulkApply(ctx, []int64{a, b}, ledger.BulkChange{Op: ledger.BulkAdd, Tag: "#Party Time"})
	require.NoError(t, err)
	assert.Equal(t, ledger.BulkResult{Transactions: 2, Lines: 2}, res)
	assert.Equal(t, map[string][]string{"a:1": {"party-time"}, "b:1": {"party-time", "snacks"}}, tagsOnLines(t, e), "the card line is not tagged, the unselected one is untouched")
	_ = untouched

	// Idempotent: nothing left to add.
	res, err = e.svc.BulkApply(ctx, []int64{a, b}, ledger.BulkChange{Op: ledger.BulkAdd, Tag: "party-time"})
	require.NoError(t, err)
	assert.Equal(t, ledger.BulkResult{}, res)
}

func TestBulkAddTagsEveryCategoryLineOfASplit(t *testing.T) {
	e := setup(t)
	id, err := e.svc.Create(context.Background(), e.walmart())
	require.NoError(t, err)
	res, err := e.svc.BulkApply(context.Background(), []int64{id}, ledger.BulkChange{Op: ledger.BulkAdd, Tag: "receipt"})
	require.NoError(t, err)
	assert.Equal(t, ledger.BulkResult{Transactions: 1, Lines: 3}, res, "the three item lines, not the card line")
}

func TestBulkRemoveAndMoveOnlyTouchLinesWithTheTag(t *testing.T) {
	e := setup(t)
	a := e.add(t, "2026-09-01", "a", 100, "", "old", "keep")
	b := e.add(t, "2026-09-02", "b", 100, "", "other")
	c := e.add(t, "2026-09-03", "c", 100, "", "old", "new") // already has the target
	ctx := context.Background()

	res, err := e.svc.BulkApply(ctx, []int64{a, b, c}, ledger.BulkChange{Op: ledger.BulkMove, Tag: "old", To: "new"})
	require.NoError(t, err)
	assert.Equal(t, ledger.BulkResult{Transactions: 2, Lines: 2}, res)
	assert.Equal(t, map[string][]string{"a:1": {"keep", "new"}, "b:1": {"other"}, "c:1": {"new"}}, tagsOnLines(t, e))

	res, err = e.svc.BulkApply(ctx, []int64{a, b, c}, ledger.BulkChange{Op: ledger.BulkRemove, Tag: "new"})
	require.NoError(t, err)
	assert.Equal(t, ledger.BulkResult{Transactions: 2, Lines: 2}, res)
	assert.Equal(t, map[string][]string{"a:1": {"keep"}, "b:1": {"other"}}, tagsOnLines(t, e))
}

func TestBulkPreviewMatchesApplyAndChangesNothing(t *testing.T) {
	e := setup(t)
	a := e.add(t, "2026-09-01", "a", 100, "")
	b := e.add(t, "2026-09-02", "b", 100, "", "x")
	ch := ledger.BulkChange{Op: ledger.BulkAdd, Tag: "x"}
	pre, err := e.svc.BulkPreview(context.Background(), []int64{a, b}, ch)
	require.NoError(t, err)
	assert.Equal(t, ledger.BulkResult{Transactions: 1, Lines: 1}, pre, "b already has it")
	assert.Equal(t, map[string][]string{"b:1": {"x"}}, tagsOnLines(t, e))
	got, err := e.svc.BulkApply(context.Background(), []int64{a, b}, ch)
	require.NoError(t, err)
	assert.Equal(t, pre, got)
}

func TestBulkValidation(t *testing.T) {
	e := setup(t)
	a := e.add(t, "2026-09-01", "a", 100, "")
	ctx := context.Background()
	for _, ch := range []ledger.BulkChange{
		{Op: ledger.BulkAdd, Tag: "  "}, {Op: ledger.BulkMove, Tag: "x"}, {Op: ledger.BulkMove, Tag: "x", To: "X"}, {Op: "nuke", Tag: "x"},
	} {
		_, err := e.svc.BulkApply(ctx, []int64{a}, ch)
		assert.Error(t, err, "%+v", ch)
	}
	assert.Empty(t, tagsOnLines(t, e))

	big := make([]int64, ledger.MaxBulk+1)
	_, err := e.svc.BulkApply(ctx, big, ledger.BulkChange{Op: ledger.BulkAdd, Tag: "x"})
	assert.ErrorIs(t, err, ledger.ErrTooManyTransactions)
}

func TestMatchingIDsFollowTheFilter(t *testing.T) {
	e := setup(t)
	a := e.add(t, "2026-09-01", "coffee", 100, "")
	e.add(t, "2026-09-02", "rent", 100, "")
	c := e.add(t, "2026-09-03", "coffee beans", 100, "")
	ids, err := e.svc.MatchingIDs(context.Background(), ledger.Filter{Text: "coffee", Limit: 1})
	require.NoError(t, err)
	assert.Equal(t, []int64{c, a}, ids, "every match, newest first, paging ignored")
}
