package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

func TestInteractionAppendIdentityAndRollback(t *testing.T) {
	s, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	notifications := 0
	s.SetInteractionObserver(func() {
		notifications++
		if _, err := s.RecentTurns(10); err != nil {
			t.Error(err)
		}
	})
	in := InteractionInput{ID: "one", Kind: interaction.Message, Role: interaction.Operator, Content: "same"}
	ref, err := s.RecordInteraction(t.Context(), in)
	if err != nil || ref.Sequence != 1 {
		t.Fatal(ref, err)
	}
	again, err := s.RecordInteraction(t.Context(), in)
	if err != nil || again.ID != ref.ID || again.Sequence != ref.Sequence {
		t.Fatal(again, err)
	}
	in.ID = "two"
	if _, err = s.RecordInteraction(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	in.Content = "different"
	if _, err = s.RecordInteraction(t.Context(), in); err == nil {
		t.Fatal("conflicting ID accepted")
	}
	if notifications != 2 {
		t.Fatal("unexpected invalidations", notifications)
	}
	s.mu.Lock()
	tx, err := s.w().Begin()
	if err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	in.ID = "rolled-back"
	_, _, err = s.appendInteractionTx(t.Context(), tx, in)
	if err != nil {
		tx.Rollback()
		s.mu.Unlock()
		t.Fatal(err)
	}
	tx.Rollback()
	s.mu.Unlock()
	var n int
	if err = s.DB().QueryRow(`SELECT COUNT(*) FROM conversations`).Scan(&n); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	s.SetFrozen(true, "test")
	in.ID = "frozen"
	_, err = s.RecordInteraction(t.Context(), in)
	var frozen *FrozenError
	if !errors.As(err, &frozen) {
		t.Fatal("freeze bypass", err)
	}
	if notifications != 2 {
		t.Fatal("refused write notified")
	}
}
func TestInteractionMigrationPreservesLegacyAndIsRepeatable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DB().Exec(`INSERT INTO conversations(id,session_id,role,content,turn_seq,created_at) VALUES('old','s','participant','old words',0,'2026-09-25T12:00:00.1-04:00'),('bad','s','system','old system',1,'not-a-time'); INSERT INTO outbox(id,to_role,content,created_ms) VALUES('mail','operator','old words',1),('private','peer','not for operator',1)`)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	for i := 0; i < 2; i++ {
		s, err = New(path)
		if err != nil {
			t.Fatal(err)
		}
		var raw, norm, kind string
		if err = s.DB().QueryRow(`SELECT created_at,recorded_at,kind FROM conversations WHERE id='old'`).Scan(&raw, &norm, &kind); err != nil {
			t.Fatal(err)
		}
		if raw != "2026-09-25T12:00:00.1-04:00" || norm != "2026-09-25T16:00:00.100000000Z" || kind != "message" {
			t.Fatal(raw, norm, kind)
		}
		var n int
		if err = s.DB().QueryRow(`SELECT COUNT(*) FROM conversations`).Scan(&n); err != nil || n != 3 {
			t.Fatal(n, err)
		}
		if err = s.DB().QueryRow(`SELECT COUNT(*) FROM conversations WHERE id='bad' AND recorded_at IS NULL`).Scan(&n); err != nil || n != 1 {
			t.Fatal("invalid time repaired", n, err)
		}
		var ref string
		if err = s.DB().QueryRow(`SELECT interaction_id FROM outbox WHERE id='mail'`).Scan(&ref); err != nil || ref == "" {
			t.Fatal(ref, err)
		}
		if err = s.DB().QueryRow(`SELECT COUNT(*) FROM outbox WHERE id='private' AND interaction_id IS NULL`).Scan(&n); err != nil || n != 1 {
			t.Fatal("private import", n, err)
		}
		if err = s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
func TestInteractionProjectionDoesNotFeedOutboundToConversation(t *testing.T) {
	s, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.AddConversationTurn("operator", "spoken"); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	tx, err := s.w().Begin()
	if err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	_, _, err = s.appendInteractionTx(context.Background(), tx, InteractionInput{ID: "mail", Kind: interaction.Outbound, Role: interaction.Resident, Source: &interaction.Source{Kind: "outbox", ID: "m"}})
	if err == nil {
		err = tx.Commit()
	} else {
		tx.Rollback()
	}
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.RecentTurns(10)
	if err != nil || len(rows) != 1 || rows[0].Content != "spoken" {
		t.Fatal(rows, err)
	}
	if got, err := s.ConversationTurnCount(); err != nil || got != 1 {
		t.Fatal(got, err)
	}
	var n int
	if err = s.DB().QueryRow(`SELECT COUNT(*) FROM conversations_searchable`).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
}
func TestInteractionDetailsRetryUsesCanonicalPayload(t *testing.T) {
	s, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := time.Now()
	in := InteractionInput{ID: "same", Kind: interaction.Notice, Role: interaction.System, OccurredAt: &at, Details: interaction.Details{Reason: "<same>"}}
	if _, err = s.RecordInteraction(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordInteraction(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	_, err = json.Marshal(in.Details)
	if err != nil {
		t.Fatal(err)
	}
}

func TestInteractionReadSnapshotCannotWriteEvenInSafe(t *testing.T) {
	s, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetFrozen(true, "test")
	s.mu.RLock()
	r, err := s.beginReadSnapshot(t.Context())
	if err != nil {
		s.mu.RUnlock()
		t.Fatal(err)
	}
	rows, err := r.QueryContext(t.Context(), `INSERT INTO conversations(id,session_id,role,content,turn_seq,created_at) VALUES('bypass','s','operator','bad',1,'now')`)
	if rows != nil {
		rows.Close()
	}
	if err == nil {
		r.Close()
		s.mu.RUnlock()
		t.Fatal("read snapshot wrote through SAFE")
	}
	if err = r.Close(); err != nil {
		s.mu.RUnlock()
		t.Fatal(err)
	}
	s.mu.RUnlock()
	s.SetFrozen(false, "")
	if err = s.AddConversationTurn("operator", "after snapshot"); err != nil {
		t.Fatal("query_only leaked to writer", err)
	}
}

func TestInteractionOwnerTransactionsRollBackWhenRecordingFails(t *testing.T) {
	s, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.StartWorkSession(t.Context(), "work", "task"); err != nil {
		t.Fatal(err)
	}
	speech, err := s.AddConversationTurnSeq("operator", "question")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecordToolStart("turn", 1, "main", "model", "provider", "read", "{}"); err != nil {
		t.Fatal(err)
	}
	// Failure is injected at the shared INSERT seam, after owner operations have
	// made their tentative mutations in the same transaction.
	if _, err = s.DB().Exec(`CREATE TRIGGER refuse_history BEFORE INSERT ON conversations BEGIN SELECT RAISE(ABORT,'injected history failure'); END`); err != nil {
		t.Fatal(err)
	}
	plan := "must roll back"
	for name, write := range map[string]func() error{
		"tool start": func() error { return s.RecordToolStart("other", 1, "main", "model", "p", "read", "{}") },
		"tool result": func() error {
			return recordToolDone(s, "turn", 1, "read", "{}", "result", interaction.Succeeded, false)
		},
		"work state": func() error { return s.UpdateWorkState(t.Context(), "work", "changed") },
		"work plan":  func() error { return s.UpdateWorkPlan(t.Context(), "work", nil, nil, &plan, nil, nil, nil) },
		"work deliver": func() error {
			return s.DeliverWorkSession(t.Context(), "work", "served: done", "locally_verified", "readback")
		},
		"outbound":   func() error { return s.AddOutboxMessage("mail", "operator", "", "speech", nil) },
		"annotation": func() error { return s.AnnotateTurn(speech, "voice", "v/1", `{"session":"v","sequence":1}`) },
	} {
		if err := write(); err == nil {
			t.Fatalf("%s ignored recording failure", name)
		}
	}
	var n int
	for _, q := range []string{`SELECT COUNT(*) FROM conversations WHERE kind='tool_call' AND turn_id='other'`, `SELECT COUNT(*) FROM outbox WHERE id='mail'`, `SELECT COUNT(*) FROM turn_annotations`} {
		if err = s.DB().QueryRow(q).Scan(&n); err != nil || n != 0 {
			t.Fatal("partial owner write", q, n, err)
		}
	}
	ws, err := s.WorkSessionByID("work")
	if err != nil || ws.Status != "active" || ws.State != "" || ws.Plan != "" || ws.Result != "" {
		t.Fatal("partial work mutation", ws, err)
	}
	started, err := s.ToolCallStarted("turn", 1)
	if err != nil || !started {
		t.Fatal("tool was marked finished without its outcome", err)
	}
}

func TestInteractionExactReadbackCannotMatchDifferentFacts(t *testing.T) {
	s, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	in := InteractionInput{ID: "occurrence", Kind: interaction.Message, Role: interaction.Operator, Content: "same text", ProjectID: "original"}
	if _, err = s.RecordInteraction(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.matchInteractionInput(t.Context(), in); err != nil || !ok {
		t.Fatal(ok, err)
	}
	in.ProjectID = "different"
	if ok, err := s.matchInteractionInput(t.Context(), in); err != nil || ok {
		t.Fatal("same text hid differing project", ok, err)
	}
}

func TestInteractionWorkChangesCarryTheActualTurn(t *testing.T) {
	s, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := interaction.WithTurn(t.Context(), "actual-turn")
	if err = s.StartWorkSession(ctx, "session", "task"); err != nil {
		t.Fatal(err)
	}
	plan := "observable plan"
	if err = s.UpdateWorkPlan(ctx, "session", nil, nil, &plan, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateWorkState(ctx, "session", "state"); err != nil {
		t.Fatal(err)
	}
	if err = s.DeliverWorkSession(ctx, "session", "served: complete", "locally_verified", "readback"); err != nil {
		t.Fatal(err)
	}
	p, err := s.QueryInteractions(t.Context(), interaction.Query{Version: 1, Filter: interaction.Filter{TurnID: "actual-turn", WorkSession: "session"}})
	if err != nil || len(p.Rows) != 4 {
		t.Fatalf("changes cannot be joined to their turn: %+v %v", p, err)
	}
	for _, r := range p.Rows {
		if r.Kind != interaction.Notice || r.Details.Work == nil || r.Details.Work.Session != "session" {
			t.Fatalf("bad work context: %+v", r)
		}
	}
}
