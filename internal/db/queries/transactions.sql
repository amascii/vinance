-- name: CreateTransaction :one
INSERT INTO transactions (date, description, currency, notes, gnucash_id)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: UpdateTransaction :exec
UPDATE transactions
SET date = ?, description = ?, currency = ?, notes = ?,
    updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
WHERE id = ?;

-- name: DeleteTransaction :execrows
DELETE FROM transactions WHERE id = ?;

-- name: GetTransaction :one
SELECT * FROM transactions WHERE id = ?;

-- name: GetTransactionByGnucashID :one
SELECT * FROM transactions WHERE gnucash_id = ?;

-- name: CreateSplit :one
INSERT INTO splits (transaction_id, account_id, position, memo, currency, amount, value, reconciled)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: DeleteSplitsByTransaction :exec
DELETE FROM splits WHERE transaction_id = ?;


-- name: AddSplitTag :exec
INSERT OR IGNORE INTO split_tags (split_id, tag_id) VALUES (?, ?);
