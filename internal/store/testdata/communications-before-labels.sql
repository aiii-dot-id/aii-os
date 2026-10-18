-- Historical table declarations before correspondent label changes.
-- Synthetic fixture rows are supplied by tests; no live identity data.

CREATE TABLE IF NOT EXISTS correspondents (
    id              TEXT PRIMARY KEY,
    label           TEXT NOT NULL UNIQUE,   -- what the identity calls them
    relationship_id TEXT REFERENCES relationships(id), -- non-null iff chartered
    created_ms      INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS reach (
    id               TEXT PRIMARY KEY,
    correspondent_id TEXT NOT NULL REFERENCES correspondents(id),
    channel          TEXT NOT NULL,
    address          TEXT NOT NULL,
    rank             INTEGER NOT NULL DEFAULT 0,
    wake             INTEGER NOT NULL DEFAULT 0,
    created_ms       INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS outbox_route (
    outbox_id TEXT PRIMARY KEY REFERENCES outbox(id),
    channel   TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS outbox (
    id           TEXT PRIMARY KEY,
    to_role      TEXT NOT NULL CHECK (to_role IN ('operator', 'peer')),
    to_identity  TEXT,
    content      TEXT NOT NULL,
    delivered    INTEGER NOT NULL DEFAULT 0,
    delivered_via TEXT,
    created_seq  INTEGER REFERENCES ledger(seq),
    delivered_at TEXT,
    created_ms   INTEGER NOT NULL DEFAULT 0 -- unix ms; resident-delivery window (timer firings the identity hasn't seen)
);

CREATE TABLE IF NOT EXISTS inbound (
    id               TEXT PRIMARY KEY,
    channel          TEXT NOT NULL,
    address          TEXT NOT NULL,   -- the sender, as the channel reports them
    correspondent_id TEXT REFERENCES correspondents(id),
    standing         TEXT NOT NULL DEFAULT 'unknown'
                     CHECK (standing IN ('unknown','known','chartered')),
    content          TEXT NOT NULL,
    received_ms      INTEGER NOT NULL DEFAULT 0,
    seen             INTEGER NOT NULL DEFAULT 0  -- carried into a turn
);
