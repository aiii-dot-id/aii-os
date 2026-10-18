package llm

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
)

// The three context values that moved to wire — the producing model, the
// cache seam, the tap label — keep one key each, wire's. The facilities
// set them through wire (cognitive names nothing of llm), the client and
// the identity's append read them through llm, and the conversation loop
// sets the model through llm. A key for one of them re-declared in llm
// compiles and splits its channel without a sound, so each is set on one
// side of the package boundary and read on the other: the model both
// ways, the seam at ChatSimple's system message (simpleMessages), the
// label at the tap's category. It says nothing of the keys llm keeps for
// its own per-call state, nor of ChatStructured's read of the seam.
func TestTheMovedContextValuesCrossTheBoundary(t *testing.T) {
	ctx := t.Context()
	if got := ModelIDFromContext(wire.WithModelID(ctx, "m-wire")); got != "m-wire" {
		t.Errorf("a model set through wire reads through llm as %q", got)
	}
	if got := wire.ModelIDFromContext(WithModelID(ctx, "m-llm")); got != "m-llm" {
		t.Errorf("a model set through llm reads through wire as %q", got)
	}
	if msgs := simpleMessages(wire.WithStablePrefix(ctx, 6), "stable\n\nrest", "u"); msgs[0].StableLen != 6 {
		t.Errorf("a facility's cache seam set through wire reaches the system message as %d", msgs[0].StableLen)
	}
	if got := tapCategory(wire.WithTapSource(ctx, "dream"), "prompt"); got != "dream.prompt" {
		t.Errorf("a facility label set through wire is captured under %q", got)
	}
	if got := tapCategory(ctx, "prompt"); got != "llm.prompt" {
		t.Errorf("an unlabelled call is captured under %q", got)
	}
}
