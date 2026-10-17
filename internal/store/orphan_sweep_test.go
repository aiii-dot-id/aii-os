package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// A runtime that dies mid-work leaves its work_sessions rows 'active'
// forever: the dashboard shows phantom work and the identity believes
// a hand it no longer has. Boot closes them honestly.
func TestOrphanedActiveWorkSessionsAreSwept(t *testing.T) {
	s := testStore(t)
	if err := s.StartWorkSession(t.Context(), "ws_orphan", "long survey"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := s.StartWorkSession(t.Context(), "ws_done", "quick check"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := s.DeliverWorkSession(t.Context(), "ws_done", "42", "", ""); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	swept, err := s.SweepOrphanWorkSessions()
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(swept) != 1 || swept[0].ID != "ws_orphan" || swept[0].Description != "long survey" {
		t.Fatalf("swept %+v, want exactly the orphan", swept)
	}
	// Idempotent: a second boot finds nothing active.
	swept, err = s.SweepOrphanWorkSessions()
	if err != nil || len(swept) != 0 {
		t.Fatalf("second sweep: %+v err=%v", swept, err)
	}
}

// The sweep is a write, and SAFE's frozen store refuses it: the session
// stays as the record found it, and nothing is reported closed.
func TestAFrozenStoreRefusesTheOrphanSweep(t *testing.T) {
	s := testStore(t)
	if err := s.StartWorkSession(t.Context(), "ws_orphan", "long survey"); err != nil {
		t.Fatal(err)
	}
	s.SetFrozen(true, "fixture SAFE")
	var frozen *FrozenError
	if swept, err := s.SweepOrphanWorkSessions(); !errors.As(err, &frozen) || len(swept) != 0 {
		t.Fatalf("frozen sweep = %+v, %v; want a FrozenError and nothing closed", swept, err)
	}
	s.SetFrozen(false, "")
	if ws, err := s.WorkSessionByID("ws_orphan"); err != nil || ws == nil || ws.Status != "active" {
		t.Fatalf("a refused sweep changed the session: %+v %v", ws, err)
	}
}

// Every session the sweep closes reaches the identity exactly one way: a
// sub-agent's as an unharvested delivery, a main session's in the note —
// which says its description and that its effects are unknown.
func TestSweptSessionNoteSaysOnlyWhatNoHarvestWill(t *testing.T) {
	s := testStore(t)
	if err := s.StartWorkSession(t.Context(), "ws_main", "migrate the notes"); err != nil {
		t.Fatal(err)
	}
	queueChild(t, s, "ws_child", "DONE")
	if err := s.StartWorkSession(t.Context(), "ws_child", SubagentDescription("survey the tree")); err != nil {
		t.Fatal(err)
	}
	swept, err := s.SweepOrphanWorkSessions()
	if err != nil || len(swept) != 2 {
		t.Fatalf("sweep = %+v, %v", swept, err)
	}
	note := SweptSessionNote(swept)
	want := `Work session ws_main ("migrate the notes") was active at the last shutdown and has been closed as interrupted — its external effects are unknown; verify before repeating them.`
	if note != want {
		t.Fatalf("note = %q\nwant %q", note, want)
	}
	harvest, err := s.UnharvestedDeliveries(8)
	if err != nil || len(harvest) != 1 || harvest[0].ID != "ws_child" {
		t.Fatalf("the child's close is not harvested: %+v %v", harvest, err)
	}
	if SweptSessionNote(harvest) != "" {
		t.Fatal("a sub-agent's close would be said twice")
	}
}

func TestBootSweepKeepsSessionsWithLiveSubagentQueueWork(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	enqueue := func(session, key, kind string, priority int) {
		t.Helper()
		if _, kept, err := s.EnqueueWorkWithSessionBelowLimit(&WorkItem{
			Kind: kind, Payload: "{}", DedupKey: key, Source: "identity", Priority: priority,
		}, 10, session, SubagentDescription(session)); err != nil || !kept {
			t.Fatalf("enqueue %s: kept=%t err=%v", session, kept, err)
		}
	}
	enqueue("ws_running", "ws_running", SubagentWorkKind, 1)
	if item, err := s.ClaimWork([]string{SubagentWorkKind}, time.Now().UnixMilli()); err != nil || item == nil || item.DedupKey != "ws_running" {
		t.Fatalf("claim running child: %+v %v", item, err)
	}
	enqueue("ws_terminal", "ws_terminal", SubagentWorkKind, 1)
	terminal, err := s.ClaimWork([]string{SubagentWorkKind}, time.Now().UnixMilli())
	if err != nil || terminal == nil || terminal.DedupKey != "ws_terminal" {
		t.Fatalf("claim terminal child: %+v %v", terminal, err)
	}
	if err := s.CompleteWork(terminal.ID); err != nil {
		t.Fatal(err)
	}
	enqueue("ws_queued", "ws_queued", SubagentWorkKind, 1)
	if err := s.StartWorkSession(t.Context(), "ws_cont", SubagentDescription("continued")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnqueueWork(&WorkItem{Kind: SubagentWorkKind, Payload: "{}", DedupKey: "ws_cont#2", Source: "identity"}); err != nil {
		t.Fatal(err)
	}
	if err := s.StartWorkSession(t.Context(), "ws_orphan", SubagentDescription("orphan")); err != nil {
		t.Fatal(err)
	}
	enqueue("ws_unrelated", "ws_unrelated", "other.kind", 5)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	swept, err := s.SweepOrphanWorkSessions()
	if err != nil || len(swept) != 3 {
		t.Fatalf("boot sweep = %+v, %v; want orphan, unrelated kind and terminal row", swept, err)
	}
	for _, id := range []string{"ws_running", "ws_queued", "ws_cont"} {
		ws, err := s.WorkSessionByID(id)
		if err != nil || ws == nil || ws.Status != "active" {
			t.Fatalf("%s lost its live queue hand: %+v %v", id, ws, err)
		}
	}
	for _, id := range []string{"ws_orphan", "ws_unrelated", "ws_terminal"} {
		ws, err := s.WorkSessionByID(id)
		if err != nil || ws == nil || ws.Status != "delivered" {
			t.Fatalf("%s was not swept: %+v %v", id, ws, err)
		}
	}
	// A child is still alive, but it must not become the main resident's
	// active session or receive their updates and continuation turns.
	if ws, err := s.ActiveWorkSession(); err != nil || ws != nil {
		t.Fatalf("queued child leaked into the main work seat: %+v %v", ws, err)
	}
	// A human can write the same words as the display convention; only a
	// real queue row, not that prefix, identifies a child.
	if err := s.StartWorkSession(t.Context(), "ws_main", SubagentDescription("operator task")); err != nil {
		t.Fatal(err)
	}
	if ws, err := s.ActiveWorkSession(); err != nil || ws == nil || ws.ID != "ws_main" {
		t.Fatalf("main session lost to queued child: %+v %v", ws, err)
	}
	if err := s.DeliverWorkSession(t.Context(), "ws_main", "served: main finished", "", ""); err != nil {
		t.Fatal(err)
	}
	item, err := s.ClaimWork([]string{SubagentWorkKind}, time.Now().UnixMilli())
	if err != nil || item == nil || item.DedupKey != "ws_queued" {
		t.Fatalf("claim queued child after restart: %+v %v", item, err)
	}
	if err := s.DeliverWorkSession(t.Context(), "ws_queued", "served: the child finished after restart", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteWork(item.ID); err != nil {
		t.Fatal(err)
	}
	if swept, err := s.SweepOrphanWorkSessions(); err != nil || len(swept) != 0 {
		t.Fatalf("completed delivery was swept again: %+v %v", swept, err)
	}
	if ws, err := s.WorkSessionByID("ws_queued"); err != nil || ws == nil || ws.Status != "delivered" || ws.Result != "served: the child finished after restart" {
		t.Fatalf("queued child's real result was lost: %+v %v", ws, err)
	}
}

func TestReadOnlyCurrentProjectionWithoutWorkQueueKeepsMainWorkReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.StartWorkSession(t.Context(), "old_main", "ordinary operator work"); err != nil {
		t.Fatal(err)
	}
	// Isolate the missing-queue condition on a current table. The genuine
	// historical declaration is exercised in readonly_work_test.go.
	if _, err := s.DB().Exec(`DROP TABLE work_queue`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	readonly, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer readonly.Close()
	ws, err := readonly.ActiveWorkSession()
	if err != nil || ws == nil || ws.ID != "old_main" {
		t.Fatalf("boot-SAFE lost old main work without a queue table: %+v %v", ws, err)
	}
}

func TestReadOnlyProjectionWithQueueStillSeparatesChildSeat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, kept, err := s.EnqueueWorkWithSessionBelowLimit(&WorkItem{
		Kind: SubagentWorkKind, Payload: "{}", DedupKey: "ws_child", Source: "identity",
	}, 1, "ws_child", SubagentDescription("queued")); err != nil || !kept {
		t.Fatalf("enqueue child: kept=%t err=%v", kept, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	readonly, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer readonly.Close()
	if ws, err := readonly.ActiveWorkSession(); err != nil || ws != nil {
		t.Fatalf("read-only child became the main seat: %+v %v", ws, err)
	}
}
