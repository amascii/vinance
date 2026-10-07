-- +goose Up

-- Accounts are real places money lives (banks, cards, wallets) plus four built-in
-- category accounts. Spending categories are tags, not accounts.
CREATE TABLE accounts (
    id         INTEGER PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    slug       TEXT NOT NULL UNIQUE CHECK (slug <> '' AND slug NOT GLOB '*[^a-z0-9-]*'),
    type       TEXT NOT NULL CHECK (type IN ('asset','liability','income','expense','equity','imbalance')),
    currency   TEXT,                                   -- ISO code; NULL for built-in category accounts
    builtin    INTEGER NOT NULL DEFAULT 0 CHECK (builtin IN (0,1)),
    archived   INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0,1)),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    -- real accounts must have a currency; built-ins must not
    CHECK ((builtin = 1 AND currency IS NULL) OR (builtin = 0 AND currency IS NOT NULL))
);

CREATE TABLE transactions (
    id          INTEGER PRIMARY KEY,
    date        TEXT NOT NULL CHECK (date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    description TEXT NOT NULL,
    currency    TEXT NOT NULL,                         -- currency that split values are expressed in
    notes       TEXT NOT NULL DEFAULT '',
    gnucash_id  TEXT UNIQUE,                           -- import idempotency; NULL for native transactions
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX idx_transactions_date ON transactions(date);

-- Debit-positive, like GnuCash. SUM(value) per transaction must be 0 (enforced in Go, internal/ledger).
CREATE TABLE splits (
    id             INTEGER PRIMARY KEY,
    transaction_id INTEGER NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
    account_id     INTEGER NOT NULL REFERENCES accounts(id),
    position       INTEGER NOT NULL,
    memo           TEXT NOT NULL DEFAULT '',
    currency       TEXT NOT NULL,                      -- currency of `amount`
    amount         INTEGER NOT NULL,                   -- minor units of splits.currency
    value          INTEGER NOT NULL,                   -- minor units of transactions.currency
    reconciled     TEXT NOT NULL DEFAULT 'n' CHECK (reconciled IN ('n','c','y'))
);
CREATE INDEX idx_splits_transaction ON splits(transaction_id);
CREATE INDEX idx_splits_account ON splits(account_id);

-- Tag names are kebab-case without '#': no spaces, no '#', no uppercase.
CREATE TABLE tags (
    id   INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE CHECK (name <> '' AND name NOT GLOB '*[ #A-Z]*')
);

CREATE TABLE split_tags (
    split_id INTEGER NOT NULL REFERENCES splits(id) ON DELETE CASCADE,
    tag_id   INTEGER NOT NULL REFERENCES tags(id),
    PRIMARY KEY (split_id, tag_id)
);
CREATE INDEX idx_split_tags_tag ON split_tags(tag_id);

-- Exchange rates for USD conversion in reports. usd_per_unit is a decimal string.
CREATE TABLE prices (
    currency     TEXT NOT NULL,
    date         TEXT NOT NULL,
    usd_per_unit TEXT NOT NULL,
    PRIMARY KEY (currency, date)
);

INSERT INTO accounts (name, slug, type, builtin) VALUES
    ('Expenses',  'expenses',  'expense',   1),
    ('Income',    'income',    'income',    1),
    ('Equity',    'equity',    'equity',    1),
    ('Imbalance', 'imbalance', 'imbalance', 1);

-- +goose Down
DROP TABLE prices;
DROP TABLE split_tags;
DROP TABLE tags;
DROP TABLE splits;
DROP TABLE transactions;
DROP TABLE accounts;
