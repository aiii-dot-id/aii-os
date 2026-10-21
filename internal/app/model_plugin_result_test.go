package app

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

func TestModelFacingPluginToolResultIsLabeledAndStillReadable(t *testing.T) {
	a := liveApp(t)
	const pluginID = "org.example.docs"
	const name = "pl_org_example_docs_read"
	if err := a.toolReg.RegisterDynamic(&fakeOp{name: name, out: `{"page":"useful foreign text"}`}, pluginID); err != nil {
		t.Fatal(err)
	}
	var call llm.ToolCall
	call.Function.Name = name
	call.Function.Arguments = `{}`
	obs := a.executeToolCall(t.Context(), call)
	if obs.Failed || strings.Count(obs.Text, untrusted.Open) != 1 ||
		strings.Count(obs.Text, untrusted.Close) != 1 ||
		!strings.Contains(obs.Text, "source: plugin "+pluginID) ||
		!strings.Contains(obs.Text, "useful foreign text") {
		t.Fatalf("resident lost a readable, labeled plugin answer: %+v", obs)
	}
}
