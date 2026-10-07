-- +goose Up
-- A recurring rule is a quick-add line plus a schedule. Occurrences are numbered 0,1,2,...
-- from anchor_date; next_index is the first one not yet added or skipped.
CREATE TABLE recurring (
    id          INTEGER PRIMARY KEY,
    text        TEXT NOT NULL CHECK (text <> ''),
    freq        TEXT NOT NULL CHECK (freq IN ('weekly','monthly','yearly')),
    every       INTEGER NOT NULL DEFAULT 1 CHECK (every >= 1 AND every <= 99),
    anchor_date TEXT NOT NULL CHECK (anchor_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    next_index  INTEGER NOT NULL DEFAULT 0 CHECK (next_index >= 0),
    end_date    TEXT CHECK (end_date IS NULL OR end_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    active      INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

-- Transactions created from a rule remember which due date they were for (not the occurrence
-- number, so editing a rule's schedule can't make old and new numbering collide). The unique
-- index makes adding the same due date twice impossible: a double-click can't double-book rent.
ALTER TABLE transactions ADD COLUMN recurring_id INTEGER REFERENCES recurring(id) ON DELETE SET NULL;
ALTER TABLE transactions ADD COLUMN recurring_for TEXT;
CREATE UNIQUE INDEX idx_transactions_recurring ON transactions(recurring_id, recurring_for) WHERE recurring_id IS NOT NULL;

-- +goose Down
DROP INDEX idx_transactions_recurring;
ALTER TABLE transactions DROP COLUMN recurring_for;
ALTER TABLE transactions DROP COLUMN recurring_id;
DROP TABLE recurring;
