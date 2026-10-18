package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func testStore(t testing.TB) *Store {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "aii.db")
	raw, err := freshImage()
	if err != nil {
		t.Fatalf("initialize store template: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("copy initialized store template: %v", err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatalf("open initialized test store: %v", err)
	}
	s := &Store{db: db}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		t.Fatalf("ping initialized test store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestInitializedStoreTemplatesRemainIsolated(t *testing.T) {
	first := testStore(t)
	if err := first.AddConversationTurn("operator", "only in the first database"); err != nil {
		t.Fatal(err)
	}
	second := testStore(t)
	turns, err := second.RecentTurnsIncludingSystem(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 0 {
		t.Fatalf("second template copy inherited %d turn(s) from the first", len(turns))
	}
}

// testLedger creates a ledger in a temp directory. The ledger and store
// are separate — tests that need ledger seqs as FK targets must call
// s.Materialize(evt) to insert the ledger row into the store's mirror.
// It is closed when the test ends, before its directory is removed: an
// open ledger holds its directory lock, and Windows cannot remove an open
// file (whether a leaked one was still open depended on whether the
// collector had run its finalizer first).
func testLedger(t *testing.T, s *Store) *ledger.Ledger {
	t.Helper()
	dir := t.TempDir()
	l, err := ledger.New(filepath.Join(dir, "ledger.jsonl"))
	if err != nil {
		t.Fatalf("New ledger failed: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func testKeyPair(t *testing.T) *crypto.KeyPair {
	t.Helper()
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatalf("keygen failed: %v", err)
	}
	return kp
}

// TestBeliefMaterialize verifies that a belief.upsert event is correctly
// projected from the ledger into the beliefs table with all fields intact.
func TestBeliefMaterialize(t *testing.T) {
	s := testStore(t)
	kp := testKeyPair(t)
	l := testLedger(t, s)

	payload := map[string]any{
		"id":         "b_001",
		"statement":  "Testing is essential",
		"ring":       3,
		"confidence": 0.9,
	}
	evt, err := l.Append(ledger.EventBeliefUpsert, 3, payload, kp)
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	if err := s.Materialize(evt); err != nil {
		t.Fatalf("Materialize failed: %v", err)
	}

	beliefs, err := s.ListBeliefs()
	if err != nil {
		t.Fatalf("ListBeliefs failed: %v", err)
	}
	if len(beliefs) != 1 {
		t.Fatalf("expected 1 belief, got %d", len(beliefs))
	}
	if beliefs[0].Statement != "Testing is essential" {
		t.Errorf("statement = %q", beliefs[0].Statement)
	}
	if beliefs[0].Confidence != 0.9 {
		t.Errorf("confidence = %f", beliefs[0].Confidence)
	}
	if beliefs[0].Ring != 3 {
		t.Errorf("ring = %d", beliefs[0].Ring)
	}
}

// TestConversationStore verifies conversation turns can be stored and
// retrieved in chronological order via AddConversationTurn.
func TestConversationStore(t *testing.T) {
	s := testStore(t)

	turns := []struct {
		role    string
		content string
	}{
		{"operator", "First message"},
		{"resident", "Second message"},
		{"operator", "Third message"},
	}
	for _, turn := range turns {
		if err := s.AddConversationTurn(turn.role, turn.content); err != nil {
			t.Fatalf("AddConversationTurn failed: %v", err)
		}
	}

	result, err := s.RecentTurns(10)
	if err != nil {
		t.Fatalf("RecentTurns failed: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("expected 3 turns, got %d", len(result))
	}
	// RecentTurns returns chronological order
	for i, want := range turns {
		if result[i].Role != want.role {
			t.Errorf("turn %d role = %q, want %q", i, result[i].Role, want.role)
		}
		if result[i].Content != want.content {
			t.Errorf("turn %d content = %q, want %q", i, result[i].Content, want.content)
		}
	}

	full, err := s.RecentTurnsIncludingSystem(10)
	if err != nil {
		t.Fatalf("RecentTurnsIncludingSystem failed: %v", err)
	}
	if len(full) != 3 {
		t.Fatalf("expected 3 transcript turns, got %d", len(full))
	}
	for i, want := range turns {
		if full[i].Role != want.role || full[i].Content != want.content {
			t.Errorf("transcript turn %d = %q/%q, want %q/%q — replay order is not chronological",
				i, full[i].Role, full[i].Content, want.role, want.content)
		}
	}
}

// TestLifetimeTicks verifies the life clock singleton.
func TestLifetimeTicks(t *testing.T) {
	s := testStore(t)
	kp := testKeyPair(t)
	l := testLedger(t, s)

	// Birth event initializes identity_lifetime
	evt, _ := l.Append(ledger.EventRing0Genesis, 0,
		map[string]string{"name": "test"}, kp)
	s.Materialize(evt)

	ticks, _ := s.LifetimeTicks()
	if ticks != 0 {
		t.Errorf("initial ticks = %d, want 0", ticks)
	}

	for i := 0; i < 3; i++ {
		s.IncrementLifetimeTicks()
	}
	ticks, _ = s.LifetimeTicks()
	if ticks != 3 {
		t.Errorf("ticks = %d, want 3", ticks)
	}
}

// TestOutbox verifies the send/deliver lifecycle.
func TestOutbox(t *testing.T) {
	s := testStore(t)
	kp := testKeyPair(t)
	l := testLedger(t, s)

	// Need ledger events for the outbox FK (created_seq REFERENCES ledger(seq))
	evt1, _ := l.Append(ledger.EventExperienceCreate, 3,
		map[string]string{"id": "e1", "content": "send event 1", "category": "communication"}, kp)
	s.Materialize(evt1)

	evt2, _ := l.Append(ledger.EventExperienceCreate, 3,
		map[string]string{"id": "e2", "content": "send event 2", "category": "communication"}, kp)
	s.Materialize(evt2)

	seq1 := evt1.Seq
	seq2 := evt2.Seq
	s.AddOutboxMessage("msg_1", "operator", "", "First message", &seq1)
	s.AddOutboxMessage("msg_2", "operator", "", "Second message", &seq2)

	msgs, err := s.UndeliveredMessages()
	if err != nil {
		t.Fatalf("UndeliveredMessages failed: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Content != "First message" {
		t.Errorf("first message = %q, want %q", msgs[0].Content, "First message")
	}

	if err := pageTakes(s, "msg_1"); err != nil {
		t.Fatal(err)
	}
	msgs, _ = s.UndeliveredMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 undelivered after delivery, got %d", len(msgs))
	}
	if msgs[0].ID != "msg_2" {
		t.Errorf("remaining message ID = %q, want msg_2", msgs[0].ID)
	}
	added, err := s.AddOutboxMessageOnce("once", "operator", "", "one", nil)
	if err != nil || !added {
		t.Fatalf("first idempotent delivery: added=%v err=%v", added, err)
	}
	added, err = s.AddOutboxMessageOnce("once", "operator", "", "one", nil)
	if err != nil || added {
		t.Fatalf("duplicate idempotent delivery: added=%v err=%v", added, err)
	}
	if _, err := s.AddOutboxMessageOnce("invalid", "invalid-role", "", "bad", nil); err == nil {
		t.Fatal("non-duplicate constraint failure was reported as an existing delivery")
	}
}

// TestAlarms verifies alarm set/query/delete lifecycle.
func TestAlarms(t *testing.T) {
	s := testStore(t)

	s.SetAlarm("alarm_1", "morning_brief", "wall", 1700000000, nil, "")
	repeat := int64(300000)
	s.SetAlarm("alarm_2", "heartbeat", "wall", 1700000300, &repeat, "")

	alarms, _ := s.DueAlarms("wall", 1700000000, 10)
	if len(alarms) != 1 {
		t.Fatalf("expected 1 due alarm, got %d", len(alarms))
	}
	if alarms[0].AlarmID != "alarm_1" {
		t.Errorf("alarm ID = %q, want alarm_1", alarms[0].AlarmID)
	}

	alarms, _ = s.DueAlarms("wall", 1700000300, 10)
	if len(alarms) != 2 {
		t.Errorf("expected 2 due alarms, got %d", len(alarms))
	}

	// Delete one-shot
	s.DeleteAlarm("alarm_1")
	alarms, _ = s.DueAlarms("wall", 1700000000, 10)
	if len(alarms) != 0 {
		t.Errorf("expected 0 after delete, got %d", len(alarms))
	}
}

// TestStats verifies aggregate counts.
func TestStats(t *testing.T) {
	s := testStore(t)
	kp := testKeyPair(t)
	l := testLedger(t, s)

	evt1, _ := l.Append(ledger.EventRing0Genesis, 0,
		map[string]string{"name": "test"}, kp)
	s.Materialize(evt1)

	evt2, _ := l.Append(ledger.EventBeliefUpsert, 3,
		map[string]any{"id": "b1", "statement": "test belief", "ring": 3, "confidence": 0.5}, kp)
	s.Materialize(evt2)

	evt3, _ := l.Append(ledger.EventExperienceCreate, 3,
		map[string]string{"id": "e1", "content": "test experience", "category": "test"}, kp)
	s.Materialize(evt3)
	s.AddConversationTurn("resident", "test message")

	stats, err := s.GetStats()
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}
	if stats.BeliefCount != 1 {
		t.Errorf("belief count = %d, want 1", stats.BeliefCount)
	}
	if stats.ExperienceCount != 1 {
		t.Errorf("experience count = %d, want 1", stats.ExperienceCount)
	}
	if stats.ConversationCount != 1 {
		t.Errorf("conversation count = %d, want 1", stats.ConversationCount)
	}
	if stats.LedgerSeq != 3 {
		t.Errorf("ledger seq = %d, want 3", stats.LedgerSeq)
	}
}

func TestStatsBeliefCountMatchesLiveFilter(t *testing.T) {
	s := testStore(t)
	kp := testKeyPair(t)
	l := testLedger(t, s)

	// Two live beliefs.
	evt1, _ := l.Append(ledger.EventBeliefUpsert, 3,
		map[string]any{"id": "b1", "statement": "live one", "ring": 3, "confidence": 0.5}, kp)
	s.Materialize(evt1)
	evt2, _ := l.Append(ledger.EventBeliefUpsert, 3,
		map[string]any{"id": "b2", "statement": "live two", "ring": 3, "confidence": 0.5}, kp)
	s.Materialize(evt2)

	stats, _ := s.GetStats()
	if stats.BeliefCount != 2 {
		t.Fatalf("baseline belief count = %d, want 2", stats.BeliefCount)
	}

	// Supersede b1 by b2 — the engine's canonical event shape
	// (prompts.go floor, consolidate.go, commit.go all agree: old_id/new_id).
	evt3, _ := l.Append(ledger.EventBeliefSupersede, 3,
		map[string]any{"old_id": "b1", "new_id": "b2", "reason": "test"}, kp)
	s.Materialize(evt3)

	stats, _ = s.GetStats()
	if stats.BeliefCount != 1 {
		t.Errorf("after supersession, count = %d, want 1 (list must agree)", stats.BeliefCount)
	}
	live, err := s.ListBeliefs()
	if err != nil {
		t.Fatalf("ListBeliefs failed: %v", err)
	}
	if len(live) != stats.BeliefCount {
		t.Errorf("count (%d) and list (%d) disagree — the anomaly is back", stats.BeliefCount, len(live))
	}

	// Archive b2 — the count must drop again.
	evt4, _ := l.Append(ledger.EventBeliefArchive, 3,
		map[string]any{"id": "b2"}, kp)
	s.Materialize(evt4)

	stats, _ = s.GetStats()
	if stats.BeliefCount != 0 {
		t.Errorf("after archive, count = %d, want 0 (list must agree)", stats.BeliefCount)
	}
	live, _ = s.ListBeliefs()
	if len(live) != stats.BeliefCount {
		t.Errorf("count (%d) and list (%d) disagree after archive", stats.BeliefCount, len(live))
	}
}

// TestWorkSession verifies the work session lifecycle via store methods.
func TestWorkSession(t *testing.T) {
	s := testStore(t)
	kp := testKeyPair(t)
	l := testLedger(t, s)

	// Need a ledger event for the work_sessions FK (created_seq REFERENCES ledger(seq))
	evt, _ := l.Append(ledger.EventRing0Genesis, 0,
		map[string]string{"name": "test"}, kp)
	s.Materialize(evt)

	// Start a work session via the store API
	if err := s.StartWorkSession(t.Context(), "ws_1", "Analyzing files"); err != nil {
		t.Fatalf("StartWorkSession failed: %v", err)
	}

	// Update Ring 4 state
	if err := s.UpdateWorkState(t.Context(), "ws_1", "Step 3 of 5 complete"); err != nil {
		t.Fatalf("UpdateWorkState failed: %v", err)
	}

	ws, err := s.ActiveWorkSession()
	if err != nil {
		t.Fatalf("ActiveWorkSession failed: %v", err)
	}
	if ws == nil {
		t.Fatal("expected active work session")
	}
	if ws.Description != "Analyzing files" {
		t.Errorf("description = %q", ws.Description)
	}
	if ws.State != "Step 3 of 5 complete" {
		t.Errorf("state = %q", ws.State)
	}

	// Deliver
	if err := s.DeliverWorkSession(t.Context(), "ws_1", "Done", "", ""); err != nil {
		t.Fatalf("DeliverWorkSession failed: %v", err)
	}
	ws, _ = s.ActiveWorkSession()
	if ws != nil {
		t.Error("expected no active work session after delivery")
	}
}

func TestToolEventRecordsResult(t *testing.T) {
	s := testStore(t)

	if err := recordToolCall(t, s, "bash", "echo hi", "hello\nworld"); err != nil {
		t.Fatal(err)
	}
	turns, err := s.RecentTurnsIncludingSystem(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) == 0 {
		t.Fatal("no transcript entries")
	}
	// RecentTurnsIncludingSystem returns chronological (oldest→newest);
	// the newest turn is the LAST element.
	last := turns[len(turns)-1]
	if !strings.Contains(last.Content, "→ bash(echo hi)") {
		t.Fatalf("call missing: %q", last.Content)
	}
	if !strings.Contains(last.Content, "← hello") {
		t.Fatalf("result excerpt missing — transcript record is incomplete: %q", last.Content)
	}

	// Large result: bounded excerpt with an honest marker
	big := strings.Repeat("界", TranscriptResultLimit+5000)
	if err := recordToolCall(t, s, "bash", "cat big", big); err != nil {
		t.Fatal(err)
	}
	turns, _ = s.RecentTurnsIncludingSystem(10)
	last = turns[len(turns)-1]
	if !strings.Contains(last.Content, "result excerpt ends") {
		t.Fatalf("bounded excerpt must carry an honest truncation marker, got %d chars", len(last.Content))
	}
	if !utf8.ValidString(last.Content) {
		t.Fatal("excerpt split a UTF-8 character")
	}
	if n := utf8.RuneCountInString(last.Content); n > TranscriptResultLimit+500 {
		t.Fatalf("excerpt not bounded: %d characters", n)
	}
}

func TestSetAlarmOwnerBound(t *testing.T) {
	s := testStore(t)
	defer s.Close()

	if err := s.SetAlarm("a1", "dream", "life", 10, nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAlarm("a1", "consolidate", "life", 20, nil, ""); err == nil {
		t.Fatal("upsert with a different owner must be rejected")
	}
	// same owner replaces fine
	if err := s.SetAlarm("a1", "dream", "life", 30, nil, ""); err != nil {
		t.Fatalf("same-owner replace must succeed: %v", err)
	}
	// idempotent same-owner same-values set is fine
	if err := s.SetAlarm("a1", "dream", "life", 30, nil, ""); err != nil {
		t.Fatalf("idempotent set must succeed: %v", err)
	}
}

func TestListIntentionsReturnsHistory(t *testing.T) {
	s := testStore(t)
	kp := testKeyPair(t)
	l := testLedger(t, s)
	defer s.Close()
	defer l.Close()

	mat := func(et ledger.EventType, payload map[string]any) {
		t.Helper()
		evt, err := l.Append(et, 3, payload, kp)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Materialize(evt); err != nil {
			t.Fatal(err)
		}
	}
	mat(ledger.EventIntentionCreate, map[string]any{"id": "i1", "statement": "first"})
	mat(ledger.EventIntentionStateChange, map[string]any{"id": "i1", "state": "completed"})
	mat(ledger.EventIntentionCreate, map[string]any{"id": "i2", "statement": "second"})
	mat(ledger.EventIntentionCreate, map[string]any{"id": "i3", "statement": "third"})
	mat(ledger.EventIntentionStateChange, map[string]any{"id": "i3", "state": "abandoned"})

	ints, err := s.ListIntentions()
	if err != nil {
		t.Fatal(err)
	}
	if len(ints) != 3 {
		t.Fatalf("want all 3 intentions, got %d", len(ints))
	}
	if ints[0].State != "active" {
		t.Fatalf("active must sort first, got %s", ints[0].State)
	}
}

func TestListRawExperiences(t *testing.T) {
	s := testStore(t)
	seq := 0
	mat := func(id, content string) {
		seq++
		evt := ledger.Event{Seq: uint64(seq), Type: ledger.EventExperienceCreate,
			Ring: 3, Timestamp: "2026-08-17T10:00:00Z"}
		payload := map[string]any{"id": id, "content": content, "category": "observation"}
		if id != "exp_raw_1" && id != "exp_raw_2" {
			payload["raw"] = false
		}
		b, _ := json.Marshal(payload)
		evt.Payload = b
		if err := s.Materialize(&evt); err != nil {
			t.Fatal(err)
		}
	}
	mat("exp_raw_1", "oldest raw note")
	mat("exp_raw_2", "second raw note")
	mat("exp_done_1", "processed newer")
	mat("exp_done_2", "processed newest")

	got, err := s.ListRawExperiences(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want the 2 raw experiences, got %d", len(got))
	}
	if got[0].Content != "oldest raw note" || got[1].Content != "second raw note" {
		t.Fatalf("raw queue must be oldest-first, got [%s, %s]", got[0].Content, got[1].Content)
	}
}

func TestStandingDerivation(t *testing.T) {
	s := testStore(t)
	seq := 0
	mat := func(et ledger.EventType, ring int, payload map[string]any) {
		seq++
		evt := ledger.Event{Seq: uint64(seq), Type: et, Ring: ring, Timestamp: "2026-08-17T10:00:00Z"}
		b, _ := json.Marshal(payload)
		evt.Payload = b
		if err := s.Materialize(&evt); err != nil {
			t.Fatalf("materialize %s: %v", et, err)
		}
	}

	mat(ledger.EventRing0Genesis, 0, map[string]any{"name": "S"}) // life clock singleton
	mat(ledger.EventBeliefUpsert, 3, map[string]any{"id": "b", "statement": "stand", "ring": 3, "confidence": 0.5})
	if got := mustStanding(t, s, "b"); got != "new" {
		t.Fatalf("edgeless belief: %q, want new", got)
	}

	// Two of the resident's own notes and one that records the operator
	// are 3 distinct sources → confirmed. A note of operator provenance
	// carries its citation (H3): it cites a REAL operator turn.
	if err := s.AddConversationTurn("operator", "I saw it hold"); err != nil {
		t.Fatal(err)
	}
	var opTurn uint64
	s.db.QueryRow(`SELECT MAX(turn_seq) FROM conversations`).Scan(&opTurn)
	for _, x := range []struct{ id, prov string }{
		{"r1", "self"}, {"r2", "self"}, {"o1", "operator"},
	} {
		payload := map[string]any{"id": x.id, "content": "obs", "category": "observation", "provenance": x.prov}
		if x.prov == "operator" {
			payload["source_turn"] = opTurn
		}
		mat(ledger.EventExperienceCreate, 3, payload)
		mat(ledger.EventEdgeCreate, 3, map[string]any{"id": "e_" + x.id, "from_id": x.id, "to_id": "b", "edge_type": "SUPPORTS"})
	}
	if got := mustStanding(t, s, "b"); got != "confirmed" {
		t.Fatalf("3 distinct sources: %q, want confirmed", got)
	}

	mat(ledger.EventBeliefUpsert, 3, map[string]any{"id": "b_self", "statement": "echo", "ring": 3, "confidence": 0.5})
	for i, x := range []string{"s1", "s2", "s3"} {
		mat(ledger.EventExperienceCreate, 3, map[string]any{"id": x, "content": "obs", "category": "observation"})
		mat(ledger.EventEdgeCreate, 3, map[string]any{"id": "e_" + x, "from_id": x, "to_id": "b_self", "edge_type": "SUPPORTS"})
		want := "new"
		if i == 2 {
			want = "confirmed"
		}
		if got := mustStanding(t, s, "b_self"); got != want {
			t.Fatalf("%d of the resident's own sources: %q, want %s", i+1, got, want)
		}
	}

	// Contradiction → suspect (overrides everything)
	mat(ledger.EventExperienceCreate, 3, map[string]any{"id": "c1", "content": "counter", "category": "observation"})
	mat(ledger.EventEdgeCreate, 3, map[string]any{"id": "e_c1", "from_id": "c1", "to_id": "b", "edge_type": "CONTRADICTS"})
	if got := mustStanding(t, s, "b"); got != "suspect" {
		t.Fatalf("contradicted belief: %q, want suspect", got)
	}

	// Resolution: archive the contradiction → standing returns from the edges
	mat(ledger.EventEdgeArchive, 3, map[string]any{"id": "e_c1"})
	if got := mustStanding(t, s, "b"); got != "confirmed" {
		t.Fatalf("archived contradiction: %q, want confirmed", got)
	}

	setTicks := func(n int64) {
		s.mu.Lock()
		_, err := s.db.Exec(`UPDATE identity_lifetime SET lifetime_ticks = ? WHERE singleton_id = 'current'`, n)
		s.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	setTicks(10)
	mat(ledger.EventConsolidationRun, 3, map[string]any{"inputs": []string{"r1"}, "outputs": []uint64{}, "confirmed": []map[string]any{{"id": "b", "ticks": 10}}})
	if got := mustStanding(t, s, "b"); got != "confirmed" { // 10-10 < 50 → not yet
		t.Fatalf("freshly anchored: %q, want confirmed (time not yet elapsed)", got)
	}
	setTicks(61)
	if got := mustStanding(t, s, "b"); got != "trusted" {
		t.Fatalf("61 ticks past a tick-10 anchor: %q, want trusted", got)
	}
}

func TestNoGrandfather(t *testing.T) {
	s := testStore(t)
	evt := ledger.Event{Seq: 1, Type: ledger.EventRelationshipUpsert, Ring: 1,
		Timestamp: "2026-08-17T09:00:00Z"}
	b, _ := json.Marshal(map[string]any{
		"id": "rel-old", "counterpart_name": "Op", "counterpart_role": "operator",
		"relationship_type": "founding_operator",
		// excerpt present, basis absent
		"operator_approval_excerpt": "Operator Op approved this relationship.",
	})
	evt.Payload = b
	if err := s.Materialize(&evt); err == nil {
		t.Fatal("basis-less operator relationship must fail closed — no grandfather in an unreleased system")
	}
	if err := testStore(t).ReplayAll(EventSlice([]ledger.Event{evt})); err == nil {
		t.Fatal("same in replay — invariants are mode-independent")
	}
}

func TestReplayPureFreshDBRebuild(t *testing.T) {
	// One chain: birth → two operator relationships carrying signed
	// conversation-turn citations.
	build := func(s *Store) []ledger.Event {
		var events []ledger.Event
		mat := func(et ledger.EventType, ring int, payload map[string]any) {
			evt := ledger.Event{Seq: uint64(len(events) + 1), Type: et, Ring: ring,
				Timestamp: "2026-08-17T09:00:00Z"}
			payloadBytes, _ := json.Marshal(payload)
			evt.Payload = payloadBytes
			if err := s.Materialize(&evt); err != nil {
				t.Fatalf("live materialize %s: %v", et, err)
			}
			events = append(events, evt)
		}
		mat(ledger.EventRing0Genesis, 0, map[string]any{"name": "ReplayIdentity"})
		mat(ledger.EventRelationshipUpsert, 1, map[string]any{
			"id": "rel-founding", "counterpart_name": "Op", "counterpart_role": "operator",
			"relationship_type": "founding_operator", "charter_text": "founding",
			"operator_approval_excerpt": "Yes — rel-founding.",
			"operator_approval_turn":    float64(7),
			"approval_basis":            "conversation_turn",
		})
		mat(ledger.EventRelationshipUpsert, 1, map[string]any{
			"id": "rel-verb", "counterpart_name": "Op", "counterpart_role": "operator",
			"relationship_type": "peer", "charter_text": "grew from the exchange",
			"supersedes":                "rel-founding",
			"operator_approval_excerpt": "Yes — I want us to keep working together.",
			"operator_approval_turn":    float64(7),
			"approval_basis":            "conversation_turn",
		})
		return events
	}

	origin := testStore(t)
	// The process witness: the operator's affirmative turn exists live.
	// (Seven turns so the cited one is turn_seq 7; the live cross-check
	// below proves the witness path still runs.)
	for i := 0; i < 7; i++ {
		if err := origin.AddConversationTurn("operator", fmt.Sprintf("turn %d", i+1)); err != nil {
			t.Fatal(err)
		}
	}
	events := build(origin)
	origin.Close()

	// Fresh DB, EMPTY conversations (the DB-loss case): replay must be total.
	fresh := testStore(t)
	defer fresh.Close()
	if err := fresh.ReplayAll(EventSlice(events)); err != nil {
		t.Fatalf("fresh-DB replay must be total (f(ledger) is pure): %v", err)
	}
	var relCount int
	if err := fresh.db.QueryRow(`SELECT COUNT(*) FROM relationships`).Scan(&relCount); err != nil {
		t.Fatal(err)
	}
	if relCount != 2 {
		t.Fatalf("fresh rebuild must land BOTH relationships, got %d", relCount)
	}

	// Malformed citation still fails closed in BOTH modes: a
	// conversation_turn basis without a turn number is a malformed
	// assertion, live or replay.
	bad := ledger.Event{Seq: 1, Type: ledger.EventRelationshipUpsert, Ring: 1,
		Timestamp: "2026-08-17T09:00:00Z"}
	b, _ := json.Marshal(map[string]any{
		"id": "rel-bad", "counterpart_name": "Op", "counterpart_role": "operator",
		"operator_approval_excerpt": "x", "approval_basis": "conversation_turn",
	})
	bad.Payload = b
	if err := testStore(t).ReplayAll(EventSlice([]ledger.Event{bad})); err == nil {
		t.Fatal("conversation_turn basis without a turn must fail closed in replay")
	}
}

func TestConversationTurnCount(t *testing.T) {
	s := testStore(t)
	for i := 0; i < 5; i++ {
		s.AddConversationTurn("operator", fmt.Sprintf("m%d", i))
	}
	n, err := s.ConversationTurnCount()
	if err != nil || n != 5 {
		t.Fatalf("count = %d %v, want 5", n, err)
	}
	s.AddConversationTurn("system", "tool event")
	n, _ = s.ConversationTurnCount()
	if n != 5 {
		t.Fatalf("system rows are not conversation: %d", n)
	}
}

// The brief lives in ring_snapshots under ring_level 0 as a single-slot
// sentinel. Any caller restoring ring content would install it as Ring 0
// — the constitution's level — unless every one of them remembered to
// skip it, and the protection was a single `if` in one caller. Excluded
// at the source now, so the function that returns ring sections cannot
// return the thing that is not one.
func TestBriefIsNotARingSection(t *testing.T) {
	st := testStore(t)

	if err := st.SaveBrief("this morning you woke to three open threads"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveRingSection(3, "working_truth", "you believe the build is green"); err != nil {
		t.Fatal(err)
	}

	snaps, err := st.RingSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	for _, sn := range snaps {
		if sn.Section == briefSection {
			t.Fatalf("the brief came back as a ring section at level %d — a caller would restore it as Ring 0", sn.RingLevel)
		}
	}
	if len(snaps) != 1 || snaps[0].Section != "working_truth" {
		t.Fatalf("real ring sections must still be returned: %+v", snaps)
	}

	got, err := st.GetBrief()
	if err != nil {
		t.Fatal(err)
	}
	if got != "this morning you woke to three open threads" {
		t.Fatalf("the brief is no longer readable by its own accessor: %q", got)
	}
}

func TestTheBriefIsReadBackWithWhenItWasWritten(t *testing.T) {
	st := testStore(t)
	if content, written, err := st.Brief(); err != nil || content != "" || !written.IsZero() {
		t.Fatalf("a store with no brief answered %q, written %s, %v", content, written, err)
	}
	before := time.Now().Add(-time.Second)
	if err := st.SaveBrief("three open threads"); err != nil {
		t.Fatal(err)
	}
	content, written, err := st.Brief()
	if err != nil {
		t.Fatal(err)
	}
	if content != "three open threads" || written.Before(before) || written.After(time.Now().Add(time.Second)) {
		t.Fatalf("THE BRIEF CAME BACK WITHOUT THE TIME IT WAS WRITTEN: %q, written %s", content, written)
	}
	// A row whose date cannot be read comes back with its brief and the
	// zero time, and no error: the caller holds a brief it cannot date.
	if _, err := st.DB().Exec(`UPDATE ring_snapshots SET updated_at = 'last Monday' WHERE section = ?`, briefSection); err != nil {
		t.Fatal(err)
	}
	if content, written, err := st.Brief(); err != nil || content != "three open threads" || !written.IsZero() {
		t.Fatalf("A BRIEF WHOSE DATE CANNOT BE READ CAME BACK AS %q, written %s, %v; want the brief, the zero time and no error", content, written, err)
	}
}

// The typed execution record: a call written before execution and its
// result, with exact identity — host id, turn+ordinal, actor, model — and
// a typed outcome: succeeded, failed, or unknown when a boot found it
// open. Payloads are bounded records; the operator excerpt is the row.
func TestToolExecutionLifecycleRecord(t *testing.T) {
	s := testStore(t)
	if err := s.RecordToolStart("turn_1", 1, "main", "fake-model", "c1", "read", `{"file_path":"/a"}`); err != nil {
		t.Fatal(err)
	}
	if err := recordToolDone(s, "turn_1", 1, "read", `{"file_path":"/a"}`, "full contents here", interaction.Succeeded, false); err != nil {
		t.Fatal(err)
	}
	// A crash: started, never done — different actor, so the record can
	// tell whose it was.
	if err := s.RecordToolStart("turn_2", 1, "ws_sub", "fake-model", "c2", "write", `{"file_path":"/b"}`); err != nil {
		t.Fatal(err)
	}

	var outcome, actor, model string
	if err := s.db.QueryRow(`SELECT r.outcome, json_extract(c.details,'$.actor'), json_extract(c.details,'$.model')
		FROM conversations c JOIN conversations r ON r.related_id=c.id AND r.kind='tool_result'
		WHERE c.kind='tool_call' AND c.turn_id='turn_1'`).Scan(&outcome, &actor, &model); err != nil {
		t.Fatal(err)
	}
	if outcome != "succeeded" || actor != "main" || model != "fake-model" {
		t.Fatalf("record = (%s,%s,%s), want (succeeded,main,fake-model)", outcome, actor, model)
	}
	if open, err := s.ToolCallStarted("turn_1", 1); err != nil || open {
		t.Fatalf("a completed call reads as open: %v %v", open, err)
	}
	if open, err := s.ToolCallStarted("turn_2", 1); err != nil || !open {
		t.Fatalf("the interrupted call does not read as open: %v %v", open, err)
	}

	names, err := s.AbandonUnfinishedToolCalls()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "write" {
		t.Fatalf("abandoned = %v, want exactly the interrupted write", names)
	}
	if again, _ := s.AbandonUnfinishedToolCalls(); len(again) != 0 {
		t.Fatalf("second pass found %v — the outcome did not stick", again)
	}
	if err := s.db.QueryRow(`SELECT outcome FROM conversations WHERE kind='tool_result' AND turn_id='turn_2'`).Scan(&outcome); err != nil || outcome != "unknown" {
		t.Fatalf("the interrupted call's outcome = %q %v, want unknown", outcome, err)
	}

	done, failed, truncated, err := s.ToolResultStats(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if done != 1 || failed != 0 || truncated != 0 {
		t.Fatalf("stats = (%d,%d,%d), want (1,0,0): an unknown outcome is not a completion", done, failed, truncated)
	}
}

// A READ OF HEARD SPEECH IS RECORDED WHOLE, its query with it. What the
// identity read is held in process memory for minutes and is on no other
// record, so an excerpt would leave the operator unable to read what the
// identity read. An ordinary result of the same length keeps its excerpt.
// The execution record's identity of a result stays length and digest.
func TestAHeardReadIsRecordedWhole(t *testing.T) {
	s := testStore(t)
	result := `{"rows":[{"text":"withheld banking sentence"},{"text":"` + strings.Repeat("words ", 1500) + `"},{"text":"the last row said cobalt harbour"}]}`
	record := func(turn, args string) string {
		t.Helper()
		if err := s.RecordToolStart(turn, 1, "main", "m", "call-1", "recall", args); err != nil {
			t.Fatal(err)
		}
		if err := recordToolDone(s, turn, 1, "recall", args, result, interaction.Succeeded, false); err != nil {
			t.Fatal(err)
		}
		var content, resultRecord string
		if err := s.db.QueryRow(`SELECT content, json_extract(details,'$.result_record') FROM conversations WHERE kind='tool_result' AND turn_id=?`, turn).Scan(&content, &resultRecord); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(resultRecord, "banking") || !strings.Contains(resultRecord, "sha256=") {
			t.Fatalf("%s: the execution record's result is its length and digest: %q", turn, resultRecord)
		}
		return content
	}
	// The record judges a heard read by the key the executor reads: a
	// case-variant duplicate of "source" is the same heard read. It is
	// judged on the arguments as sent: one longer than the argument
	// excerpt is still a heard read, and is kept to its last word.
	for i, args := range []string{
		`{"source":"heard","query":"private cobalt phrase"}`,
		`{"source":"heard","SOURCE":"memory","query":"private cobalt phrase"}`,
		`{"source":"heard","ſource":"memory","query":"private cobalt phrase"}`,
		`{"after_seq":0,"decay":"none","exact":false,"limit":20,"query":"private cobalt phrase ` + strings.Repeat("and more ", 40) + `to its end","source":"heard"}`,
	} {
		got := record(fmt.Sprintf("heard-%d", i), args)
		if strings.Contains(args, "to its end") && !strings.Contains(got, `to its end","source":"heard"}`) {
			t.Fatalf("a heard read's long arguments were cut: %q", got[:min(len(got), 300)])
		}
		if !strings.Contains(got, "private cobalt phrase") || !strings.Contains(got, "withheld banking sentence") || !strings.Contains(got, "the last row said cobalt harbour") || strings.Contains(got, "result excerpt ends") {
			t.Fatalf("%s: a heard read is not in the record whole: %d characters, ends %q", args, len(got), got[max(0, len(got)-80):])
		}
	}
	if got := record("ordinary", `{"query":"private cobalt phrase"}`); strings.Contains(got, "the last row said cobalt harbour") || !strings.Contains(got, "result excerpt ends") {
		t.Fatalf("an ordinary result of the same length lost its excerpt limit: %d characters", len(got))
	}
}

func TestToolRecordCustodyPolicy(t *testing.T) {
	s := testStore(t)
	secretArgs := `{"command":"curl -H \'Authorization: Bearer sk-SECRETTOKEN-` + strings.Repeat("a", 300) + `\'"}`
	secretResult := "BEGIN PRIVATE KEY " + strings.Repeat("s3cr3t-", 700) // ~5k chars
	if err := s.RecordToolStart("turn_b", 1, "main", "m", "c", "shell", secretArgs); err != nil {
		t.Fatal(err)
	}
	if err := recordToolDone(s, "turn_b", 1, "shell", secretArgs, secretResult, interaction.Succeeded, false); err != nil {
		t.Fatal(err)
	}
	var argsRec, resRec string
	if err := s.db.QueryRow(`SELECT json_extract(details,'$.args_record'), json_extract(details,'$.result_record') FROM conversations WHERE kind='tool_result' AND turn_id='turn_b'`).
		Scan(&argsRec, &resRec); err != nil {
		t.Fatal(err)
	}
	var callArgs string
	if err := s.db.QueryRow(`SELECT json_extract(details,'$.args_record') FROM conversations WHERE kind='tool_call' AND turn_id='turn_b'`).Scan(&callArgs); err != nil || callArgs != argsRec {
		t.Fatalf("the call's args record %q is not its result's %q: %v", callArgs, argsRec, err)
	}

	if strings.Contains(argsRec, "SECRETTOKEN") {
		t.Fatalf("args_record carries the bearer token — custody is identity only: %q", argsRec)
	}
	if !strings.Contains(argsRec, "sha256=") || !strings.Contains(argsRec, "chars") {
		t.Fatalf("args_record lost identity: %q", argsRec)
	}
	if strings.Contains(resRec, "s3cr3t") || strings.Contains(resRec, "PRIVATE KEY") {
		t.Fatalf("result_record carries payload — custody default is identity only: %q", resRec[:80])
	}
	if !strings.Contains(resRec, "sha256=") || !strings.Contains(resRec, "chars") {
		t.Fatalf("result_record lost identity: %q", resRec)
	}
}

func TestRecordToolDoneMeasuredRequiresExactlyOneOpenCall(t *testing.T) {
	s := testStore(t)
	// No started row: refuse, and no excerpt row may appear.
	var before int
	s.db.QueryRow(`SELECT COUNT(*) FROM conversations`).Scan(&before)
	if err := recordToolDone(s, "turn_x", 1, "read", `{}`, "ghost", interaction.Succeeded, false); err == nil {
		t.Fatal("done with no started row was accepted — a completion was invented")
	}
	var after int
	s.db.QueryRow(`SELECT COUNT(*) FROM conversations`).Scan(&after)
	if after != before {
		t.Fatalf("refused completion still wrote an excerpt (%d -> %d)", before, after)
	}
	// One started row: done succeeds ONCE; the second done refuses.
	if err := s.RecordToolStart("turn_x", 2, "main", "m", "c", "read", `{}`); err != nil {
		t.Fatal(err)
	}
	if err := recordToolDone(s, "turn_x", 2, "read", `{}`, "real", interaction.Succeeded, false); err != nil {
		t.Fatal(err)
	}
	s.db.QueryRow(`SELECT COUNT(*) FROM conversations`).Scan(&after)
	if after != before+2 {
		t.Fatalf("attempt and outcome wrote %d rows, want 2", after-before)
	}
	var linked int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM conversations outcome JOIN conversations attempt ON outcome.related_id=attempt.id WHERE outcome.kind='tool_result' AND attempt.kind='tool_call' AND outcome.source_id=attempt.source_id`).Scan(&linked); err != nil || linked != 1 {
		t.Fatalf("outcome does not link one recorded attempt: %d %v", linked, err)
	}
	if err := recordToolDone(s, "turn_x", 2, "read", `{}`, "again", interaction.Succeeded, false); err == nil {
		t.Fatal("a second completion of the same execution was accepted")
	}
	// A start is recorded once: the same start again is refused, not merged.
	if err := s.RecordToolStart("turn_x", 2, "main", "m", "c", "read", `{}`); err == nil {
		t.Fatal("a second start of the same execution was accepted")
	}
}

// THE RETIREMENT KEEPS THE CRASH WARNING (Q14). Through 0.1.9 a started
// execution lived in tool_events alone. A database last run by such a
// binary, opened by this one, loses the table; the started write it held
// must still be named by the boot, beside a call the log already holds,
// and a completed execution must not be.
func TestARetiredToolRecordsUnfinishedCallsAreNamedAtBoot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aii.db")
	st, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	// Since 0.1.10 the log holds the call too.
	if err := st.RecordToolStart("t2", 1, "main", "m", "c2", "edit", `{}`); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	db := openRawForTest(t, path)
	for _, q := range []string{
		`CREATE TABLE tool_events (execution_id TEXT PRIMARY KEY, turn_id TEXT NOT NULL, ordinal INTEGER NOT NULL,
		 actor TEXT NOT NULL, model TEXT NOT NULL DEFAULT '', provider_call_id TEXT NOT NULL DEFAULT '', tool TEXT NOT NULL,
		 args_record TEXT NOT NULL, state TEXT NOT NULL CHECK (state IN ('started','done','abandoned')),
		 failed INTEGER NOT NULL DEFAULT 0, truncated INTEGER NOT NULL DEFAULT 0, result_record TEXT NOT NULL DEFAULT '',
		 started_ms INTEGER NOT NULL, finished_ms INTEGER NOT NULL DEFAULT 0, UNIQUE (turn_id, ordinal)) STRICT`,
		`INSERT INTO tool_events (execution_id,turn_id,ordinal,actor,model,provider_call_id,tool,args_record,state,started_ms) VALUES
		 ('` + toolExecutionID("t1", 1) + `','t1',1,'main','m','c1','read','[2 chars, sha256=00]','done',1000),
		 ('` + toolExecutionID("t2", 1) + `','t2',1,'main','m','c2','edit','[2 chars, sha256=00]','started',1001),
		 ('` + toolExecutionID("t3", 4) + `','t3',4,'ws_sub','m','c3','write','[18 chars, sha256=11]','started',1002)`,
	} {
		if _, err := db.Exec(q); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = New(path)
	if err != nil {
		t.Fatalf("boot over a database holding tool_events failed: %v", err)
	}
	defer st.Close()
	if _, exists, err := st.liveObjectSQL("table", "tool_events"); err != nil || exists {
		t.Fatalf("the retired table survived: %v %v", exists, err)
	}
	var content, actor string
	if err := st.db.QueryRow(`SELECT content, json_extract(details,'$.actor') FROM conversations WHERE kind='tool_call' AND turn_id='t3'`).Scan(&content, &actor); err != nil {
		t.Fatalf("the started write was not carried into the log: %v", err)
	}
	if !strings.Contains(content, "write([18 chars, sha256=11])") || actor != "ws_sub" {
		t.Fatalf("the carried call = %q by %q, want its tool, its args record and its actor", content, actor)
	}
	names, err := st.AbandonUnfinishedToolCalls()
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"edit", "write"}) {
		t.Fatalf("the boot named %v, want the open edit and the carried write — the retirement erased a warning", names)
	}
	var calls int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM conversations WHERE kind='tool_call'`).Scan(&calls); err != nil || calls != 2 {
		t.Fatalf("%d calls in the log (%v), want 2: the completed read is not carried and the edit not doubled", calls, err)
	}
}

// Typed status is a column, not a prefix to grep for.
func TestToolResultTypedStatus(t *testing.T) {
	s := testStore(t)
	if err := s.RecordToolStart("turn_f", 1, "main", "m", "cf", "shell", `{}`); err != nil {
		t.Fatal(err)
	}
	if err := recordToolDone(s, "turn_f", 1, "shell", `{}`, "Error: it broke", interaction.Failed, false); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordToolStart("turn_f", 2, "main", "m", "ct", "read", `{}`); err != nil {
		t.Fatal(err)
	}
	if err := recordToolDone(s, "turn_f", 2, "read", `{}`, "big output", interaction.Succeeded, true); err != nil {
		t.Fatal(err)
	}
	done, failed, truncated, err := s.ToolResultStats(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if done != 2 || failed != 1 || truncated != 1 {
		t.Fatalf("stats = (%d,%d,%d), want (2,1,1)", done, failed, truncated)
	}
}

func TestTranscriptArgsCapRestored(t *testing.T) {
	s := testStore(t)
	long := strings.Repeat("A", 500)
	if err := recordToolCall(t, s, "shell", long, "ok"); err != nil {
		t.Fatal(err)
	}
	var content string
	if err := s.db.QueryRow(`SELECT content FROM conversations ORDER BY turn_seq DESC LIMIT 1`).Scan(&content); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(content, long) {
		t.Fatal("full 500-rune args landed in the operator transcript — the 200 cap is gone again")
	}
	if !strings.Contains(content, strings.Repeat("A", 197)+"...") {
		t.Fatalf("cap shape is not 197+...: %q", content[:120])
	}
}

// mustStanding derives a standing for a test and FAILS on error — the
// derivation is fail-closed now, and a test must never discard that.
func mustStanding(t *testing.T, src interface {
	StandingFor(id string) (string, error)
}, id string) string {
	t.Helper()
	standing, err := src.StandingFor(id)
	if err != nil {
		t.Fatalf("standing of %s: %v", id, err)
	}
	return standing
}

func TestPromptIdentityCarriesOnlyActiveIntentions(t *testing.T) {
	s := testStore(t)
	kp := testKeyPair(t)
	l := testLedger(t, s)
	defer s.Close()
	defer l.Close()
	mat := func(et ledger.EventType, payload map[string]any) {
		t.Helper()
		evt, err := l.Append(et, 3, payload, kp)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Materialize(evt); err != nil {
			t.Fatal(err)
		}
	}
	mat(ledger.EventIntentionCreate, map[string]any{"id": "i_gate", "statement": "Land the deliver gate"})
	mat(ledger.EventIntentionCreate, map[string]any{"id": "i_other", "statement": "Read Ring 5 v2"})
	pi, err := s.PromptIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if len(pi.Priorities) != 2 {
		t.Fatalf("priorities = %v, want both active intentions", pi.Priorities)
	}
	mat(ledger.EventIntentionStateChange, map[string]any{"id": "i_gate", "state": "completed", "outcome": "served: it landed"})
	pi, err = s.PromptIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if len(pi.Priorities) != 1 || pi.Priorities[0] != "Read Ring 5 v2" {
		t.Fatalf("a closed intention is still a priority: %v", pi.Priorities)
	}
}

// recordToolDone completes the open call at (turn, ordinal) the way the
// runtime does, through RecordToolDoneMeasured, with the outcome the test
// means and no measurement: the one place this package's tests write a
// completion.
func recordToolDone(s *Store, turn string, ordinal int, tool, args, result string, outcome interaction.Outcome, truncated bool) error {
	return s.RecordToolDoneMeasured(turn, ordinal, tool, args, result, outcome, truncated, ToolMeasurement{})
}

// recordToolCall records one call through the writers the runtime uses,
// start then done; the result's excerpt is the newest conversation row.
func recordToolCall(t *testing.T, s *Store, tool, args, result string) error {
	t.Helper()
	recordToolCallN++
	turn := fmt.Sprintf("tool-call-%d", recordToolCallN)
	if err := s.RecordToolStart(turn, 1, "main", "m", "call", tool, args); err != nil {
		return err
	}
	return recordToolDone(s, turn, 1, tool, args, result, interaction.Succeeded, false)
}

var recordToolCallN int
