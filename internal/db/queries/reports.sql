-- name: AccountBalances :many
-- Every real account with the sum of its split amounts (in the account's own currency).
SELECT a.id, a.name, a.slug, a.type, a.currency, a.archived,
       CAST(COALESCE(SUM(s.amount), 0) AS INTEGER) AS balance,
       COUNT(s.id) AS split_count
FROM accounts a
LEFT JOIN splits s ON s.account_id = a.id
WHERE a.builtin = 0
GROUP BY a.id
ORDER BY a.name;

-- name: LatestPrices :many
-- The most recent USD-per-unit rate for each currency.
SELECT p.currency, p.usd_per_unit, p.date
FROM prices p
JOIN (SELECT currency, MAX(date) AS date FROM prices GROUP BY currency) latest
  ON latest.currency = p.currency AND latest.date = p.date
ORDER BY p.currency;

-- name: UpsertPrice :exec
INSERT INTO prices (currency, date, usd_per_unit) VALUES (?, ?, ?)
ON CONFLICT (currency, date) DO UPDATE SET usd_per_unit = excluded.usd_per_unit;
