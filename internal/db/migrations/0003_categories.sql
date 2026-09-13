CREATE TABLE categories (
    id             TEXT PRIMARY KEY,
    group_id       TEXT NOT NULL REFERENCES category_groups(id),
    name           TEXT NOT NULL,
    hidden         BOOLEAN NOT NULL,
    sort_order     INTEGER NOT NULL,
    notes          TEXT,
    hlc_physical   INTEGER NOT NULL,
    hlc_counter    INTEGER NOT NULL,
    hlc_node_id    TEXT NOT NULL,
    server_version INTEGER NOT NULL,
    deleted_at     INTEGER
);
