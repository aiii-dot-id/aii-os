package pluginhost

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// .
// .
// .
// .
// .
func TestAnOperationIsReplaySafeOnlyByItsDeclaredEffects(t *testing.T) {
	ap := &ActivePlugin{ID: "org.example.fx"}
	for effects, want := range map[string]bool{
		broker.EffectsReadInternal:  true,
		broker.EffectsReadExternal:  true,
		broker.EffectsWriteLocal:    false,
		broker.EffectsWriteExternal: false,
		broker.EffectsExec:          false,
		"":                          false,
		"read":                      false,
	} {
		tool := ap.newOperationTool("pl_org_example_fx_get", "fx.get", "", &opDescriptor{effects: effects}, tools.Discovery{})
		if got := tool.ReplaySafe(); got != want {
			t.Errorf("effects %q: replay-safe=%v, want %v", effects, got, want)
		}
	}
	if ap.newOperationTool("pl_org_example_fx_get", "fx.get", "", nil, tools.Discovery{}).ReplaySafe() {
		t.Error("an operation with no descriptor was licensed")
	}
	confirmed := ap.newOperationTool("pl_org_example_fx_get", "fx.get", "", &opDescriptor{effects: broker.EffectsReadInternal}, tools.Discovery{OperatorConfirms: true})
	if confirmed.ReplaySafe() {
		t.Error("an operation that runs only on the operator's confirmation was licensed for replay")
	}
	var _ tools.ReplaySafeTool = confirmed
}
