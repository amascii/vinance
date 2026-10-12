-- +goose Up
-- Login sessions (alexedwards/scs sqlite3store). `expiry` is a Julian day number, as that store writes it.
CREATE TABLE sessions (
    token  TEXT PRIMARY KEY,
    data   BLOB NOT NULL,
    expiry REAL NOT NULL
);
CREATE INDEX sessions_expiry_idx ON sessions(expiry);

-- +goose Down
DROP TABLE sessions;
