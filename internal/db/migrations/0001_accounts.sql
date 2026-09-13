CREATE TABLE accounts (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL,
    type           TEXT NOT NULL,
    on_budget      BOOLEAN NOT NULL,
    closed         BOOLEAN NOT NULL,
    currency       TEXT NOT NULL,
    notes          TEXT,
    hlc_physical   INTEGER NOT NULL,
    hlc_counter    INTEGER NOT NULL,
    hlc_node_id    TEXT NOT NULL,
    server_version INTEGER NOT NULL,
    deleted_at     INTEGER
);
