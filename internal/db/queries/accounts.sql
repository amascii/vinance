-- name: ListAccounts :many
SELECT * FROM accounts ORDER BY builtin DESC, name;

-- name: GetAccount :one
SELECT * FROM accounts WHERE id = ?;

-- name: GetAccountBySlug :one
SELECT * FROM accounts WHERE slug = ?;

-- name: GetBuiltinAccount :one
SELECT * FROM accounts WHERE builtin = 1 AND type = ?;

-- name: CreateAccount :one
INSERT INTO accounts (name, slug, type, currency)
VALUES (?, ?, ?, ?)
RETURNING *;
