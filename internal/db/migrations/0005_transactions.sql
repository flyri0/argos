CREATE TABLE transactions (
    id             TEXT PRIMARY KEY,
    account_id     TEXT NOT NULL REFERENCES accounts(id),
    category_id    TEXT REFERENCES categories(id),
    payee_id       TEXT REFERENCES payees(id),
    parent_id      TEXT REFERENCES transactions(id),
    date           DATE NOT NULL,
    amount         INTEGER NOT NULL,
    cleared        BOOLEAN NOT NULL,
    notes          TEXT NOT NULL DEFAULT '',
    transfer_id    TEXT,
    hlc_physical   INTEGER NOT NULL,
    hlc_counter    INTEGER NOT NULL,
    hlc_node_id    TEXT NOT NULL,
    server_version INTEGER NOT NULL,
    deleted_at     INTEGER
);
