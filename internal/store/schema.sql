-- provenance: every table declares who builds it, as the first line of

CREATE TABLE IF NOT EXISTS public_name (
    -- provenance: derived, clear-order 10
    singleton_id TEXT PRIMARY KEY CHECK (singleton_id = 'current'),
    name_id      TEXT NOT NULL,
    name         TEXT NOT NULL,
    zone         TEXT NOT NULL,
    claimed_seq  INTEGER NOT NULL REFERENCES ledger(seq),
    claimed_at   TEXT NOT NULL
) STRICT;

CREATE TABLE IF NOT EXISTS ledger (
    -- provenance: derived, clear-order 11
    seq      INTEGER PRIMARY KEY,
    prev     TEXT NOT NULL,
    ts       TEXT NOT NULL,
    type     TEXT NOT NULL,
    ring     INTEGER CHECK (ring IS NULL OR (ring >= 0 AND ring <= 3)),
    payload  TEXT NOT NULL CHECK (json_valid(payload)),
    content  TEXT NOT NULL,
    sig      TEXT NOT NULL
) STRICT;

CREATE TABLE IF NOT EXISTS beliefs (
    -- provenance: derived, clear-order 8
    id           TEXT PRIMARY KEY,
    statement    TEXT NOT NULL,
    ring         INTEGER NOT NULL CHECK (ring >= 1 AND ring <= 3),
    node_type    TEXT CHECK (node_type IS NULL OR node_type IN ('value', 'working_style')),
    confidence   REAL NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    evidence_count INTEGER NOT NULL DEFAULT 0,
    archived     INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0,1)),
    confirmed_at_ticks INTEGER NOT NULL DEFAULT 0,
    first_seq    INTEGER NOT NULL REFERENCES ledger(seq),
    last_seq     INTEGER NOT NULL REFERENCES ledger(seq),
    superseded_by TEXT REFERENCES beliefs(id)
) STRICT;

CREATE TABLE IF NOT EXISTS self_model_synthesis (
    -- provenance: derived, clear-order 7
    id                    TEXT PRIMARY KEY,
    synthesis_text        TEXT NOT NULL,
    source_entity_refs    TEXT NOT NULL CHECK (json_valid(source_entity_refs)),
    changes_since_last    TEXT NOT NULL DEFAULT '',
    continuity_thread     TEXT NOT NULL,
    superseded_by         TEXT REFERENCES self_model_synthesis(id),
    created_seq           INTEGER NOT NULL REFERENCES ledger(seq),
    created_at            TEXT NOT NULL
) STRICT;

CREATE TABLE IF NOT EXISTS experiences (
    -- provenance: derived, clear-order 6
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
) STRICT;
CREATE INDEX IF NOT EXISTS idx_experiences_created_seq ON experiences (created_seq DESC);

CREATE TABLE IF NOT EXISTS conversations (
    -- provenance: ephemeral
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL,
    role        TEXT NOT NULL CHECK (role IN ('resident','system','operator','participant')),
    content     TEXT NOT NULL,
    turn_seq    INTEGER NOT NULL UNIQUE,
    created_at  TEXT NOT NULL,
    project_id  TEXT NOT NULL DEFAULT ''
) STRICT;
CREATE INDEX IF NOT EXISTS idx_conversations_turn_seq ON conversations(turn_seq DESC);

CREATE TABLE IF NOT EXISTS turn_annotations (
    -- provenance: ephemeral
    turn_seq   INTEGER NOT NULL,
    kind       TEXT NOT NULL,
    key        TEXT NOT NULL,
    payload    TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (turn_seq, kind)
) STRICT;
CREATE INDEX IF NOT EXISTS idx_turn_annotations_kind_key ON turn_annotations(kind, key);

CREATE TABLE IF NOT EXISTS work_sessions (
    -- provenance: ephemeral
    id          TEXT PRIMARY KEY,
    description TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','delivered','abandoned')),
    state       TEXT,
    lease_owner TEXT,
    lease_until TEXT,
    created_seq INTEGER REFERENCES ledger(seq),
    updated_seq INTEGER REFERENCES ledger(seq),
    result      TEXT,
    project_id  TEXT NOT NULL DEFAULT '',
    focus TEXT NOT NULL DEFAULT '',
    next_move TEXT NOT NULL DEFAULT '',
    plan TEXT NOT NULL DEFAULT '',
    expected_evidence TEXT NOT NULL DEFAULT '',
    falsifier TEXT NOT NULL DEFAULT '',
    decision_needed TEXT NOT NULL DEFAULT '',
    evidence TEXT NOT NULL DEFAULT '',
    evidence_readback TEXT NOT NULL DEFAULT '',
    harvested_ms INTEGER,
    -- delivered_at: wall-clock (unix-ms) a delivery landed, incl. the boot
    -- sweep. NULL for pre-repair rows; the measurement window uses it so
    -- Ring-4 deliveries (created_seq=0) can be dated at all.
    delivered_at INTEGER
) STRICT;

