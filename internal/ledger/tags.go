package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Errors returned by tag operations.
var (
	ErrTagNotFound = errors.New("tag not found")
	ErrTagInUse    = errors.New("tag is still used by transactions")
	ErrInvalidTag  = errors.New("invalid tag name")
)

// TagInfo is a tag with how many transaction lines (splits) carry it.
type TagInfo struct {
	Name   string
	Splits int
}

// ListTags returns every tag with its usage, ordered by name.
func (s *Service) ListTags(ctx context.Context) ([]TagInfo, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT t.name, COUNT(st.split_id)
		FROM tags t LEFT JOIN split_tags st ON st.tag_id = t.id
		GROUP BY t.id ORDER BY t.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TagInfo
	for rows.Next() {
		var t TagInfo
		if err := rows.Scan(&t.Name, &t.Splits); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TagUsage reports how many lines carry the tag, and whether the tag exists.
func (s *Service) TagUsage(ctx context.Context, name string) (splits int, exists bool, err error) {
	var id int64
	switch err := s.db.QueryRowContext(ctx, "SELECT id FROM tags WHERE name = ?", name).Scan(&id); {
	case errors.Is(err, sql.ErrNoRows):
		return 0, false, nil
	case err != nil:
		return 0, false, err
	}
	err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM split_tags WHERE tag_id = ?", id).Scan(&splits)
	return splits, true, err
}

// RenameResult describes what RenameTag did.
type RenameResult struct {
	To     string // the normalised new name
	Merged bool   // the new name already existed, so the two tags were combined
	Splits int    // lines that carried the old tag
}

// RenameTag renames a tag. The new name is normalised like any tag (TagSlug). If a tag with
// that name already exists the two are merged: every line carrying the old tag gets the
// existing one (lines that already had both keep a single copy) and the old tag is removed.
// Renaming a tag to itself does nothing.
func (s *Service) RenameTag(ctx context.Context, from, to string) (RenameResult, error) {
	res := RenameResult{To: TagSlug(to)}
	if res.To == "" {
		return res, ErrInvalidTag
	}
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var fromID int64
		switch err := tx.QueryRowContext(ctx, "SELECT id FROM tags WHERE name = ?", from).Scan(&fromID); {
		case errors.Is(err, sql.ErrNoRows):
			return ErrTagNotFound
		case err != nil:
			return err
		}
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM split_tags WHERE tag_id = ?", fromID).Scan(&res.Splits); err != nil {
			return err
		}
		if res.To == from {
			return nil
		}
		var toID int64
		switch err := tx.QueryRowContext(ctx, "SELECT id FROM tags WHERE name = ?", res.To).Scan(&toID); {
		case errors.Is(err, sql.ErrNoRows):
			_, err := tx.ExecContext(ctx, "UPDATE tags SET name = ? WHERE id = ?", res.To, fromID)
			return err
		case err != nil:
			return err
		}
		res.Merged = true
		// A budget on the old tag moves to the surviving one unless that already has its own.
		if _, err := tx.ExecContext(ctx, `UPDATE budgets SET tag_id = ? WHERE tag_id = ?
			AND NOT EXISTS (SELECT 1 FROM budgets WHERE tag_id = ?)`, toID, fromID, toID); err != nil {
			return fmt.Errorf("move budget: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO split_tags (split_id, tag_id)
			SELECT split_id, ? FROM split_tags WHERE tag_id = ?`, toID, fromID); err != nil {
			return fmt.Errorf("merge tags: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM split_tags WHERE tag_id = ?", fromID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM tags WHERE id = ?", fromID)
		return err
	})
	return res, err
}

// DeleteTag removes a tag that no line uses. Tags in use must be merged or removed from
// their transactions first.
func (s *Service) DeleteTag(ctx context.Context, name string) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		var id int64
		switch err := tx.QueryRowContext(ctx, "SELECT id FROM tags WHERE name = ?", name).Scan(&id); {
		case errors.Is(err, sql.ErrNoRows):
			return ErrTagNotFound
		case err != nil:
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM split_tags WHERE tag_id = ?", id).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrTagInUse
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM tags WHERE id = ?", id)
		return err
	})
}
