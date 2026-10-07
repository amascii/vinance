-- +goose Up
-- Quick-add suggestions look transactions up by description, ignoring case.
CREATE INDEX idx_transactions_description ON transactions(description COLLATE NOCASE);

-- +goose Down
DROP INDEX idx_transactions_description;
