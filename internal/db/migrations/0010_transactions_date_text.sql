-- Rebuilds transactions with date declared TEXT (§5). A DATE-declared column
-- is converted to a time value by the driver on read, so dates came back as
-- full timestamps that /sync then rejected. Stored values are already
-- YYYY-MM-DD text, so rows are copied unchanged.
CREATE TABLE transactions_new (
    id             TEXT PRIMARY KEY,
    account_id     TEXT NOT NULL REFERENCES accounts(id),
    category_id    TEXT REFERENCES categories(id),
    payee_id       TEXT REFERENCES payees(id),
    parent_id      TEXT REFERENCES transactions(id),
    date           TEXT NOT NULL,
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

INSERT INTO transactions_new (id, account_id, category_id, payee_id, parent_id, date, amount, cleared, notes, transfer_id, hlc_physical, hlc_counter, hlc_node_id, server_version, deleted_at)
SELECT id, account_id, category_id, payee_id, parent_id, date, amount, cleared, notes, transfer_id, hlc_physical, hlc_counter, hlc_node_id, server_version, deleted_at
FROM transactions;

DROP TABLE transactions;

ALTER TABLE transactions_new RENAME TO transactions;

CREATE INDEX idx_transactions_account_id_deleted_at ON transactions(account_id, deleted_at);