CREATE TABLE IF NOT EXISTS curiosity_cue (
    -- provenance: ephemeral
    singleton_id TEXT PRIMARY KEY DEFAULT 'current',
    subject      TEXT NOT NULL,
    why          TEXT NOT NULL DEFAULT '',
    pointer      TEXT NOT NULL DEFAULT '',
    kind         TEXT NOT NULL DEFAULT 'invitation',
    set_at       TEXT NOT NULL
) STRICT;

CREATE TABLE IF NOT EXISTS intentions (
    -- provenance: derived, clear-order 5
    id          TEXT PRIMARY KEY,
    statement   TEXT NOT NULL,
    state       TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active','completed','abandoned')),
    why         TEXT,
    outcome     TEXT,
    archived    INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0,1)),
    created_seq INTEGER NOT NULL REFERENCES ledger(seq),
    updated_seq INTEGER REFERENCES ledger(seq)
) STRICT;

CREATE TABLE IF NOT EXISTS commitments (
    -- provenance: derived, clear-order 3
    id             TEXT PRIMARY KEY,
    description    TEXT NOT NULL,
    counterpart_id TEXT NOT NULL REFERENCES relationships(id),
    state          TEXT NOT NULL DEFAULT 'promised'
                   CHECK (state IN ('promised','in_progress','completed','abandoned','repaired')),
    result         TEXT,
    repair_state   TEXT,
    created_seq    INTEGER NOT NULL REFERENCES ledger(seq),
    updated_seq    INTEGER REFERENCES ledger(seq)
) STRICT;

CREATE TABLE IF NOT EXISTS edges (
    -- provenance: derived, clear-order 4
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
) STRICT;
CREATE INDEX IF NOT EXISTS idx_edges_to ON edges (to_id) WHERE archived = 0;

CREATE TABLE IF NOT EXISTS alarms (
    -- provenance: ephemeral
    alarm_id     TEXT PRIMARY KEY,
    owner_name   TEXT NOT NULL,
    clock        TEXT NOT NULL CHECK (clock IN ('wall', 'life')),
    deadline     INTEGER NOT NULL CHECK (deadline >= 0),
    repeat_every INTEGER CHECK (repeat_every IS NULL OR repeat_every > 0),
    payload      TEXT
) STRICT;

CREATE INDEX IF NOT EXISTS alarms_due_idx ON alarms (clock, deadline, alarm_id);

CREATE TABLE IF NOT EXISTS work_queue (
    -- provenance: ephemeral
    id           TEXT PRIMARY KEY,
    kind         TEXT NOT NULL,
    payload      TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(payload)),
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
) STRICT;
CREATE INDEX IF NOT EXISTS wq_claim_idx ON work_queue (state, priority, scheduled_ms);
CREATE INDEX IF NOT EXISTS wq_dedup_idx ON work_queue (kind, dedup_key) WHERE dedup_key IS NOT NULL;

CREATE TABLE IF NOT EXISTS identity_lifetime (
    -- provenance: ephemeral
    singleton_id   TEXT PRIMARY KEY DEFAULT 'current' CHECK (singleton_id = 'current'),
    birth_at       TEXT NOT NULL,
    lifetime_ticks INTEGER NOT NULL DEFAULT 0 CHECK (lifetime_ticks >= 0),
    last_tick_at   TEXT NOT NULL
) STRICT;

CREATE TABLE IF NOT EXISTS outbox (
    -- provenance: ephemeral
    id           TEXT PRIMARY KEY,
    to_role      TEXT NOT NULL CHECK (to_role IN ('operator', 'peer')),
    to_identity  TEXT,
    content      TEXT NOT NULL,
    delivered    INTEGER NOT NULL DEFAULT 0,
    delivered_via TEXT,
    created_seq  INTEGER REFERENCES ledger(seq),
    delivered_at TEXT,
    created_ms   INTEGER NOT NULL DEFAULT 0,
    -- The delivery record:
    -- how many times an adapter was asked, what the last one answered
    -- and when, whether the row is parked (asked no more), and the
    -- effect class of the last send — performed once delivered,
    -- unknown when the send was written and the response lost (never
    -- retried, never handed to a secondary).
    attempts        INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT NOT NULL DEFAULT '',
    last_attempt_ms INTEGER NOT NULL DEFAULT 0,
    parked          INTEGER NOT NULL DEFAULT 0,
    effect          TEXT NOT NULL DEFAULT '' CHECK (effect IN ('', 'performed', 'unknown'))
) STRICT;

