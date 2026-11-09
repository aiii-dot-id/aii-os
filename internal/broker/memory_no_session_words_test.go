package broker

import (
	"context"
	"testing"
)

// THE PROJECT-NOTE REFUSAL SAYS WHY AND WHAT TO DO: with no work
// session open it names that, says whether the last one is closed, and
// names the way out. The behaviour is unchanged: no fallback.
func TestTheProjectNoteRefusalSaysNoSessionIsOpen(t *testing.T) {
	h, b := memHost(t, Config{})
	_, err := b.memorySelection("project")
	if err == nil || err.Error() != "No work session is open, so this note can't be tied to a project. Open a work session, or save it with applicability=reusable." {
		t.Fatalf("with no session ever opened: %v", err)
	}
	ctx := context.Background()
	if err := h.cfg.Store.StartWorkSession(ctx, "ws_once", "a task"); err != nil {
		t.Fatal(err)
	}
	if err := h.cfg.Store.DeliverWorkSession(ctx, "ws_once", "done", "", ""); err != nil {
		t.Fatal(err)
	}
	_, err = b.memorySelection("project")
	if err == nil || err.Error() != "No work session is open (the last one is closed), so this note can't be tied to a project. Open a work session, or save it with applicability=reusable." {
		t.Fatalf("with the last session closed: %v", err)
	}
	// A task-scoped note, the same shape.
	_, err = b.memorySelection("task")
	if err == nil || err.Error() != "No work session is open (the last one is closed), so this note can't be tied to a task. Open a work session, or save it with applicability=reusable." {
		t.Fatalf("the task refusal: %v", err)
	}
}
