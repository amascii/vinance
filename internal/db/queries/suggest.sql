-- name: SuggestTags :many
-- Tags starting with the given prefix, most-used first. The prefix must not contain LIKE wildcards.
SELECT t.name, COUNT(st.split_id) AS uses
FROM tags t
LEFT JOIN split_tags st ON st.tag_id = t.id
WHERE t.name LIKE sqlc.arg(prefix) || '%'
GROUP BY t.id
ORDER BY uses DESC, t.name
LIMIT sqlc.arg(max_rows);

-- name: SuggestAccounts :many
-- Real, unarchived accounts whose slug starts with the prefix, most-used first.
SELECT a.id, a.name, a.slug, a.type, a.currency, COUNT(s.id) AS uses
FROM accounts a
LEFT JOIN splits s ON s.account_id = a.id
WHERE a.builtin = 0 AND a.archived = 0 AND a.slug LIKE sqlc.arg(prefix) || '%'
GROUP BY a.id
ORDER BY uses DESC, a.name
LIMIT sqlc.arg(max_rows);

-- name: LastSpentFromAccountID :one
-- The real account most recently spent from (a negative split), used as quick-add's default.
SELECT s.account_id
FROM splits s
JOIN accounts a ON a.id = s.account_id
WHERE a.builtin = 0 AND a.archived = 0 AND s.amount < 0
ORDER BY s.id DESC
LIMIT 1;