CREATE TABLE IF NOT EXISTS turn_metrics (
    -- provenance: ephemeral
    ts_ms    INTEGER PRIMARY KEY,
    calls    INTEGER NOT NULL,
    read_only INTEGER NOT NULL,
    spawned  INTEGER NOT NULL,
    harvested INTEGER NOT NULL,
    predicted INTEGER NOT NULL DEFAULT 0,
    declared_ordinal INTEGER NOT NULL DEFAULT 0,
    independent INTEGER NOT NULL DEFAULT 0,
    rounds INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE TABLE IF NOT EXISTS speech_usage (
    -- provenance: ephemeral
    provider   TEXT NOT NULL,
    direction  TEXT NOT NULL CHECK (direction IN ('stt','tts')),
    period     TEXT NOT NULL,
    requests   INTEGER NOT NULL DEFAULT 0,
    characters INTEGER NOT NULL DEFAULT 0,
    ms         INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (provider, direction, period)
) STRICT;

CREATE TABLE IF NOT EXISTS skill_proposals (
    -- provenance: ephemeral
    id         TEXT PRIMARY KEY,
    ts_ms      INTEGER NOT NULL,
    title      TEXT NOT NULL,
    delta      TEXT NOT NULL,
    evidence   TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'proposed' CHECK (status IN ('proposed','promoted','rejected')),
    decided_ms INTEGER NOT NULL DEFAULT 0,
    verified   TEXT NOT NULL DEFAULT 'none'
) STRICT;

CREATE TABLE IF NOT EXISTS standing_state (
    -- provenance: ephemeral
    singleton_id TEXT PRIMARY KEY DEFAULT 'current' CHECK (singleton_id = 'current'),
    text         TEXT NOT NULL DEFAULT '',
    updated_ms   INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE TABLE IF NOT EXISTS subagent_metrics (
    -- provenance: ephemeral
    session_id TEXT PRIMARY KEY,
    ts_ms      INTEGER NOT NULL,
    calls      INTEGER NOT NULL,
    tokens     INTEGER NOT NULL,
    wall_ms    INTEGER NOT NULL,
    failed     INTEGER NOT NULL,
    role       TEXT NOT NULL DEFAULT '',
    model      TEXT NOT NULL DEFAULT '',
    legs       INTEGER NOT NULL DEFAULT 1
) STRICT;

CREATE TABLE IF NOT EXISTS tool_events (
    -- provenance: ephemeral
    -- replaces-legacy-when-column: phase
    execution_id TEXT PRIMARY KEY,
    turn_id      TEXT NOT NULL,
    ordinal      INTEGER NOT NULL,
    actor        TEXT NOT NULL,
    model        TEXT NOT NULL DEFAULT '',
    provider_call_id TEXT NOT NULL DEFAULT '',
    tool         TEXT NOT NULL,
    args_record  TEXT NOT NULL,
    state        TEXT NOT NULL CHECK (state IN ('started','done','abandoned')),
    failed       INTEGER NOT NULL DEFAULT 0,
    truncated    INTEGER NOT NULL DEFAULT 0,
    result_record TEXT NOT NULL DEFAULT '',
    started_ms   INTEGER NOT NULL,
    finished_ms  INTEGER NOT NULL DEFAULT 0,
    UNIQUE (turn_id, ordinal)
) STRICT;

CREATE INDEX IF NOT EXISTS tool_events_started_idx ON tool_events (started_ms);
CREATE INDEX IF NOT EXISTS tool_events_turn_idx ON tool_events (turn_id);

CREATE INDEX IF NOT EXISTS outbox_undelivered_idx ON outbox (delivered, created_seq);

CREATE TABLE IF NOT EXISTS relationships (
    -- provenance: derived, clear-order 9
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
) STRICT;

CREATE TABLE IF NOT EXISTS inbound (
    -- provenance: ephemeral
    id          TEXT PRIMARY KEY,
    channel     TEXT NOT NULL,
    address     TEXT NOT NULL,
    body        TEXT NOT NULL,
    received_ms INTEGER NOT NULL DEFAULT 0
) STRICT;
CREATE INDEX IF NOT EXISTS inbound_by_time ON inbound (received_ms);

CREATE TABLE IF NOT EXISTS ring_snapshots (
    -- provenance: ephemeral
    ring_level INTEGER NOT NULL,
    section    TEXT NOT NULL,
    content    TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (ring_level, section)
) STRICT;

CREATE TABLE IF NOT EXISTS witness_receipts (
    -- provenance: derived, clear-order 1
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    anchored_seq INTEGER NOT NULL,
    receipt_json TEXT NOT NULL CHECK (json_valid(receipt_json)),
    received_at  TEXT NOT NULL
) STRICT;

CREATE TABLE IF NOT EXISTS trust_epochs (
    -- provenance: derived, clear-order 2
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    root           TEXT NOT NULL,
    trust_epoch    INTEGER NOT NULL,
    payload_sha256 TEXT NOT NULL,
    accepted_at    TEXT NOT NULL
) STRICT;

CREATE TABLE IF NOT EXISTS witness_identity (
    -- provenance: ephemeral
    singleton_id    INTEGER PRIMARY KEY CHECK (singleton_id = 1),
    envelope_json   TEXT NOT NULL CHECK (json_valid(envelope_json)),
    created_at      TEXT NOT NULL
) STRICT;

CREATE TABLE IF NOT EXISTS plugin_kv (
    -- provenance: ephemeral
    plugin_id  TEXT NOT NULL,
    key        TEXT NOT NULL,
    value      TEXT NOT NULL,
    temp       INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (plugin_id, key)
) STRICT;

CREATE TABLE IF NOT EXISTS plugin_receipts (
    -- provenance: ephemeral
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    receipt_id   TEXT NOT NULL,
    plugin_id    TEXT NOT NULL,
    operation    TEXT NOT NULL,
    target       TEXT NOT NULL,
    success      INTEGER NOT NULL,
    receipt_json TEXT NOT NULL CHECK (json_valid(receipt_json)),
    created_at   TEXT NOT NULL
) STRICT;

CREATE TABLE IF NOT EXISTS runtime_meta (
    -- provenance: ephemeral
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

-- ─── Memory instruments ─────────────────────────────────────────────
--
-- Access is one ephemeral table keyed by store and id: how often a
-- memory was consciously recalled, when last, and a bounded history of
-- those times, so every decay policy computes from the same rows. Only
-- an act of recall writes it; rendering a memory never does.

CREATE TABLE IF NOT EXISTS memory_access (
    -- provenance: ephemeral
    store    TEXT NOT NULL,
    id       TEXT NOT NULL,
    count    INTEGER NOT NULL DEFAULT 0 CHECK (count >= 0),
    last_at  TEXT NOT NULL,
    history  TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(history)),
    PRIMARY KEY (store, id)
) STRICT;

-- The meaning layer: one quantized unit vector per
-- recallable row under a basis (provider/model), keyed like
-- memory_access. Ephemeral: only the runtime can reach the provider
-- that makes a vector, so a replay leaves this table alone; a basis
-- change drops what the old basis made and the backfill refills it.
CREATE TABLE IF NOT EXISTS memory_vectors (
    -- provenance: ephemeral
    store       TEXT NOT NULL,
    id          TEXT NOT NULL,
    basis       TEXT NOT NULL,
    content_sha TEXT NOT NULL,
    dims        INTEGER NOT NULL CHECK (dims > 0),
    scale       REAL NOT NULL CHECK (scale > 0),
    q           BLOB NOT NULL,
    embedded_at TEXT NOT NULL,
    PRIMARY KEY (store, id)
) STRICT;
CREATE INDEX IF NOT EXISTS idx_memory_vectors_basis ON memory_vectors (store, basis);

-- The decision log of the unconscious side: the salience
-- filter's class for every candidate weighed, the rhythm's run, defer
-- or skip per facility per pass. Linked to the ledger seq a decision
-- produced (0 when nothing), bounded at write — a record, never a
-- backlog, read by no one to act.
CREATE TABLE IF NOT EXISTS memory_decisions (
    -- provenance: ephemeral
    id          INTEGER PRIMARY KEY,
    decided_at  TEXT NOT NULL,
    kind        TEXT NOT NULL CHECK (kind IN ('salience', 'rhythm')),
    facility    TEXT NOT NULL,
    decision    TEXT NOT NULL,
    seq         INTEGER NOT NULL DEFAULT 0,
    score       REAL NOT NULL DEFAULT 0,
    record      TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(record))
) STRICT;
CREATE INDEX IF NOT EXISTS idx_memory_decisions_kind ON memory_decisions (kind, id);

-- A plugin's own memories: its working record, indexed by the same
-- sidecars as the identity's stores. Provenance is stamped by the host,
-- never supplied by the caller; superseded_by is the state a correction
-- leaves, so the old memory stays for audit and surfaces only through
-- its successor. Never identity truth: promotion is the identity's act.

CREATE TABLE IF NOT EXISTS plugin_memories (
    -- provenance: ephemeral
    id            TEXT PRIMARY KEY,
    plugin_id     TEXT NOT NULL,
    text          TEXT NOT NULL,
    project       TEXT NOT NULL DEFAULT '',
    attribution   TEXT NOT NULL DEFAULT 'plugin'
                  CHECK (attribution IN ('plugin', 'resident', 'operator', 'participant', 'external')),
    superseded_by TEXT REFERENCES plugin_memories(id),
    temp          INTEGER NOT NULL DEFAULT 0 CHECK (temp IN (0,1)),
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
) STRICT;

CREATE INDEX IF NOT EXISTS idx_plugin_memories_plugin ON plugin_memories(plugin_id);

-- Full-text sidecars: one per searchable store, with a trigram twin for
-- substring and fuzzy matches. A sidecar is FTS5 over the base's own
-- rows (content= the base, or a view over it), keyed by the base's
-- rowid, so it copies no text; the base's triggers keep it atomic with
-- every write, a replay's clear and the materializer's inserts rebuild
-- it through those triggers, and FTS5's own rebuild fills it when a
-- boot finds it holding fewer rows than its content. Each declares its
-- provenance on the line before it: sidecar of <base>. Neither derived
-- nor ephemeral — f(base) — and never a writer's target.
--
-- The resident's, the operator's and a participant's words are
-- searchable; system-role turns are tool output, a record and not a
-- memory, and the view keeps them out of both conversation sidecars.

CREATE VIEW IF NOT EXISTS conversations_searchable AS
    SELECT rowid AS rowid, id, content
    FROM conversations
    WHERE role IN ('resident', 'operator', 'participant');

-- provenance: sidecar of experiences
CREATE VIRTUAL TABLE IF NOT EXISTS experiences_fts USING fts5(content, content='experiences', content_rowid='rowid', tokenize='unicode61 remove_diacritics 2');
-- provenance: sidecar of experiences
CREATE VIRTUAL TABLE IF NOT EXISTS experiences_tri USING fts5(content, content='experiences', content_rowid='rowid', tokenize='trigram');
CREATE TRIGGER IF NOT EXISTS experiences_sidecar_ai AFTER INSERT ON experiences BEGIN
    INSERT INTO experiences_fts(rowid, content) VALUES (new.rowid, new.content);
    INSERT INTO experiences_tri(rowid, content) VALUES (new.rowid, new.content);
END;
CREATE TRIGGER IF NOT EXISTS experiences_sidecar_ad AFTER DELETE ON experiences BEGIN
    INSERT INTO experiences_fts(experiences_fts, rowid, content) VALUES ('delete', old.rowid, old.content);
    INSERT INTO experiences_tri(experiences_tri, rowid, content) VALUES ('delete', old.rowid, old.content);
END;
CREATE TRIGGER IF NOT EXISTS experiences_sidecar_au AFTER UPDATE OF content ON experiences BEGIN
    INSERT INTO experiences_fts(experiences_fts, rowid, content) VALUES ('delete', old.rowid, old.content);
    INSERT INTO experiences_tri(experiences_tri, rowid, content) VALUES ('delete', old.rowid, old.content);
    INSERT INTO experiences_fts(rowid, content) VALUES (new.rowid, new.content);
    INSERT INTO experiences_tri(rowid, content) VALUES (new.rowid, new.content);
END;

-- provenance: sidecar of beliefs
CREATE VIRTUAL TABLE IF NOT EXISTS beliefs_fts USING fts5(statement, content='beliefs', content_rowid='rowid', tokenize='unicode61 remove_diacritics 2');
-- provenance: sidecar of beliefs
CREATE VIRTUAL TABLE IF NOT EXISTS beliefs_tri USING fts5(statement, content='beliefs', content_rowid='rowid', tokenize='trigram');
CREATE TRIGGER IF NOT EXISTS beliefs_sidecar_ai AFTER INSERT ON beliefs BEGIN
    INSERT INTO beliefs_fts(rowid, statement) VALUES (new.rowid, new.statement);
    INSERT INTO beliefs_tri(rowid, statement) VALUES (new.rowid, new.statement);
END;
CREATE TRIGGER IF NOT EXISTS beliefs_sidecar_ad AFTER DELETE ON beliefs BEGIN
    INSERT INTO beliefs_fts(beliefs_fts, rowid, statement) VALUES ('delete', old.rowid, old.statement);
    INSERT INTO beliefs_tri(beliefs_tri, rowid, statement) VALUES ('delete', old.rowid, old.statement);
END;
CREATE TRIGGER IF NOT EXISTS beliefs_sidecar_au AFTER UPDATE OF statement ON beliefs BEGIN
    INSERT INTO beliefs_fts(beliefs_fts, rowid, statement) VALUES ('delete', old.rowid, old.statement);
    INSERT INTO beliefs_tri(beliefs_tri, rowid, statement) VALUES ('delete', old.rowid, old.statement);
    INSERT INTO beliefs_fts(rowid, statement) VALUES (new.rowid, new.statement);
    INSERT INTO beliefs_tri(rowid, statement) VALUES (new.rowid, new.statement);
END;

-- provenance: sidecar of self_model_synthesis
CREATE VIRTUAL TABLE IF NOT EXISTS self_model_synthesis_fts USING fts5(synthesis_text, content='self_model_synthesis', content_rowid='rowid', tokenize='unicode61 remove_diacritics 2');
-- provenance: sidecar of self_model_synthesis
CREATE VIRTUAL TABLE IF NOT EXISTS self_model_synthesis_tri USING fts5(synthesis_text, content='self_model_synthesis', content_rowid='rowid', tokenize='trigram');
CREATE TRIGGER IF NOT EXISTS self_model_synthesis_sidecar_ai AFTER INSERT ON self_model_synthesis BEGIN
    INSERT INTO self_model_synthesis_fts(rowid, synthesis_text) VALUES (new.rowid, new.synthesis_text);
    INSERT INTO self_model_synthesis_tri(rowid, synthesis_text) VALUES (new.rowid, new.synthesis_text);
END;
CREATE TRIGGER IF NOT EXISTS self_model_synthesis_sidecar_ad AFTER DELETE ON self_model_synthesis BEGIN
    INSERT INTO self_model_synthesis_fts(self_model_synthesis_fts, rowid, synthesis_text) VALUES ('delete', old.rowid, old.synthesis_text);
    INSERT INTO self_model_synthesis_tri(self_model_synthesis_tri, rowid, synthesis_text) VALUES ('delete', old.rowid, old.synthesis_text);
END;
CREATE TRIGGER IF NOT EXISTS self_model_synthesis_sidecar_au AFTER UPDATE OF synthesis_text ON self_model_synthesis BEGIN
    INSERT INTO self_model_synthesis_fts(self_model_synthesis_fts, rowid, synthesis_text) VALUES ('delete', old.rowid, old.synthesis_text);
    INSERT INTO self_model_synthesis_tri(self_model_synthesis_tri, rowid, synthesis_text) VALUES ('delete', old.rowid, old.synthesis_text);
    INSERT INTO self_model_synthesis_fts(rowid, synthesis_text) VALUES (new.rowid, new.synthesis_text);
    INSERT INTO self_model_synthesis_tri(rowid, synthesis_text) VALUES (new.rowid, new.synthesis_text);
END;

-- provenance: sidecar of intentions
CREATE VIRTUAL TABLE IF NOT EXISTS intentions_fts USING fts5(statement, why, outcome, content='intentions', content_rowid='rowid', tokenize='unicode61 remove_diacritics 2');
-- provenance: sidecar of intentions
CREATE VIRTUAL TABLE IF NOT EXISTS intentions_tri USING fts5(statement, why, outcome, content='intentions', content_rowid='rowid', tokenize='trigram');
CREATE TRIGGER IF NOT EXISTS intentions_sidecar_ai AFTER INSERT ON intentions BEGIN
    INSERT INTO intentions_fts(rowid, statement, why, outcome) VALUES (new.rowid, new.statement, new.why, new.outcome);
    INSERT INTO intentions_tri(rowid, statement, why, outcome) VALUES (new.rowid, new.statement, new.why, new.outcome);
END;
CREATE TRIGGER IF NOT EXISTS intentions_sidecar_ad AFTER DELETE ON intentions BEGIN
    INSERT INTO intentions_fts(intentions_fts, rowid, statement, why, outcome) VALUES ('delete', old.rowid, old.statement, old.why, old.outcome);
    INSERT INTO intentions_tri(intentions_tri, rowid, statement, why, outcome) VALUES ('delete', old.rowid, old.statement, old.why, old.outcome);
END;
CREATE TRIGGER IF NOT EXISTS intentions_sidecar_au AFTER UPDATE OF statement, why, outcome ON intentions BEGIN
    INSERT INTO intentions_fts(intentions_fts, rowid, statement, why, outcome) VALUES ('delete', old.rowid, old.statement, old.why, old.outcome);
    INSERT INTO intentions_tri(intentions_tri, rowid, statement, why, outcome) VALUES ('delete', old.rowid, old.statement, old.why, old.outcome);
    INSERT INTO intentions_fts(rowid, statement, why, outcome) VALUES (new.rowid, new.statement, new.why, new.outcome);
    INSERT INTO intentions_tri(rowid, statement, why, outcome) VALUES (new.rowid, new.statement, new.why, new.outcome);
END;

-- provenance: sidecar of commitments
CREATE VIRTUAL TABLE IF NOT EXISTS commitments_fts USING fts5(description, content='commitments', content_rowid='rowid', tokenize='unicode61 remove_diacritics 2');
-- provenance: sidecar of commitments
CREATE VIRTUAL TABLE IF NOT EXISTS commitments_tri USING fts5(description, content='commitments', content_rowid='rowid', tokenize='trigram');
CREATE TRIGGER IF NOT EXISTS commitments_sidecar_ai AFTER INSERT ON commitments BEGIN
    INSERT INTO commitments_fts(rowid, description) VALUES (new.rowid, new.description);
    INSERT INTO commitments_tri(rowid, description) VALUES (new.rowid, new.description);
END;
CREATE TRIGGER IF NOT EXISTS commitments_sidecar_ad AFTER DELETE ON commitments BEGIN
    INSERT INTO commitments_fts(commitments_fts, rowid, description) VALUES ('delete', old.rowid, old.description);
    INSERT INTO commitments_tri(commitments_tri, rowid, description) VALUES ('delete', old.rowid, old.description);
END;
CREATE TRIGGER IF NOT EXISTS commitments_sidecar_au AFTER UPDATE OF description ON commitments BEGIN
    INSERT INTO commitments_fts(commitments_fts, rowid, description) VALUES ('delete', old.rowid, old.description);
    INSERT INTO commitments_tri(commitments_tri, rowid, description) VALUES ('delete', old.rowid, old.description);
    INSERT INTO commitments_fts(rowid, description) VALUES (new.rowid, new.description);
    INSERT INTO commitments_tri(rowid, description) VALUES (new.rowid, new.description);
END;

-- provenance: sidecar of relationships
CREATE VIRTUAL TABLE IF NOT EXISTS relationships_fts USING fts5(counterpart_name, charter_text, content='relationships', content_rowid='rowid', tokenize='unicode61 remove_diacritics 2');
-- provenance: sidecar of relationships
CREATE VIRTUAL TABLE IF NOT EXISTS relationships_tri USING fts5(counterpart_name, charter_text, content='relationships', content_rowid='rowid', tokenize='trigram');
CREATE TRIGGER IF NOT EXISTS relationships_sidecar_ai AFTER INSERT ON relationships BEGIN
    INSERT INTO relationships_fts(rowid, counterpart_name, charter_text) VALUES (new.rowid, new.counterpart_name, new.charter_text);
    INSERT INTO relationships_tri(rowid, counterpart_name, charter_text) VALUES (new.rowid, new.counterpart_name, new.charter_text);
END;
CREATE TRIGGER IF NOT EXISTS relationships_sidecar_ad AFTER DELETE ON relationships BEGIN
    INSERT INTO relationships_fts(relationships_fts, rowid, counterpart_name, charter_text) VALUES ('delete', old.rowid, old.counterpart_name, old.charter_text);
    INSERT INTO relationships_tri(relationships_tri, rowid, counterpart_name, charter_text) VALUES ('delete', old.rowid, old.counterpart_name, old.charter_text);
END;
CREATE TRIGGER IF NOT EXISTS relationships_sidecar_au AFTER UPDATE OF counterpart_name, charter_text ON relationships BEGIN
    INSERT INTO relationships_fts(relationships_fts, rowid, counterpart_name, charter_text) VALUES ('delete', old.rowid, old.counterpart_name, old.charter_text);
    INSERT INTO relationships_tri(relationships_tri, rowid, counterpart_name, charter_text) VALUES ('delete', old.rowid, old.counterpart_name, old.charter_text);
    INSERT INTO relationships_fts(rowid, counterpart_name, charter_text) VALUES (new.rowid, new.counterpart_name, new.charter_text);
    INSERT INTO relationships_tri(rowid, counterpart_name, charter_text) VALUES (new.rowid, new.counterpart_name, new.charter_text);
END;

-- provenance: sidecar of conversations
CREATE VIRTUAL TABLE IF NOT EXISTS conversations_fts USING fts5(content, content='conversations_searchable', content_rowid='rowid', tokenize='unicode61 remove_diacritics 2');
-- provenance: sidecar of conversations
CREATE VIRTUAL TABLE IF NOT EXISTS conversations_tri USING fts5(content, content='conversations_searchable', content_rowid='rowid', tokenize='trigram');
CREATE TRIGGER IF NOT EXISTS conversations_sidecar_ai AFTER INSERT ON conversations WHEN new.role IN ('resident', 'operator', 'participant') BEGIN
    INSERT INTO conversations_fts(rowid, content) VALUES (new.rowid, new.content);
    INSERT INTO conversations_tri(rowid, content) VALUES (new.rowid, new.content);
END;
CREATE TRIGGER IF NOT EXISTS conversations_sidecar_ad AFTER DELETE ON conversations WHEN old.role IN ('resident', 'operator', 'participant') BEGIN
    INSERT INTO conversations_fts(conversations_fts, rowid, content) VALUES ('delete', old.rowid, old.content);
    INSERT INTO conversations_tri(conversations_tri, rowid, content) VALUES ('delete', old.rowid, old.content);
END;
CREATE TRIGGER IF NOT EXISTS conversations_sidecar_au AFTER UPDATE OF content, role ON conversations BEGIN
    INSERT INTO conversations_fts(conversations_fts, rowid, content) SELECT 'delete', old.rowid, old.content WHERE old.role IN ('resident', 'operator', 'participant');
    INSERT INTO conversations_tri(conversations_tri, rowid, content) SELECT 'delete', old.rowid, old.content WHERE old.role IN ('resident', 'operator', 'participant');
    INSERT INTO conversations_fts(rowid, content) SELECT new.rowid, new.content WHERE new.role IN ('resident', 'operator', 'participant');
    INSERT INTO conversations_tri(rowid, content) SELECT new.rowid, new.content WHERE new.role IN ('resident', 'operator', 'participant');
END;

-- provenance: sidecar of inbound
CREATE VIRTUAL TABLE IF NOT EXISTS inbound_fts USING fts5(body, content='inbound', content_rowid='rowid', tokenize='unicode61 remove_diacritics 2');
-- provenance: sidecar of inbound
CREATE VIRTUAL TABLE IF NOT EXISTS inbound_tri USING fts5(body, content='inbound', content_rowid='rowid', tokenize='trigram');
CREATE TRIGGER IF NOT EXISTS inbound_sidecar_ai AFTER INSERT ON inbound BEGIN
    INSERT INTO inbound_fts(rowid, body) VALUES (new.rowid, new.body);
    INSERT INTO inbound_tri(rowid, body) VALUES (new.rowid, new.body);
END;
CREATE TRIGGER IF NOT EXISTS inbound_sidecar_ad AFTER DELETE ON inbound BEGIN
    INSERT INTO inbound_fts(inbound_fts, rowid, body) VALUES ('delete', old.rowid, old.body);
    INSERT INTO inbound_tri(inbound_tri, rowid, body) VALUES ('delete', old.rowid, old.body);
END;
CREATE TRIGGER IF NOT EXISTS inbound_sidecar_au AFTER UPDATE OF body ON inbound BEGIN
    INSERT INTO inbound_fts(inbound_fts, rowid, body) VALUES ('delete', old.rowid, old.body);
    INSERT INTO inbound_tri(inbound_tri, rowid, body) VALUES ('delete', old.rowid, old.body);
    INSERT INTO inbound_fts(rowid, body) VALUES (new.rowid, new.body);
    INSERT INTO inbound_tri(rowid, body) VALUES (new.rowid, new.body);
END;

-- provenance: sidecar of plugin_memories
CREATE VIRTUAL TABLE IF NOT EXISTS plugin_memories_fts USING fts5(text, content='plugin_memories', content_rowid='rowid', tokenize='unicode61 remove_diacritics 2');
-- provenance: sidecar of plugin_memories
CREATE VIRTUAL TABLE IF NOT EXISTS plugin_memories_tri USING fts5(text, content='plugin_memories', content_rowid='rowid', tokenize='trigram');
CREATE TRIGGER IF NOT EXISTS plugin_memories_sidecar_ai AFTER INSERT ON plugin_memories BEGIN
    INSERT INTO plugin_memories_fts(rowid, text) VALUES (new.rowid, new.text);
    INSERT INTO plugin_memories_tri(rowid, text) VALUES (new.rowid, new.text);
END;
CREATE TRIGGER IF NOT EXISTS plugin_memories_sidecar_ad AFTER DELETE ON plugin_memories BEGIN
    INSERT INTO plugin_memories_fts(plugin_memories_fts, rowid, text) VALUES ('delete', old.rowid, old.text);
    INSERT INTO plugin_memories_tri(plugin_memories_tri, rowid, text) VALUES ('delete', old.rowid, old.text);
END;
CREATE TRIGGER IF NOT EXISTS plugin_memories_sidecar_au AFTER UPDATE OF text ON plugin_memories BEGIN
    INSERT INTO plugin_memories_fts(plugin_memories_fts, rowid, text) VALUES ('delete', old.rowid, old.text);
    INSERT INTO plugin_memories_tri(plugin_memories_tri, rowid, text) VALUES ('delete', old.rowid, old.text);
    INSERT INTO plugin_memories_fts(rowid, text) VALUES (new.rowid, new.text);
    INSERT INTO plugin_memories_tri(rowid, text) VALUES (new.rowid, new.text);
END;
