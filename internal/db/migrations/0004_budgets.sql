-- +goose Up
-- A monthly spending limit (USD cents) for one tag. Spending is measured exactly like the tag
-- report: Expenses lines carrying the tag, converted to USD.
CREATE TABLE budgets (
    id          INTEGER PRIMARY KEY,
    tag_id      INTEGER NOT NULL UNIQUE REFERENCES tags(id) ON DELETE CASCADE,
    monthly_usd INTEGER NOT NULL CHECK (monthly_usd > 0),
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

-- +goose Down
DROP TABLE budgets;
