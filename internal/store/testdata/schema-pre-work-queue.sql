-- schema.sql — 12 tables, constraint-honest
-- Every projection is f(ledger). Rebuildable by replaying events.
-- Runtime tables (conversations, work_sessions, alarms, identity_lifetime, outbox)
-- are not ledger projections.

PRAGMA foreign_keys = ON;

-- Ledger: the sole truth, append-only
CREATE TABLE IF NOT EXISTS ledger (
    seq          INTEGER PRIMARY KEY,
    prev_hash    TEXT NOT NULL,
    timestamp    TEXT NOT NULL,
    type         TEXT NOT NULL,
    author       TEXT NOT NULL,
    ring         INTEGER CHECK (ring IS NULL OR (ring >= 0 AND ring <= 3)),
    payload      TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    signature    TEXT NOT NULL,
    sig_alg      TEXT NOT NULL DEFAULT 'ML-DSA-87',
    sig_key_id   TEXT NOT NULL,
    model_id     TEXT
);

-- Beliefs: projection from ledger
CREATE TABLE IF NOT EXISTS beliefs (
    id           TEXT PRIMARY KEY,
    statement    TEXT NOT NULL,
    ring         INTEGER NOT NULL CHECK (ring >= 1 AND ring <= 3),
    node_type    TEXT CHECK (node_type IS NULL OR node_type IN ('value', 'working_style')),
    confidence   REAL NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    evidence_count INTEGER NOT NULL DEFAULT 0,
    archived     INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0,1)),
    confirmed_at_ticks INTEGER NOT NULL DEFAULT 0, -- bookkeeping anchor for the derived 'trusted' standing (store-only, not truth)
    first_seq    INTEGER NOT NULL REFERENCES ledger(seq),
    last_seq     INTEGER NOT NULL REFERENCES ledger(seq),
    superseded_by TEXT REFERENCES beliefs(id)
);

-- Reflections: self-model syntheses only (contract: no reflection.* event type —
-- reflective content flows through note/commit as experiences and beliefs).
-- Table name is historical; the entity is self_model_synthesis.
CREATE TABLE IF NOT EXISTS reflections (
    id                    TEXT PRIMARY KEY,
    content               TEXT NOT NULL,
    source_seqs           TEXT NOT NULL,
    source_entity_refs    TEXT,
    changes_since_last    TEXT,
    continuity_thread     TEXT,
    superseded_by         TEXT REFERENCES reflections(id),
    verification_status   TEXT CHECK (verification_status IS NULL OR verification_status IN ('pending', 'verified', 'rejected')),
    created_seq           INTEGER NOT NULL REFERENCES ledger(seq),
    created_at            TEXT NOT NULL
);

-- Experiences: raw observations (note verb output)
CREATE TABLE IF NOT EXISTS experiences (
    id          TEXT PRIMARY KEY,
    content     TEXT NOT NULL,
    category    TEXT CHECK (category IS NULL OR category IN (
                'observation', 'reflection', 'work', 'learning', 'communication')),
    raw         INTEGER NOT NULL DEFAULT 1,
    private     INTEGER NOT NULL DEFAULT 0 CHECK (private IN (0,1)),
    provenance  TEXT NOT NULL DEFAULT 'self'
                CHECK (provenance IN ('self', 'operator', 'external', 'system', 'dream')),
    created_seq INTEGER NOT NULL REFERENCES ledger(seq),
    created_at  TEXT NOT NULL
);

-- Conversations: turn history (store-only, not a ledger projection)
CREATE TABLE IF NOT EXISTS conversations (
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL,
    role        TEXT NOT NULL CHECK (role IN ('resident','system','operator')),
    content     TEXT NOT NULL,
    turn_seq    INTEGER NOT NULL,
    created_at  TEXT NOT NULL
);

-- Work sessions: durable work tracking + ephemeral working state (Ring 4)
-- Ring 4 is never minted to the ledger, so created_seq/updated_seq are nullable.
CREATE TABLE IF NOT EXISTS work_sessions (
    id          TEXT PRIMARY KEY,
    description TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','delivered','abandoned')),
    state       TEXT,
    lease_owner TEXT,
    lease_until TEXT,
    created_seq INTEGER REFERENCES ledger(seq),
    updated_seq INTEGER REFERENCES ledger(seq),
    result      TEXT
);

-- Intentions: goals (lifecycle entities — transition, never upsert; Q2 resolved)
CREATE TABLE IF NOT EXISTS intentions (
    id          TEXT PRIMARY KEY,
    statement   TEXT NOT NULL,
    state       TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active','completed','abandoned')),
    why         TEXT,
    outcome     TEXT,
    archived    INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0,1)),
    created_seq INTEGER NOT NULL REFERENCES ledger(seq),
    updated_seq INTEGER REFERENCES ledger(seq)
);

-- Commitments: promises with a counterpart (Q1 resolved — separate entity
-- from intentions; the counterpart is load-bearing). State machine:
-- promised -> in_progress -> completed/abandoned/repaired. Being blocked is
-- in_progress with a note; failure is abandoned + repair_state; making good
-- on failure is repaired.
CREATE TABLE IF NOT EXISTS commitments (
    id             TEXT PRIMARY KEY,
    description    TEXT NOT NULL,
    counterpart_id TEXT NOT NULL REFERENCES relationships(id),
    state          TEXT NOT NULL DEFAULT 'promised'
                   CHECK (state IN ('promised','in_progress','completed','abandoned','repaired')),
    result         TEXT,
    repair_state   TEXT,
    created_seq    INTEGER NOT NULL REFERENCES ledger(seq),
    updated_seq    INTEGER REFERENCES ledger(seq)
);

