CREATE TABLE budget_entries (
    id             TEXT PRIMARY KEY,
    category_id    TEXT NOT NULL REFERENCES categories(id),
    month          TEXT NOT NULL,
    budgeted       INTEGER NOT NULL,
    hlc_physical   INTEGER NOT NULL,
    hlc_counter    INTEGER NOT NULL,
    hlc_node_id    TEXT NOT NULL,
    server_version INTEGER NOT NULL,
    deleted_at     INTEGER,
    UNIQUE (category_id, month)
);
