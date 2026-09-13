CREATE TABLE devices (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL,
    token_hash     TEXT NOT NULL,
    approved_at    INTEGER NOT NULL,
    last_seen_at   INTEGER,
    revoked_at     INTEGER
);
