-- schema.sql — the SOLE owner of every table, index, and component in
-- this database (operator directive 2026-08-22). Nothing is ever created
-- in code, and nothing is created ad-hoc in a running database.
-- Every projection is f(ledger). Rebuildable by replaying events.
-- Runtime tables (conversations, work_sessions, alarms, identity_lifetime, outbox)
-- are not ledger projections.
--
-- EVOLUTION: schema changes edit THIS FILE — additive, with
-- CREATE ... IF NOT EXISTS — and land before any code that depends on
-- them. No DDL in Go source, ever; the only exception in the tree is
-- test code. Enforcement: schema_ownership_test.go (compile time) and
-- the boot audit (schema_audit.go, runtime).
--
-- Connection PRAGMAs (foreign_keys, busy_timeout, journal_mode) live in
-- the DSN builders in db.go, where they reach EVERY pooled connection —
-- never here: a PRAGMA in this file would reach only one pooled
-- connection (finding 7, 2026-08-17 review).

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

-- Accepted self-model syntheses. A rejected candidate never reaches this
-- projection; absence is the rejection representation.
CREATE TABLE IF NOT EXISTS self_model_synthesis (
    id                    TEXT PRIMARY KEY,
    synthesis_text        TEXT NOT NULL,
    source_entity_refs    TEXT NOT NULL CHECK (json_valid(source_entity_refs)),
    changes_since_last    TEXT NOT NULL DEFAULT '',
    continuity_thread     TEXT NOT NULL,
    superseded_by         TEXT REFERENCES self_model_synthesis(id),
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

-- Conversations: turn history (store-only, not a ledger projection).
-- turn_seq is UNIQUE (M7, external review): R52 approval citations and H3
-- source_turn citations resolve BY turn_seq — a duplicate would make the
-- consent cross-check non-deterministic. The index serves the per-turn
-- scans (latest-operator-turn, turn-by-seq, recent-turns).
CREATE TABLE IF NOT EXISTS conversations (
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL,
    -- 'participant': a person who is NOT the operator, speaking to the
    -- identity — someone on a messaging channel today, a voice in the
    -- room later. Their words persist in history because the identity
    -- must be able to refer to what was said; they are never operator
    -- authority, and both R52 gates enforce that for free by checking
    -- role = 'operator' (identity/commit.go, store/materialize.go).
    role        TEXT NOT NULL CHECK (role IN ('resident','system','operator','participant')),
    content     TEXT NOT NULL,
    turn_seq    INTEGER NOT NULL UNIQUE,
    created_at  TEXT NOT NULL,
    project_id  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_conversations_turn_seq ON conversations(turn_seq DESC);

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
    result      TEXT,
    project_id  TEXT NOT NULL DEFAULT ''
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

-- Work Queue: durable dispatch with leases (docs/WORK_QUEUE.md).
-- The queue owns delivery and durability; handlers own meaning.
CREATE TABLE IF NOT EXISTS work_queue (
    id           TEXT PRIMARY KEY,
    kind         TEXT NOT NULL,
    payload      TEXT NOT NULL DEFAULT '{}',
    dedup_key    TEXT,
    source       TEXT NOT NULL DEFAULT 'substrate',
    state        TEXT NOT NULL DEFAULT 'PENDING'
                 CHECK (state IN ('PENDING','CLAIMED','DONE','FAILED')),
    priority     INTEGER NOT NULL DEFAULT 5,
    scheduled_ms INTEGER NOT NULL DEFAULT 0,
    claimed_at   INTEGER NOT NULL DEFAULT 0,
    lease_ms     INTEGER NOT NULL DEFAULT 300000,
    done_at      INTEGER NOT NULL DEFAULT 0,
    retry_count  INTEGER NOT NULL DEFAULT 0,
    max_retries  INTEGER NOT NULL DEFAULT 3,
    error_msg    TEXT,
    created_ms   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS wq_claim_idx ON work_queue (state, priority, scheduled_ms);
CREATE INDEX IF NOT EXISTS wq_dedup_idx ON work_queue (kind, dedup_key) WHERE dedup_key IS NOT NULL;

-- Identity lifetime: the life clock (singleton)
CREATE TABLE IF NOT EXISTS identity_lifetime (
    singleton_id   TEXT PRIMARY KEY DEFAULT 'current' CHECK (singleton_id = 'current'),
    birth_at       TEXT NOT NULL,
    lifetime_ticks INTEGER NOT NULL DEFAULT 0 CHECK (lifetime_ticks >= 0),
    last_tick_at   TEXT NOT NULL
);

-- Outbox: undelivered messages from the identity to operator/peers
-- send is outbox-only — never mints experience.create (ENTITY_TYPES.md).
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

-- Reach: how to contact someone, and whether they may wake the identity.
-- The LABEL is the person — an earlier version had a separate
-- correspondent entity, a charter link and a recorded-standing enum, none
-- of which anything consumed.
--
-- Runtime state, never identity truth: knowing an address is not a fact
-- about who the identity IS (ENTITY_TYPES.md). rank 0 is primary, and the
-- operator's ordering IS primary/secondary. wake=0 batches to the next
-- turn, which is the default — a wake is a turn and a turn is a real
-- spend, so waking is granted, never assumed.
CREATE TABLE IF NOT EXISTS inbound (
    id          TEXT PRIMARY KEY,
    channel     TEXT NOT NULL,
    address     TEXT NOT NULL,
    body        TEXT NOT NULL,
    received_ms INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS inbound_by_time ON inbound (received_ms);

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

-- Trust-epoch acceptances: f(ledger) projection of trust.epoch_accepted
-- events (PLUGIN_REVOCATION_DESIGN §2.3). One row per accepted
-- revocation-snapshot epoch per root; the newest row per root IS the
-- anti-rollback high-water mark. Plain INSERT like witness_receipts —
-- replay clears and rebuilds.
CREATE TABLE IF NOT EXISTS trust_epochs (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    root           TEXT NOT NULL,
    trust_epoch    INTEGER NOT NULL,
    payload_sha256 TEXT NOT NULL,
    accepted_at    TEXT NOT NULL
);

-- The identity's stable witness key-envelope: the bytes the witness
-- service derives identity_id from. Synthesized once; a changed envelope
-- is a changed identity (history fragments). Store-only runtime state.
CREATE TABLE IF NOT EXISTS witness_identity (
    singleton_id    INTEGER PRIMARY KEY CHECK (singleton_id = 1),
    envelope_json   TEXT NOT NULL,
    created_at      TEXT NOT NULL
);

-- Plugin RING4 kv (build-order step 4): the broker's per-plugin scoped
-- projection namespace (PLUGIN_FRAMEWORK §8: plugins write only scoped
-- RING4 kv — never the ledger, never RING0-3/5). plugin_id scoping is
-- structural: the broker binding supplies it; no plugin-supplied
-- parameter ever names the namespace. temp=1 rows are the uncertified-
-- tier scope, cleared at (de)activation; temp=0 rows persist.
CREATE TABLE IF NOT EXISTS plugin_kv (
    plugin_id  TEXT NOT NULL,
    key        TEXT NOT NULL,
    value      TEXT NOT NULL,
    temp       INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (plugin_id, key)
);

-- Plugin broker receipts: host-authored proof of every brokered effect
-- (PLUGIN_THREAT_MODEL §5, A3: a plugin's return value is data; only a
-- host-authored receipt is proof). Written exclusively by the broker
-- when it evaluated and performed/attempted the effect itself.
CREATE TABLE IF NOT EXISTS plugin_receipts (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    receipt_id   TEXT NOT NULL,
    plugin_id    TEXT NOT NULL,
    operation    TEXT NOT NULL,
    target       TEXT NOT NULL,
    success      INTEGER NOT NULL,
    receipt_json TEXT NOT NULL,
    created_at   TEXT NOT NULL
);

-- Runtime meta: singleton key/value rows for process-scoped state that
-- must survive restart (R62 focus persistence). This is NOT identity
-- material — never mirrored from the ledger, never projected; it is
-- operator/workspace runtime state. schema.sql remains the SOLE owner
-- of DDL (operator directive 08-22): evolution is additive, CREATE ...
-- IF NOT EXISTS. active_project records the last focus transition so a
-- restart does not silently drop the identity into no-project focus
-- while the operator still believes one is chosen. Empty row value =
-- no focus. Validated on restore: a dangling id is dropped, not
-- rendered — inert data cannot become a rendered claim.
CREATE TABLE IF NOT EXISTS runtime_meta (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
