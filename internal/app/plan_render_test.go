package app

import (
	"strings"
	"testing"
)

// .
// .
// .
// .
func TestPlanSurfaceRendersInWorkState(t *testing.T) {
	a, _ := focusFixture(t)

	if err := a.store.StartWorkSession("ws_render", "cs-4 render"); err != nil {
		t.Fatal(err)
	}

	// .
	before, err := a.buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(before, "### Resume") {
		t.Fatalf("empty plan must render nothing:\n%s", before)
	}

	focus := "land the plan surface"
	next := "negative-control the render"
	plan := "## Plan (authored words — émoji ✓ and [ws_x] citations)\n- [ ] render verbatim"
	if err := a.store.UpdateWorkPlan("ws_render", &focus, &next, &plan, nil, nil, nil); err != nil {
		t.Fatal(err)
	}

	state, err := a.buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Focus: land the plan surface",
		"Next action: negative-control the render",
		"## Plan (authored words — émoji ✓ and [ws_x] citations)",
	} {
		if !strings.Contains(state, want) {
			t.Fatalf("render missing %q:\n%s", want, state)
		}
	}

	// .
	if !strings.Contains(state, "- [ ] render verbatim") {
		t.Fatalf("plan body not verbatim:\n%s", state)
	}
}
