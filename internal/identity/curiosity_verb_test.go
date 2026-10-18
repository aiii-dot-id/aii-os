package identity

import (
	"context"
	"github.com/aiii-dot-id/aii-os/internal/memory"
	"strings"
	"testing"
)

func TestVerbCuriositySetShowClear(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	if _, err := engine.ExecuteAction(context.Background(), "verb", "curiosity",
		map[string]any{"action": "note"}); err == nil {
		t.Fatal("a cue needs a subject")
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "curiosity", map[string]any{
		"action": "note", "subject": "the shape of tail latency", "why": "bimodal under load", "pointer": "ws_probe",
	}); err != nil {
		t.Fatalf("note: %v", err)
	}
	c, _ := st.CuriosityCue()
	if c == nil || c.Subject != "the shape of tail latency" {
		t.Fatalf("cue not set through the verb: %+v", c)
	}
	out, err := engine.ExecuteAction(context.Background(), "verb", "curiosity", map[string]any{"action": "show"})
	if err != nil || !strings.Contains(out, "tail latency") || !strings.Contains(out, "invitation, not a task") {
		t.Fatalf("show: %v %q", err, out)
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "curiosity",
		map[string]any{"action": "note", "subject": "x", "kind": "obligation"}); err == nil {
		t.Fatal("kind must be invitation or active — a cue is never an obligation")
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "curiosity", map[string]any{"action": "clear"}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if c, _ := st.CuriosityCue(); c != nil {
		t.Fatalf("cue not cleared: %+v", c)
	}
}

func TestCuriosityInterestRecoverableBySubject(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	if _, err := engine.ExecuteAction(context.Background(), "verb", "note", map[string]any{
		"content": "the shape of tail latency under load is bimodal, worth a real probe",
	}); err != nil {
		t.Fatalf("note: %v", err)
	}
	if _, err := engine.ExecuteAction(context.Background(), "verb", "curiosity", map[string]any{
		"action": "note", "subject": "tail latency",
	}); err != nil {
		t.Fatalf("curiosity: %v", err)
	}
	found, _, err := memory.New(st).Enumerate(context.Background(), "experiences", "tail latency", false, 0, 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(found) == 0 {
		t.Fatal("the interest must be recoverable by its subject vocabulary, not only by a category label")
	}
}
