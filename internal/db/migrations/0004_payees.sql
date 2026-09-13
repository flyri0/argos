CREATE TABLE payees (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL,
    hlc_physical   INTEGER NOT NULL,
    hlc_counter    INTEGER NOT NULL,
    hlc_node_id    TEXT NOT NULL,
    server_version INTEGER NOT NULL,
    deleted_at     INTEGER
);