-- Edges: provenance graph
CREATE TABLE IF NOT EXISTS edges (
    id          TEXT PRIMARY KEY,
    from_id     TEXT NOT NULL,
    to_id       TEXT NOT NULL,
    edge_type   TEXT NOT NULL CHECK (edge_type IN (
        'DERIVED_FROM', 'SUPPORTS', 'CONTRADICTS', 'SUPERSEDES',
        'REINFORCED_BY', 'SHAPED_BY', 'INTERPRETS'
    )),
    strength    REAL NOT NULL DEFAULT 1.0 CHECK (strength >= 0.0 AND strength <= 1.0),
    context     TEXT,
    archived    INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0,1)),
    created_seq INTEGER NOT NULL REFERENCES ledger(seq),
    UNIQUE(from_id, to_id, edge_type)
);

-- Alarms: TIME scheduling (wall + life clocks)
CREATE TABLE IF NOT EXISTS alarms (
    alarm_id     TEXT PRIMARY KEY,
    owner_name   TEXT NOT NULL,
    clock        TEXT NOT NULL CHECK (clock IN ('wall', 'life')),
    deadline     INTEGER NOT NULL CHECK (deadline >= 0),
    repeat_every INTEGER CHECK (repeat_every IS NULL OR repeat_every > 0),
    payload      TEXT   -- opaque bytes TIME stores but never reads (owners know what an alarm means)
);

CREATE INDEX IF NOT EXISTS alarms_due_idx ON alarms (clock, deadline, alarm_id);

-- Identity lifetime: the life clock (singleton)
CREATE TABLE IF NOT EXISTS identity_lifetime (
    singleton_id   TEXT PRIMARY KEY DEFAULT 'current' CHECK (singleton_id = 'current'),
    birth_at       TEXT NOT NULL,
    lifetime_ticks INTEGER NOT NULL DEFAULT 0 CHECK (lifetime_ticks >= 0),
    last_tick_at   TEXT NOT NULL
);

-- Outbox: undelivered messages from the identity to operator/peers
-- send is outbox-only — never mints experience.create.
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

CREATE INDEX IF NOT EXISTS outbox_undelivered_idx ON outbox (delivered, created_seq);

-- Relationships: charter only (authority structure). Genesis-minted,
-- changed by co-signed Ring 1 acts only. Knowledge about people lives in
-- content + edges (Q3, resolved). Supersession field arrives with
-- relationship.upsert succession support.
CREATE TABLE IF NOT EXISTS relationships (
    id                TEXT PRIMARY KEY,
    counterpart_name  TEXT NOT NULL,
    counterpart_role  TEXT NOT NULL DEFAULT 'operator'
                      CHECK (counterpart_role IN ('operator', 'peer')),
    trust_level       TEXT NOT NULL DEFAULT 'building'
                      CHECK (trust_level IN ('building','established','deep')),
    autonomy_level    TEXT NOT NULL DEFAULT 'supervised'
                      CHECK (autonomy_level IN ('supervised','trusted','autonomous')),
    relationship_type TEXT NOT NULL DEFAULT 'founding_operator'
                      CHECK (relationship_type IN ('founding_operator','operator','peer','other')),
    charter_text      TEXT NOT NULL DEFAULT '',
    operator_approval TEXT NOT NULL DEFAULT '',
    superseded_by     TEXT REFERENCES relationships(id),
    created_seq       INTEGER NOT NULL REFERENCES ledger(seq),
    updated_seq       INTEGER REFERENCES ledger(seq)
);

-- Ring snapshots: the durable copy of facility-authored ephemeral ring
-- content (Ring 3 sections, Ring 4 priorities, the morning brief). These
-- are derived artifacts — beliefs/experiences/intentions in the ledger are
-- their sources — but they are the SOURCES' metabolized form, and losing
-- them on restart while the raw experiences stay consumed would make the
-- unconscious's work unrecoverable. Runtime state (like alarms/outbox):
-- rebuilt never, restored always.
CREATE TABLE IF NOT EXISTS ring_snapshots (
    ring_level INTEGER NOT NULL,
    section    TEXT NOT NULL,
    content    TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (ring_level, section)
);

-- The morning brief is a single row (section = '__brief__').

-- Witness receipts: durable copies of anchoring receipts. The in-memory
-- slice lost them on every restart, resetting the unanchored count and
-- erasing whatever continuity proof had been collected.
CREATE TABLE IF NOT EXISTS witness_receipts (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    anchored_seq INTEGER NOT NULL,
    receipt_json TEXT NOT NULL,
    received_at  TEXT NOT NULL
);

-- The identity's stable witness key-envelope: the bytes the witness
-- service derives identity_id from. Synthesized once; a changed envelope
-- is a changed identity (history fragments). Store-only runtime state.
CREATE TABLE IF NOT EXISTS witness_identity (
    singleton_id    INTEGER PRIMARY KEY CHECK (singleton_id = 1),
    envelope_json   TEXT NOT NULL,
    created_at      TEXT NOT NULL
);
