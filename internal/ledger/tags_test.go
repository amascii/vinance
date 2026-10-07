package ledger_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/amascii/vinance/internal/ledger"
)

func tagsOnLines(t *testing.T, e env) map[string][]string {
	t.Helper()
	rows, err := e.db.Query(`SELECT t.description || ':' || s.position, tg.name FROM split_tags st
		JOIN splits s ON s.id = st.split_id JOIN transactions t ON t.id = s.transaction_id JOIN tags tg ON tg.id = st.tag_id
		ORDER BY 1, 2`)
	require.NoError(t, err)
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var line, tag string
		require.NoError(t, rows.Scan(&line, &tag))
		out[line] = append(out[line], tag)
	}
	return out
}

func TestListTagsWithUsage(t *testing.T) {
	e := setup(t)
	e.add(t, "2026-09-01", "a", 100, "", "snacks", "party")
	e.add(t, "2026-09-02", "b", 100, "", "snacks")
	_, err := e.db.Exec("INSERT INTO tags (name) VALUES ('unused')")
	require.NoError(t, err)

	got, err := e.svc.ListTags(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []ledger.TagInfo{{"party", 1}, {"snacks", 2}, {"unused", 0}}, got)

	n, exists, err := e.svc.TagUsage(context.Background(), "snacks")
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, 2, n)
	_, exists, err = e.svc.TagUsage(context.Background(), "nope")
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestRenameTagInPlace(t *testing.T) {
	e := setup(t)
	e.add(t, "2026-09-01", "a", 100, "", "accesories")
	e.add(t, "2026-09-02", "b", 100, "", "accesories", "gift")

	res, err := e.svc.RenameTag(context.Background(), "accesories", "Accessories")
	require.NoError(t, err)
	assert.Equal(t, ledger.RenameResult{To: "accessories", Merged: false, Splits: 2}, res)
	assert.Equal(t, map[string][]string{"a:1": {"accessories"}, "b:1": {"accessories", "gift"}}, tagsOnLines(t, e))
	_, exists, _ := e.svc.TagUsage(context.Background(), "accesories")
	assert.False(t, exists, "the old name is gone")
}

func TestRenameTagMergesIntoExisting(t *testing.T) {
	e := setup(t)
	e.add(t, "2026-09-01", "only-old", 100, "", "ingredients")
	e.add(t, "2026-09-02", "only-new", 100, "", "groceries")
	e.add(t, "2026-09-03", "both", 100, "", "ingredients", "groceries") // would collide on merge
	e.add(t, "2026-09-04", "old-and-other", 100, "", "ingredients", "cooking")

	res, err := e.svc.RenameTag(context.Background(), "ingredients", "groceries")
	require.NoError(t, err)
	assert.Equal(t, ledger.RenameResult{To: "groceries", Merged: true, Splits: 3}, res)

	assert.Equal(t, map[string][]string{
		"only-old:1":      {"groceries"},
		"only-new:1":      {"groceries"},
		"both:1":          {"groceries"}, // not duplicated
		"old-and-other:1": {"cooking", "groceries"},
	}, tagsOnLines(t, e))
	tags, err := e.svc.ListTags(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []ledger.TagInfo{{"cooking", 1}, {"groceries", 4}}, tags, "the merged-away tag no longer exists")
}

func TestRenameTagEdgeCases(t *testing.T) {
	e := setup(t)
	e.add(t, "2026-09-01", "a", 100, "", "snacks")
	ctx := context.Background()

	res, err := e.svc.RenameTag(ctx, "snacks", "snacks")
	require.NoError(t, err)
	assert.False(t, res.Merged)
	assert.Equal(t, 1, res.Splits)

	res, err = e.svc.RenameTag(ctx, "snacks", "#Snacks ")
	require.NoError(t, err)
	assert.False(t, res.Merged, "differs only by formatting: same tag, nothing to do")
	assert.Equal(t, "snacks", res.To)

	_, err = e.svc.RenameTag(ctx, "snacks", "  # ")
	assert.ErrorIs(t, err, ledger.ErrInvalidTag)
	_, err = e.svc.RenameTag(ctx, "snacks", "")
	assert.ErrorIs(t, err, ledger.ErrInvalidTag)
	_, err = e.svc.RenameTag(ctx, "ghost", "boo")
	assert.ErrorIs(t, err, ledger.ErrTagNotFound)

	// An unused tag can still be renamed or merged.
	_, err = e.db.Exec("INSERT INTO tags (name) VALUES ('stale')")
	require.NoError(t, err)
	res, err = e.svc.RenameTag(ctx, "stale", "snacks")
	require.NoError(t, err)
	assert.True(t, res.Merged)
	assert.Equal(t, 0, res.Splits)
	assert.Equal(t, map[string][]string{"a:1": {"snacks"}}, tagsOnLines(t, e))
}

func TestDeleteTag(t *testing.T) {
	e := setup(t)
	e.add(t, "2026-09-01", "a", 100, "", "used")
	_, err := e.db.Exec("INSERT INTO tags (name) VALUES ('unused')")
	require.NoError(t, err)
	ctx := context.Background()

	assert.ErrorIs(t, e.svc.DeleteTag(ctx, "used"), ledger.ErrTagInUse)
	assert.ErrorIs(t, e.svc.DeleteTag(ctx, "ghost"), ledger.ErrTagNotFound)
	require.NoError(t, e.svc.DeleteTag(ctx, "unused"))

	tags, err := e.svc.ListTags(ctx)
	require.NoError(t, err)
	assert.Equal(t, []ledger.TagInfo{{"used", 1}}, tags)
}

func TestTagOperationsDoNotTouchTransactions(t *testing.T) {
	e := setup(t)
	id := e.add(t, "2026-09-01", "a", 100, "", "old-name")
	before, err := e.svc.Get(context.Background(), id)
	require.NoError(t, err)
	_, err = e.svc.RenameTag(context.Background(), "old-name", "new-name")
	require.NoError(t, err)
	after, err := e.svc.Get(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, before.UpdatedAt, after.UpdatedAt)
	assert.Equal(t, []string{"new-name"}, after.Splits[1].Tags)
}
