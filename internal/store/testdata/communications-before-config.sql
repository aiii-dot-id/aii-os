-- Historical table declarations before communications configuration.
-- Synthetic fixture rows are supplied by tests; no live identity data.

CREATE TABLE IF NOT EXISTS reach (
    label      TEXT NOT NULL,
    channel    TEXT NOT NULL,
    address    TEXT NOT NULL,
    rank       INTEGER NOT NULL DEFAULT 0,
    wake       INTEGER NOT NULL DEFAULT 0,
    created_ms INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (label, channel)
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
    id          TEXT PRIMARY KEY,
    channel     TEXT NOT NULL,
    address     TEXT NOT NULL,
    body        TEXT NOT NULL,
    received_ms INTEGER NOT NULL DEFAULT 0,
    seen        INTEGER NOT NULL DEFAULT 0
);
