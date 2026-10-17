package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// claimFor claims a peer row for one walk, or fails the test.
func claimFor(t *testing.T, s *Store, id string) Dispatch {
	t.Helper()
	d, err := s.ClaimDispatch(id)
	if err != nil {
		t.Fatalf("claim %s: %v", id, err)
	}
	return d
}

// flight reads what decides a row's flight.
func flight(t *testing.T, s *Store, id string) (dispatched, last int64) {
	t.Helper()
	if err := s.DB().QueryRow(`SELECT COALESCE(dispatched_ms, 0), last_attempt_ms FROM outbox WHERE id = ?`, id).Scan(&dispatched, &last); err != nil {
		t.Fatal(err)
	}
	return dispatched, last
}

func dispatchStore(t *testing.T, ids ...string) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	for _, id := range ids {
		if err := s.AddOutboxMessage(id, "peer", "friend", "hello from "+id, nil); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// ONE CLAIM, AND ONLY IT WRITES THE OUTCOME (C2). A row in flight is not
// claimed again and is not the outbox's to carry; an outcome for any
// other claim changes nothing; the claim's own outcome ends the flight,
// and after that the same claim is spent.
func TestADispatchClaimIsTheOnlyKeyToItsOutcome(t *testing.T) {
	s := dispatchStore(t, "m")
	d := claimFor(t, s, "m")
	if _, err := s.ClaimDispatch("m"); !errors.Is(err, ErrClaimRefused) {
		t.Fatalf("a row in flight was claimed again: %v", err)
	}
	if pending, err := s.PendingPeerDeliveries(); err != nil || len(pending) != 0 {
		t.Fatalf("a row in flight is still pending: %d %v", len(pending), err)
	}
	stale := Dispatch{MessageID: "m", At: d.At - 1}
	if err := s.MarkDispatchDelivered(stale, "org.example.other"); !errors.Is(err, ErrClaimSuperseded) {
		t.Fatalf("another claim's outcome was written: %v", err)
	}
	if err := s.MarkDispatchDelivered(d, "org.example.telegram"); err != nil {
		t.Fatal(err)
	}
	if dispatched, last := flight(t, s, "m"); last < dispatched {
		t.Fatalf("the outcome left the row in flight: dispatched %d, last attempt %d", dispatched, last)
	}
	if _, _, err := s.RecordDeliveryAttempt(d, "late refusal", false, 8); !errors.Is(err, ErrClaimSuperseded) {
		t.Fatalf("a spent claim wrote a second outcome: %v", err)
	}
	all, _ := s.OutboxOutcomesSince(0)
	if len(all) != 1 || all[0].Delivered != 1 || all[0].Attempts != 1 || all[0].DeliveredVia != "org.example.telegram" || all[0].Effect != "performed" {
		t.Fatalf("one delivery, one attempt, by the claim's carrier: %+v", all)
	}
	if _, err := s.ClaimDispatch("m"); !errors.Is(err, ErrClaimRefused) {
		t.Fatalf("a delivered row was claimed: %v", err)
	}
}

// A LATE RESULT CANNOT OVERWRITE A NEWER ATTEMPT OR A SETTLED ROW. A walk
// that no adapter took is recorded and the next walk claims the row
// again; the first claim's result, arriving now, belongs to nothing.
// Then the boot's settlement parks the second claim in flight, and its
// own late result changes nothing either.
func TestALateResultForAnOlderClaimChangesNothing(t *testing.T) {
	s := dispatchStore(t, "m")
	first := claimFor(t, s, "m")
	if _, _, err := s.RecordDeliveryAttempt(first, "matrix(the service accepted nothing)", false, 8); err != nil {
		t.Fatal(err)
	}
	second := claimFor(t, s, "m")
	if second.At <= first.At {
		t.Fatalf("the newer claim is not later: %d after %d", second.At, first.At)
	}
	if err := s.MarkEffectUnknown(first, "org.example.matrix", "late"); !errors.Is(err, ErrClaimSuperseded) {
		t.Fatalf("a late result overwrote the newer attempt: %v", err)
	}
	if n, err := s.SettleInterruptedDispatches("the process stopped while this message was being sent"); err != nil || n != 1 {
		t.Fatalf("settled %d, %v; want the one row in flight", n, err)
	}
	if err := s.MarkDispatchDelivered(second, "org.example.matrix"); !errors.Is(err, ErrClaimSuperseded) {
		t.Fatalf("a late result overwrote the settlement: %v", err)
	}
	all, _ := s.UndeliveredMessages()
	if len(all) != 1 || all[0].Effect != "unknown" || !all[0].Parked || all[0].Attempts != 2 || all[0].DeliveredVia != "" ||
		!strings.Contains(all[0].LastError, "process stopped") {
		t.Fatalf("the settlement is what the row says: %+v", all)
	}
}

// IN FLIGHT BY CONSTRUCTION, NOT BY THE CLOCK. The last attempt is dated
// an hour ahead (the clock stepped back since): the claim still lands
// after it, and its outcome still ends the flight.
func TestAClaimOrdersAfterTheLastAttemptWhateverTheClock(t *testing.T) {
	s := dispatchStore(t, "m")
	ahead := time.Now().Add(time.Hour).UnixMilli()
	if _, err := s.DB().Exec(`UPDATE outbox SET last_attempt_ms = ?, attempts = 1 WHERE id = 'm'`, ahead); err != nil {
		t.Fatal(err)
	}
	d := claimFor(t, s, "m")
	if d.At != ahead+1 {
		t.Fatalf("claim at %d, want one past the last attempt %d", d.At, ahead)
	}
	if _, _, err := s.RecordDeliveryAttempt(d, "refused", false, 8); err != nil {
		t.Fatal(err)
	}
	if dispatched, last := flight(t, s, "m"); last < dispatched {
		t.Fatalf("an outcome under a clock behind the claim left it in flight: %d < %d", last, dispatched)
	}
	if pending, _ := s.PendingPeerDeliveries(); len(pending) != 1 {
		t.Fatal("a refused walk's row is pending again")
	}
}

// THE BOOT'S SETTLEMENT TOUCHES ONLY WHAT WAS IN FLIGHT, and makes each
// one an attempt the identity is shown: a row never claimed and a row
// already answered are left as they were.
func TestSettlementParksOnlyRowsInFlight(t *testing.T) {
	s := dispatchStore(t, "never", "answered", "stopped")
	answered := claimFor(t, s, "answered")
	if _, _, err := s.RecordDeliveryAttempt(answered, "refused", false, 8); err != nil {
		t.Fatal(err)
	}
	claimFor(t, s, "stopped")
	n, err := s.SettleInterruptedDispatches("the process stopped while this message was being sent")
	if err != nil || n != 1 {
		t.Fatalf("settled %d, %v; want 1", n, err)
	}
	byID := map[string]OutboxMessage{}
	all, _ := s.UndeliveredMessages()
	for _, m := range all {
		byID[m.ID] = m
	}
	if m := byID["never"]; m.Attempts != 0 || m.Parked || m.Effect != "" {
		t.Fatalf("a row never claimed was settled: %+v", m)
	}
	if m := byID["answered"]; m.Attempts != 1 || m.Parked || m.Effect != "" {
		t.Fatalf("an answered row was settled: %+v", m)
	}
	if m := byID["stopped"]; m.Attempts != 1 || !m.Parked || m.Effect != "unknown" || m.LastAttemptMs == 0 {
		t.Fatalf("the row in flight is not parked as unknown and shown: %+v", m)
	}
	if n, err := s.SettleInterruptedDispatches("again"); err != nil || n != 0 {
		t.Fatalf("a second settlement found %d rows (%v)", n, err)
	}
}

// A RESTORE'S SETTLEMENT PARKS WHAT WAITS FOR PEOPLE, ONCE (C2). Every
// peer row undelivered and unparked — never tried, refused and still
// queued, or in flight — becomes an attempt with the answer, parked,
// effect unknown, and no longer in flight; a delivered row, a row already
// parked and the operator's row are left as they were. The same restore
// again changes nothing, and neither does a row queued since; a later
// restore settles what waits then.
func TestARestoreSettlementParksWhatWaitsForPeopleOnce(t *testing.T) {
	s := dispatchStore(t, "never", "refused", "flying", "parked", "delivered")
	refused := claimFor(t, s, "refused")
	if _, _, err := s.RecordDeliveryAttempt(refused, "matrix(nothing accepted)", false, 8); err != nil {
		t.Fatal(err)
	}
	parked := claimFor(t, s, "parked")
	if _, _, err := s.RecordDeliveryAttempt(parked, "grant refused", true, 8); err != nil {
		t.Fatal(err)
	}
	delivered := claimFor(t, s, "delivered")
	if err := s.MarkDispatchDelivered(delivered, "org.example.matrix"); err != nil {
		t.Fatal(err)
	}
	flying := claimFor(t, s, "flying")
	if err := s.AddOutboxMessage("note", "operator", "", "for the operator", nil); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.LastRestore(); ok || err != nil {
		t.Fatalf("a database never restored reads a restore: %v %v", ok, err)
	}

	r := Restore{At: time.Now().UTC(), Source: "snapshot", SourceAt: time.Now().Add(-time.Hour).UTC().Truncate(time.Second)}
	n, err := s.SettleRestore(r, "restored from a snapshot")
	if err != nil || n != 3 {
		t.Fatalf("settled %d, %v; want the three rows that wait", n, err)
	}
	byID := map[string]OutboxMessage{}
	all, _ := s.UndeliveredMessages()
	for _, m := range all {
		byID[m.ID] = m
	}
	for id, attempts := range map[string]int{"never": 1, "refused": 2, "flying": 1} {
		m := byID[id]
		if m.Attempts != attempts || !m.Parked || m.Effect != "unknown" || m.DeliveredVia != "" || m.LastError != "restored from a snapshot" || m.LastAttemptMs == 0 {
			t.Errorf("%s is not parked as possibly sent and shown: %+v", id, m)
		}
	}
	if dispatched, last := flight(t, s, "flying"); last < dispatched || dispatched != flying.At {
		t.Errorf("the row in flight is still in flight after the settlement: %d > %d", dispatched, last)
	}
	if m := byID["parked"]; m.Attempts != 1 || m.Effect != "" || m.LastError != "grant refused" {
		t.Errorf("a row already parked was settled: %+v", m)
	}
	if m := byID["note"]; m.Attempts != 0 || m.Parked || m.Effect != "" {
		t.Errorf("the operator's row was settled: %+v", m)
	}
	if _, ok := byID["delivered"]; ok {
		t.Error("a delivered row came back undelivered")
	}
	got, ok, err := s.LastRestore()
	if err != nil || !ok || !got.At.Equal(r.At) || got.Source != "snapshot" || !got.SourceAt.Equal(r.SourceAt) {
		t.Fatalf("the restore is not recorded as it was: %+v %v %v", got, ok, err)
	}

	if err := s.AddOutboxMessage("since", "peer", "friend", "written after the restore", nil); err != nil {
		t.Fatal(err)
	}
	if n, err := s.SettleRestore(r, "again"); err != nil || n != 0 {
		t.Fatalf("the same restore settled again: %d %v", n, err)
	}
	if pending, _ := s.PendingPeerDeliveries(); len(pending) != 1 || pending[0].ID != "since" {
		t.Fatalf("a row written after the restore was touched by its settlement: %+v", pending)
	}
	later := Restore{At: r.At.Add(time.Minute), Source: "set-aside"}
	if n, err := s.SettleRestore(later, "restored from a set kept aside"); err != nil || n != 1 {
		t.Fatalf("a later restore settled %d, %v; want the one row that waits", n, err)
	}
	if got, _, _ := s.LastRestore(); !got.At.Equal(later.At) || !got.SourceAt.IsZero() {
		t.Errorf("the later restore is not the one recorded: %+v", got)
	}
}

const olderOutbox = `CREATE TABLE IF NOT EXISTS outbox (
    -- provenance: ephemeral
    interaction_id TEXT REFERENCES conversations(id),
    id           TEXT PRIMARY KEY,
    to_role      TEXT NOT NULL CHECK (to_role IN ('operator', 'peer')),
    to_identity  TEXT,
    requested_channel TEXT NOT NULL DEFAULT '',
    recipient_error TEXT NOT NULL DEFAULT '',
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
) STRICT;`

// olderOutboxReconcile runs the older binary's schema step: this file
// with the outbox declared the older way, in one transaction a refusal
// rolls back.
func olderOutboxReconcile(t *testing.T, s *Store) error {
	t.Helper()
	raw, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	const head, tail = "CREATE TABLE IF NOT EXISTS outbox (", ") STRICT;"
	start := strings.Index(text, head)
	end := strings.Index(text[max(start, 0):], tail)
	if start < 0 || end < 0 {
		t.Fatal("schema.sql no longer declares outbox")
	}
	older := text[:start] + olderOutbox + text[start+end+len(tail):]
	return s.withSchema(t.Context(), older, func() error {
		_, err := s.reconcileSchema(older)
		return err
	})
}

// GOING BACK PAST THE CLAIM. (a) No
// send claimed since the upgrade: the column is all NULL and the older
// engine drops it without loss. (b) Once a send is claimed it refuses,
// naming dispatched_ms. (c) The documented route — park what is in
// flight, then drop the column — lets it start, and the message this
// version left in flight is parked, not pending, so the older binary
// never sends it again.
func TestOutboxRollbackPastTheDispatchClaim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aii.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	for _, id := range []string{"sent", "in_flight", "waiting"} {
		if err := s.AddOutboxMessage(id, "peer", "friend", id, nil); err != nil {
			t.Fatal(err)
		}
	}
	// (a)
	if err := olderOutboxReconcile(t, s); err != nil {
		t.Fatalf("(a) the older engine refused an outbox with no claim: %v", err)
	}
	if cols, err := s.liveColumns("outbox"); err != nil || cols["dispatched_ms"] {
		t.Fatalf("(a) the older shape was not restored: %v %v", cols, err)
	}
	// (b) upgrade again, deliver one message and leave one in flight.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err = New(path); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDispatchDelivered(claimFor(t, s, "sent"), "org.example.telegram"); err != nil {
		t.Fatal(err)
	}
	claimFor(t, s, "in_flight")
	err = olderOutboxReconcile(t, s)
	var shape *ShapeError
	if !errors.As(err, &shape) || !strings.Contains(err.Error(), "refusing to rebuild outbox: column(s) dispatched_ms hold data and are not declared") {
		t.Fatalf("(b) the older engine did not refuse naming the column: %v", err)
	}
	// (c) the route KNOWN_LIMITATIONS gives, statement for statement.
	for _, q := range []string{
		`UPDATE outbox SET parked = 1, effect = 'unknown', attempts = attempts + 1, last_attempt_ms = dispatched_ms WHERE delivered = 0 AND dispatched_ms > last_attempt_ms;`,
		`ALTER TABLE outbox DROP COLUMN dispatched_ms;`,
	} {
		if _, err := s.DB().Exec(q); err != nil {
			t.Fatalf("(c) the documented route failed at %q: %v", q, err)
		}
	}
	if err := olderOutboxReconcile(t, s); err != nil {
		t.Fatalf("(c) with the column dropped the older engine still refused: %v", err)
	}
	// What the older binary would carry: its own pending query.
	rows, err := s.DB().Query(`SELECT id FROM outbox WHERE delivered = 0 AND parked = 0 AND to_role = 'peer'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var pending []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		pending = append(pending, id)
	}
	if len(pending) != 1 || pending[0] != "waiting" {
		t.Fatalf("(c) the older binary would carry %v; want only the message never sent", pending)
	}
}

// A send in flight is not "queued, not yet tried": the turn is told what
// became of it once its outcome is written, not before.
func TestASendInFlightIsNotObservedAsWaiting(t *testing.T) {
	s := dispatchStore(t, "flying")
	d := claimFor(t, s, "flying")
	if rows, err := s.PendingTurnEvents(t.Context()); err != nil || len(rows) != 0 {
		t.Fatalf("a send in flight was observed before its outcome: %d rows, %v", len(rows), err)
	}
	if _, _, err := s.RecordDeliveryAttempt(d, "fixture refusal", false, 8); err != nil {
		t.Fatal(err)
	}
	rows, err := s.PendingTurnEvents(t.Context())
	if err != nil || len(rows) != 1 || rows[0].Outcome == nil || rows[0].Outcome.Attempts != 1 {
		t.Fatalf("the settled attempt should be one observation at attempt 1: %+v %v", rows, err)
	}
}
