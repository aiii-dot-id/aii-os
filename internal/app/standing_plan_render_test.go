package app

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
// .
func TestStandingPlanRendersAfterSessionClose(t *testing.T) {
	projection, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer projection.Close()

	// .
	if err := projection.StartWorkSession("ws_x", "arc"); err != nil {
		t.Fatal(err)
	}
	focus := "post-swap verification"
	if err := projection.UpdateWorkPlan("ws_x", &focus, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := projection.DeliverWorkSession("ws_x", "done", "", ""); err != nil {
		t.Fatal(err)
	}

	ws, err := (&App{store: projection}).buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ws, "### Standing plan") || !strings.Contains(ws, focus) {
		t.Fatalf("standing plan did not render after close:\n%s", ws)
	}

	// .
	// .
	if err := projection.StartWorkSession("ws_y", "new arc"); err != nil {
		t.Fatal(err)
	}
	focus2 := "new arc focus"
	if err := projection.UpdateWorkPlan("ws_y", &focus2, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	ws, err = (&App{store: projection}).buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ws, "### Standing plan") {
		t.Fatalf("standing plan overrode an active session's plan:\n%s", ws)
	}
	if !strings.Contains(ws, "### Resume") || !strings.Contains(ws, focus2) {
		t.Fatalf("active plan missing:\n%s", ws)
	}
}
