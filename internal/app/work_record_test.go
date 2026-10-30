package app

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"github.com/aiii-dot-id/aii-os/internal/workrecord"
)

func TestTheWorkRecordOnABootedIdentity(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	tr := appTranscript{st: app.store}
	if app.store.ToolResultRunes() != store.TranscriptResultLimit || tr.TranscriptResultExcerptLimit() != store.TranscriptResultLimit {
		t.Fatalf("with nothing set the record keeps %d and the notice offers %d; want %d", app.store.ToolResultRunes(), tr.TranscriptResultExcerptLimit(), store.TranscriptResultLimit)
	}
	if _, err := app.applyConfigChange(map[string]any{"record.tool_result_runes": float64(50)}); err != nil {
		t.Fatalf("the door refused the record's bound: %v", err)
	}
	if app.store.ToolResultRunes() != 50 || tr.TranscriptResultExcerptLimit() != 50 {
		t.Fatalf("set to 50, the record keeps %d and the notice offers %d", app.store.ToolResultRunes(), tr.TranscriptResultExcerptLimit())
	}
	if got := app.configSnapshot().Record.ToolResultRunes; got != 50 {
		t.Fatalf("the configuration in force states %d", got)
	}

	ctx := context.Background()
	if _, err := app.engine.ExecuteAction(ctx, "verb", "work", map[string]any{"action": "start", "description": "the booted session"}); err != nil {
		t.Fatal(err)
	}
	if err := app.store.RecordToolStart("turn-w", 1, "main", "m", "", "shell", `{"command":"make"}`); err != nil {
		t.Fatal(err)
	}
	if err := app.store.RecordToolDoneMeasured("turn-w", 1, "shell", `{"command":"make"}`, strings.Repeat("z", 80), interaction.Succeeded, false, store.ToolMeasurement{}); err != nil {
		t.Fatal(err)
	}
	out, err := app.engine.ExecuteAction(ctx, "verb", "recall", map[string]any{"source": "work", "query": ""})
	if err != nil {
		t.Fatalf("recall source=work on a booted identity: %v", err)
	}
	if !strings.Contains(out, "the booted session") || !strings.Contains(out, strings.Repeat("z", 50)) || strings.Contains(out, strings.Repeat("z", 51)) || !strings.Contains(out, "[the first 50 of 80 characters]") {
		t.Fatalf("the booted identity's own call, kept at the operator's 50:\n%s", out)
	}

	// Through the executor, in the context the loop gives a call: the
	// recall's own row, written before it runs, is not in its answer.
	const turn = "turn-loop"
	args := `{"source":"work","query":"","limit":10}`
	if err := tr.RecordToolStart(turn, 1, "call-loop", "recall", args, "m"); err != nil {
		t.Fatal(err)
	}
	var call llm.ToolCall
	call.ID, call.Type, call.Function.Name, call.Function.Arguments = "call-loop", "function", "recall", args
	obs := appToolExecutor{app}.Execute(interaction.WithTurn(ctx, turn), call)
	if obs.Failed || !strings.Contains(obs.Text, "the booted session") || strings.Contains(obs.Text, `"limit":10`) || strings.Contains(obs.Text, "no result recorded yet") {
		t.Fatalf("the read lists its own running call:\n%s", obs.Text)
	}

	h := &dashboard.WSHandler{}
	app.wireRecordHooks(h)
	if h.WorkExport == nil {
		t.Fatal("the dashboard is given no export")
	}
	var buf bytes.Buffer
	if err := h.WorkExport(ctx, time.Time{}, "", "", &buf); err != nil {
		t.Fatal(err)
	}
	if n, err := workrecord.Check(bytes.NewReader(buf.Bytes())); err != nil || n != 1 || !strings.Contains(buf.String(), "the booted session") {
		t.Fatalf("the hook's export: %d %v\n%s", n, err, buf.String())
	}

	if _, err := app.applyConfigChange(map[string]any{"record.tool_result_runes": nil}); err != nil {
		t.Fatal(err)
	}
	if app.store.ToolResultRunes() != store.TranscriptResultLimit {
		t.Fatalf("cleared, the record keeps %d", app.store.ToolResultRunes())
	}
}

func TestTheHostFrameRuleReadsOnlyNamesTheHostAssigns(t *testing.T) {
	if identity.PluginToolPrefix != pluginhost.ToolPrefix {
		t.Fatalf("the work record reads %q, the plugin host assigns %q", identity.PluginToolPrefix, pluginhost.ToolPrefix)
	}
	builtins := tools.AllBuiltins()
	for _, name := range builtins {
		if strings.HasPrefix(name, pluginhost.ToolPrefix) {
			t.Errorf("builtin %q is named under the plugin prefix", name)
		}
	}
	if !slices.Contains(builtins, identity.WebFetchTool) {
		t.Errorf("web_fetch is not a builtin: %v", builtins)
	}
}
