package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

var initializedStoreTemplate struct {
	once sync.Once
	raw  []byte
	err  error
}

// .
// .
// .
func initializedStoreBytes() ([]byte, error) {
	initializedStoreTemplate.once.Do(func() {
		dir, err := os.MkdirTemp("", "aii-store-template-*")
		if err != nil {
			initializedStoreTemplate.err = err
			return
		}
		defer os.RemoveAll(dir)

		path := filepath.Join(dir, "empty.db")
		s, err := New(path)
		if err != nil {
			initializedStoreTemplate.err = err
			return
		}
		if _, err := s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			_ = s.Close()
			initializedStoreTemplate.err = fmt.Errorf("checkpoint initialized store template: %w", err)
			return
		}
		if err := s.Close(); err != nil {
			initializedStoreTemplate.err = fmt.Errorf("close initialized store template: %w", err)
			return
		}
		initializedStoreTemplate.raw, initializedStoreTemplate.err = os.ReadFile(path)
	})
	return initializedStoreTemplate.raw, initializedStoreTemplate.err
}

func testStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "aii.db")
	raw, err := initializedStoreBytes()
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
	s.restoreActiveProject()
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

// .
// .
// .
func testLedger(t *testing.T, s *Store) *ledger.Ledger {
	t.Helper()
	dir := t.TempDir()
	l, err := ledger.New(filepath.Join(dir, "ledger.jsonl"))
	if err != nil {
		t.Fatalf("New ledger failed: %v", err)
	}
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

// .
// .
func TestBeliefMaterialize(t *testing.T) {
	s := testStore(t)
	kp := testKeyPair(t)
	l := testLedger(t, s)

	payload := map[string]interface{}{
		"id":         "b_001",
		"statement":  "Testing is essential",
		"ring":       3,
		"confidence": 0.9,
	}
	evt, err := l.Append(ledger.EventBeliefUpsert, kp.Fingerprint(), 3, payload, kp)
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

// .
// .
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
	// .
	for i, want := range turns {
		if result[i].Role != want.role {
			t.Errorf("turn %d role = %q, want %q", i, result[i].Role, want.role)
		}
		if result[i].Content != want.content {
			t.Errorf("turn %d content = %q, want %q", i, result[i].Content, want.content)
		}
	}

	// .
	// .
	// .
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

// .
func TestLifetimeTicks(t *testing.T) {
	s := testStore(t)
	kp := testKeyPair(t)
	l := testLedger(t, s)

	// .
	evt, _ := l.Append(ledger.EventRing0Genesis, kp.Fingerprint(), 0,
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

// .
func TestOutbox(t *testing.T) {
	s := testStore(t)
	kp := testKeyPair(t)
	l := testLedger(t, s)

	// .
	evt1, _ := l.Append(ledger.EventExperienceCreate, kp.Fingerprint(), 3,
		map[string]string{"id": "e1", "content": "send event 1", "category": "communication"}, kp)
	s.Materialize(evt1)

	evt2, _ := l.Append(ledger.EventExperienceCreate, kp.Fingerprint(), 3,
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

	s.MarkDelivered("msg_1", "dashboard")
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

// .
func TestAlarms(t *testing.T) {
	s := testStore(t)

	s.SetAlarm("alarm_1", "morning_brief", "wall", 1700000000, nil, "")
	repeat := int64(300000)
	s.SetAlarm("alarm_2", "heartbeat", "wall", 1700000300, &repeat, "")

	// .
	alarms, _ := s.DueAlarms("wall", 1700000000, 10)
	if len(alarms) != 1 {
		t.Fatalf("expected 1 due alarm, got %d", len(alarms))
	}
	if alarms[0].AlarmID != "alarm_1" {
		t.Errorf("alarm ID = %q, want alarm_1", alarms[0].AlarmID)
	}

	// .
	alarms, _ = s.DueAlarms("wall", 1700000300, 10)
	if len(alarms) != 2 {
		t.Errorf("expected 2 due alarms, got %d", len(alarms))
	}

	// .
	s.DeleteAlarm("alarm_1")
	alarms, _ = s.DueAlarms("wall", 1700000000, 10)
	if len(alarms) != 0 {
		t.Errorf("expected 0 after delete, got %d", len(alarms))
	}
}

// .
func TestStats(t *testing.T) {
	s := testStore(t)
	kp := testKeyPair(t)
	l := testLedger(t, s)

	evt1, _ := l.Append(ledger.EventRing0Genesis, kp.Fingerprint(), 0,
		map[string]string{"name": "test"}, kp)
	s.Materialize(evt1)

	evt2, _ := l.Append(ledger.EventBeliefUpsert, kp.Fingerprint(), 3,
		map[string]interface{}{"id": "b1", "statement": "test belief", "ring": 3, "confidence": 0.5}, kp)
	s.Materialize(evt2)

	evt3, _ := l.Append(ledger.EventExperienceCreate, kp.Fingerprint(), 3,
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

// .
// .
// .
// .
// .
// .
func TestStatsBeliefCountMatchesLiveFilter(t *testing.T) {
	s := testStore(t)
	kp := testKeyPair(t)
	l := testLedger(t, s)

	// .
	evt1, _ := l.Append(ledger.EventBeliefUpsert, kp.Fingerprint(), 3,
		map[string]interface{}{"id": "b1", "statement": "live one", "ring": 3, "confidence": 0.5}, kp)
	s.Materialize(evt1)
	evt2, _ := l.Append(ledger.EventBeliefUpsert, kp.Fingerprint(), 3,
		map[string]interface{}{"id": "b2", "statement": "live two", "ring": 3, "confidence": 0.5}, kp)
	s.Materialize(evt2)

	stats, _ := s.GetStats()
	if stats.BeliefCount != 2 {
		t.Fatalf("baseline belief count = %d, want 2", stats.BeliefCount)
	}

	// .
	// .
	evt3, _ := l.Append(ledger.EventBeliefSupersede, kp.Fingerprint(), 3,
		map[string]interface{}{"old_id": "b1", "new_id": "b2", "reason": "test"}, kp)
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

	// .
	evt4, _ := l.Append(ledger.EventBeliefArchive, kp.Fingerprint(), 3,
		map[string]interface{}{"id": "b2"}, kp)
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

// .
func TestWorkSession(t *testing.T) {
	s := testStore(t)
	kp := testKeyPair(t)
	l := testLedger(t, s)

	// .
	evt, _ := l.Append(ledger.EventRing0Genesis, kp.Fingerprint(), 0,
		map[string]string{"name": "test"}, kp)
	s.Materialize(evt)

	// .
	if err := s.StartWorkSession("ws_1", "Analyzing files"); err != nil {
		t.Fatalf("StartWorkSession failed: %v", err)
	}

	// .
	if err := s.UpdateWorkState("ws_1", "Step 3 of 5 complete"); err != nil {
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

	// .
	if err := s.DeliverWorkSession("ws_1", "Done", "", ""); err != nil {
		t.Fatalf("DeliverWorkSession failed: %v", err)
	}
	ws, _ = s.ActiveWorkSession()
	if ws != nil {
		t.Error("expected no active work session after delivery")
	}
}

// .
// .
// .
func TestToolEventRecordsResult(t *testing.T) {
	s := testStore(t)

	if err := s.RecordToolEvent("bash", "echo hi", "hello\nworld"); err != nil {
		t.Fatal(err)
	}
	turns, err := s.RecentTurnsIncludingSystem(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) == 0 {
		t.Fatal("no transcript entries")
	}
	// .
	// .
	last := turns[len(turns)-1]
	if !strings.Contains(last.Content, "→ bash(echo hi)") {
		t.Fatalf("call missing: %q", last.Content)
	}
	if !strings.Contains(last.Content, "← hello") {
		t.Fatalf("result excerpt missing — transcript record is incomplete: %q", last.Content)
	}

	// .
	big := strings.Repeat("界", TranscriptResultLimit+5000)
	if err := s.RecordToolEvent("bash", "cat big", big); err != nil {
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

// .
// .
func TestSetAlarmOwnerBound(t *testing.T) {
	s := testStore(t)
	defer s.Close()

	if err := s.SetAlarm("a1", "dream", "life", 10, nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAlarm("a1", "consolidate", "life", 20, nil, ""); err == nil {
		t.Fatal("upsert with a different owner must be rejected")
	}
	// .
	if err := s.SetAlarm("a1", "dream", "life", 30, nil, ""); err != nil {
		t.Fatalf("same-owner replace must succeed: %v", err)
	}
	// .
	if err := s.SetAlarm("a1", "dream", "life", 30, nil, ""); err != nil {
		t.Fatalf("idempotent set must succeed: %v", err)
	}
}

// .
// .
// .
func TestListIntentionsReturnsHistory(t *testing.T) {
	s := testStore(t)
	kp := testKeyPair(t)
	l := testLedger(t, s)
	defer s.Close()
	defer l.Close()

	mat := func(et ledger.EventType, payload map[string]interface{}) {
		t.Helper()
		evt, err := l.Append(et, kp.Fingerprint(), 3, payload, kp)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Materialize(evt); err != nil {
			t.Fatal(err)
		}
	}
	mat(ledger.EventIntentionCreate, map[string]interface{}{"id": "i1", "statement": "first"})
	mat(ledger.EventIntentionStateChange, map[string]interface{}{"id": "i1", "state": "completed"})
	mat(ledger.EventIntentionCreate, map[string]interface{}{"id": "i2", "statement": "second"})
	mat(ledger.EventIntentionCreate, map[string]interface{}{"id": "i3", "statement": "third"})
	mat(ledger.EventIntentionStateChange, map[string]interface{}{"id": "i3", "state": "abandoned"})

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

// .
// .
// .
func TestListRawExperiences(t *testing.T) {
	s := testStore(t)
	seq := 0
	mat := func(id, content string) {
		seq++
		evt := ledger.Event{Seq: uint64(seq), Type: ledger.EventExperienceCreate,
			Ring: 3, Timestamp: "2026-08-17T10:00:00Z"}
		payload := map[string]interface{}{"id": id, "content": content, "category": "observation"}
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

// .
// .
// .
// .
// .
func TestStandingDerivation(t *testing.T) {
	s := testStore(t)
	seq := 0
	mat := func(et ledger.EventType, ring int, payload map[string]interface{}) {
		seq++
		evt := ledger.Event{Seq: uint64(seq), Type: et, Ring: ring, Timestamp: "2026-08-17T10:00:00Z"}
		b, _ := json.Marshal(payload)
		evt.Payload = b
		if err := s.Materialize(&evt); err != nil {
			t.Fatalf("materialize %s: %v", et, err)
		}
	}

	mat(ledger.EventRing0Genesis, 0, map[string]interface{}{"name": "S"})
	mat(ledger.EventBeliefUpsert, 3, map[string]interface{}{"id": "b", "statement": "stand", "ring": 3, "confidence": 0.5})
	if got := mustStanding(t, s, "b"); got != "new" {
		t.Fatalf("edgeless belief: %q, want new", got)
	}

	// .
	// .
	// .
	if err := s.AddConversationTurn("operator", "I saw it hold"); err != nil {
		t.Fatal(err)
	}
	var opTurn uint64
	s.db.QueryRow(`SELECT MAX(turn_seq) FROM conversations`).Scan(&opTurn)
	for _, x := range []struct{ id, prov string }{
		{"r1", "self"}, {"r2", "self"}, {"o1", "operator"},
	} {
		payload := map[string]interface{}{"id": x.id, "content": "obs", "category": "observation", "provenance": x.prov}
		if x.prov == "operator" {
			payload["source_turn"] = opTurn
		}
		mat(ledger.EventExperienceCreate, 3, payload)
		mat(ledger.EventEdgeCreate, 3, map[string]interface{}{"id": "e_" + x.id, "from_id": x.id, "to_id": "b", "edge_type": "SUPPORTS"})
	}
	if got := mustStanding(t, s, "b"); got != "confirmed" {
		t.Fatalf("3 sources spanning 2 classes: %q, want confirmed", got)
	}

	// .
	mat(ledger.EventBeliefUpsert, 3, map[string]interface{}{"id": "b_self", "statement": "echo", "ring": 3, "confidence": 0.5})
	for _, x := range []string{"s1", "s2", "s3"} {
		mat(ledger.EventExperienceCreate, 3, map[string]interface{}{"id": x, "content": "obs", "category": "observation"})
		mat(ledger.EventEdgeCreate, 3, map[string]interface{}{"id": "e_" + x, "from_id": x, "to_id": "b_self", "edge_type": "SUPPORTS"})
	}
	if got := mustStanding(t, s, "b_self"); got != "new" {
		t.Fatalf("one-class evidence: %q, want new", got)
	}

	// .
	mat(ledger.EventExperienceCreate, 3, map[string]interface{}{"id": "c1", "content": "counter", "category": "observation"})
	mat(ledger.EventEdgeCreate, 3, map[string]interface{}{"id": "e_c1", "from_id": "c1", "to_id": "b", "edge_type": "CONTRADICTS"})
	if got := mustStanding(t, s, "b"); got != "suspect" {
		t.Fatalf("contradicted belief: %q, want suspect", got)
	}

	// .
	mat(ledger.EventEdgeArchive, 3, map[string]interface{}{"id": "e_c1"})
	if got := mustStanding(t, s, "b"); got != "confirmed" {
		t.Fatalf("archived contradiction: %q, want confirmed", got)
	}

	// .
	// .
	// .
	// .
	setTicks := func(n int64) {
		s.mu.Lock()
		_, err := s.db.Exec(`UPDATE identity_lifetime SET lifetime_ticks = ? WHERE singleton_id = 'current'`, n)
		s.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	setTicks(10)
	mat(ledger.EventConsolidationRun, 3, map[string]interface{}{"inputs": []string{"r1"}, "outputs": []uint64{}, "confirmed": []map[string]interface{}{{"id": "b", "ticks": 10}}})
	if got := mustStanding(t, s, "b"); got != "confirmed" {
		t.Fatalf("freshly anchored: %q, want confirmed (time not yet elapsed)", got)
	}
	setTicks(61)
	if got := mustStanding(t, s, "b"); got != "trusted" {
		t.Fatalf("61 ticks past a tick-10 anchor: %q, want trusted", got)
	}
}

// .
// .
// .
func TestNoGrandfather(t *testing.T) {
	s := testStore(t)
	evt := ledger.Event{Seq: 1, Type: ledger.EventRelationshipUpsert, Ring: 1,
		Timestamp: "2026-08-17T09:00:00Z"}
	b, _ := json.Marshal(map[string]interface{}{
		"id": "rel-old", "counterpart_name": "Op", "counterpart_role": "operator",
		"relationship_type": "founding_operator",
		// .
		"operator_approval_excerpt": "Operator Op approved this relationship.",
	})
	evt.Payload = b
	if err := s.Materialize(&evt); err == nil {
		t.Fatal("basis-less operator relationship must fail closed — no grandfather in an unreleased system")
	}
	if err := s.MaterializeReplay(&evt); err == nil {
		t.Fatal("same in replay mode — invariants are mode-independent")
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestReplayPureFreshDBRebuild(t *testing.T) {
	// .
	// .
	build := func(s *Store) []ledger.Event {
		var events []ledger.Event
		mat := func(et ledger.EventType, ring int, payload map[string]interface{}) {
			evt := ledger.Event{Seq: uint64(len(events) + 1), Type: et, Ring: ring,
				Timestamp: "2026-08-17T09:00:00Z"}
			payloadBytes, _ := json.Marshal(payload)
			evt.Payload = payloadBytes
			if err := s.Materialize(&evt); err != nil {
				t.Fatalf("live materialize %s: %v", et, err)
			}
			events = append(events, evt)
		}
		mat(ledger.EventRing0Genesis, 0, map[string]interface{}{"name": "ReplayIdentity"})
		mat(ledger.EventRelationshipUpsert, 1, map[string]interface{}{
			"id": "rel-founding", "counterpart_name": "Op", "counterpart_role": "operator",
			"relationship_type": "founding_operator", "charter_text": "founding",
			"operator_approval_excerpt": "Yes — rel-founding.",
			"operator_approval_turn":    float64(7),
			"approval_basis":            "conversation_turn",
		})
		mat(ledger.EventRelationshipUpsert, 1, map[string]interface{}{
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
	// .
	// .
	// .
	for i := 0; i < 7; i++ {
		if err := origin.AddConversationTurn("operator", fmt.Sprintf("turn %d", i+1)); err != nil {
			t.Fatal(err)
		}
	}
	events := build(origin)
	origin.Close()

	// .
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

	// .
	// .
	// .
	bad := ledger.Event{Seq: 1, Type: ledger.EventRelationshipUpsert, Ring: 1,
		Timestamp: "2026-08-17T09:00:00Z"}
	b, _ := json.Marshal(map[string]interface{}{
		"id": "rel-bad", "counterpart_name": "Op", "counterpart_role": "operator",
		"operator_approval_excerpt": "x", "approval_basis": "conversation_turn",
	})
	bad.Payload = b
	if err := fresh.MaterializeReplay(&bad); err == nil {
		t.Fatal("conversation_turn basis without a turn must fail closed in replay")
	}
}

// .
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

// .
// .
// .
// .
// .
// .
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

// .
// .
// .
// .
func TestToolEventLifecycleRecord(t *testing.T) {
	s := testStore(t)
	if err := s.RecordToolStart("turn_1", 1, "main", "fake-model", "c1", "read", `{"file_path":"/a"}`); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordToolDone("turn_1", 1, "read", `{"file_path":"/a"}`, "full contents here", false, false); err != nil {
		t.Fatal(err)
	}
	// .
	// .
	if err := s.RecordToolStart("turn_2", 1, "ws_sub", "fake-model", "c2", "write", `{"file_path":"/b"}`); err != nil {
		t.Fatal(err)
	}

	var state, actor, model string
	if err := s.db.QueryRow(`SELECT state, actor, model FROM tool_events WHERE turn_id='turn_1' AND ordinal=1`).
		Scan(&state, &actor, &model); err != nil {
		t.Fatal(err)
	}
	if state != "done" || actor != "main" || model != "fake-model" {
		t.Fatalf("row = (%s,%s,%s), want (done,main,fake-model)", state, actor, model)
	}

	names, err := s.AbandonUnfinishedToolCalls()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "write" {
		t.Fatalf("abandoned = %v, want exactly the interrupted write", names)
	}
	if again, _ := s.AbandonUnfinishedToolCalls(); len(again) != 0 {
		t.Fatalf("second pass found %v — the stamp did not stick", again)
	}

	done, failed, truncated, err := s.ToolEventStats(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if done != 1 || failed != 0 || truncated != 0 {
		t.Fatalf("stats = (%d,%d,%d), want (1,0,0)", done, failed, truncated)
	}
}

// .
// .
// .
// .
func TestToolEventCustodyPolicy(t *testing.T) {
	s := testStore(t)
	secretArgs := `{"command":"curl -H \'Authorization: Bearer sk-SECRETTOKEN-` + strings.Repeat("a", 300) + `\'"}`
	secretResult := "BEGIN PRIVATE KEY " + strings.Repeat("s3cr3t-", 700)
	if err := s.RecordToolStart("turn_b", 1, "main", "m", "c", "shell", secretArgs); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordToolDone("turn_b", 1, "shell", secretArgs, secretResult, false, false); err != nil {
		t.Fatal(err)
	}
	var argsRec, resRec string
	if err := s.db.QueryRow(`SELECT args_record, result_record FROM tool_events WHERE turn_id='turn_b'`).
		Scan(&argsRec, &resRec); err != nil {
		t.Fatal(err)
	}
	// .
	// .
	// .
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
	if n, err := s.PruneToolEvents(); err != nil || n != 0 {
		t.Fatalf("prune of fresh rows = (%d,%v), want (0,nil)", n, err)
	}
}

// .
// .
func TestRecordToolDoneRequiresExactlyOneStartedRow(t *testing.T) {
	s := testStore(t)
	// .
	var before int
	s.db.QueryRow(`SELECT COUNT(*) FROM conversations`).Scan(&before)
	if err := s.RecordToolDone("turn_x", 1, "read", `{}`, "ghost", false, false); err == nil {
		t.Fatal("done with no started row was accepted — a completion was invented")
	}
	var after int
	s.db.QueryRow(`SELECT COUNT(*) FROM conversations`).Scan(&after)
	if after != before {
		t.Fatalf("refused completion still wrote an excerpt (%d -> %d)", before, after)
	}
	// .
	if err := s.RecordToolStart("turn_x", 2, "main", "m", "c", "read", `{}`); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordToolDone("turn_x", 2, "read", `{}`, "real", false, false); err != nil {
		t.Fatal(err)
	}
	s.db.QueryRow(`SELECT COUNT(*) FROM conversations`).Scan(&after)
	if after != before+1 {
		t.Fatalf("successful completion wrote %d excerpt row(s), want 1", after-before)
	}
	if err := s.RecordToolDone("turn_x", 2, "read", `{}`, "again", false, false); err == nil {
		t.Fatal("a second completion of the same execution was accepted")
	}
}

// .
// .
// .
// .
// .
func TestLegacyToolEventsShapeIsReplacedAtBoot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aii.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	const legacyDDL = `CREATE TABLE tool_events (
		ts_ms INTEGER NOT NULL, call_id TEXT NOT NULL, tool TEXT NOT NULL,
		args TEXT NOT NULL, phase TEXT NOT NULL, failed INTEGER NOT NULL DEFAULT 0,
		truncated INTEGER NOT NULL DEFAULT 0, result TEXT NOT NULL DEFAULT '')`
	if _, err := db.Exec(legacyDDL); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tool_events VALUES
		(1000,'c1','read','{}','started',0,0,''),
		(1001,'c1','read','{}','done',0,0,'contents'),
		(1002,'c2','write','{"file_path":"/x"}','started',0,0,'')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	st, err := New(path)
	if err != nil {
		t.Fatalf("boot over a 4eafaa5-shaped database failed: %v", err)
	}
	defer st.Close()
	legacy, err := st.hasLegacyToolEvents()
	if err != nil {
		t.Fatal(err)
	}
	if legacy {
		t.Fatal("legacy shape survived boot")
	}
	// .
	// .
	// .
	// .
	// .
	names, err := st.AbandonUnfinishedToolCalls()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "write" {
		t.Fatalf("abandonment after upgrade named %v, want exactly the crashed write — the replacement erased the warning", names)
	}
	var n int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM tool_events WHERE turn_id != 'legacy_v1'`).Scan(&n); err != nil {
		t.Fatalf("v2 table unusable after upgrade: %v", err)
	}
	if n != 0 {
		t.Fatalf("completed legacy rows leaked into the v2 shape: %d", n)
	}
	// .
	if err := st.RecordToolStart("t", 1, "main", "m", "c", "read", `{}`); err != nil {
		t.Fatalf("v2 write on upgraded database: %v", err)
	}
}

// .
func TestToolEventTypedStatus(t *testing.T) {
	s := testStore(t)
	if err := s.RecordToolStart("turn_f", 1, "main", "m", "cf", "shell", `{}`); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordToolDone("turn_f", 1, "shell", `{}`, "Error: it broke", true, false); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordToolStart("turn_f", 2, "main", "m", "ct", "read", `{}`); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordToolDone("turn_f", 2, "read", `{}`, "big output", false, true); err != nil {
		t.Fatal(err)
	}
	done, failed, truncated, err := s.ToolEventStats(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if done != 2 || failed != 1 || truncated != 1 {
		t.Fatalf("stats = (%d,%d,%d), want (2,1,1)", done, failed, truncated)
	}
}

// .
// .
func TestTranscriptArgsCapRestored(t *testing.T) {
	s := testStore(t)
	long := strings.Repeat("A", 500)
	if err := s.RecordToolEvent("shell", long, "ok"); err != nil {
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

// .
// .
// .
// .
// .
func TestReplacementDirectiveStaysNarrow(t *testing.T) {
	raw, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	got := declaredReplacements(string(raw))
	if len(got) != 1 || got["tool_events"] != "phase" {
		t.Fatalf("declared replacements = %v — the directive is constrained to the tool_events v1 bridge; expanding it is a decision, not a convenience", got)
	}
}

// .
// .
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

// .
// .
// .
func TestPromptIdentityCarriesOnlyActiveIntentions(t *testing.T) {
	s := testStore(t)
	kp := testKeyPair(t)
	l := testLedger(t, s)
	defer s.Close()
	defer l.Close()
	mat := func(et ledger.EventType, payload map[string]interface{}) {
		t.Helper()
		evt, err := l.Append(et, kp.Fingerprint(), 3, payload, kp)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Materialize(evt); err != nil {
			t.Fatal(err)
		}
	}
	mat(ledger.EventIntentionCreate, map[string]interface{}{"id": "i_gate", "statement": "Land the deliver gate"})
	mat(ledger.EventIntentionCreate, map[string]interface{}{"id": "i_other", "statement": "Read Ring 5 v2"})
	pi, err := s.PromptIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if len(pi.Priorities) != 2 {
		t.Fatalf("priorities = %v, want both active intentions", pi.Priorities)
	}
	mat(ledger.EventIntentionStateChange, map[string]interface{}{"id": "i_gate", "state": "completed", "outcome": "served: it landed"})
	pi, err = s.PromptIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if len(pi.Priorities) != 1 || pi.Priorities[0] != "Read Ring 5 v2" {
		t.Fatalf("a closed intention is still a priority: %v", pi.Priorities)
	}
}
