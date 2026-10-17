package app

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// probesOf is the card of plugin id as the Plugins page is sent it.
func probesOf(t *testing.T, a *App, id string) *dashboard.PluginProbesView {
	t.Helper()
	cfg := a.configSnapshot()
	for _, v := range a.pluginViews(&cfg) {
		if v.ID == id {
			return v.Probes
		}
	}
	t.Fatalf("no card for %s", id)
	return nil
}

func TestTheProbeVerdictOfASourceIsOnItsCard(t *testing.T) {
	good := liveApp(t)
	s := runSource(t, good, "org.example.vectors", working)
	checked(t, good, s)
	if p := probesOf(t, good, s.ap.ID); p == nil || p.State != "passed" || p.At == "" {
		t.Fatalf("a source whose probes agreed: %+v", p)
	}

	broken := liveApp(t)
	b := runSource(t, broken, "org.example.vectors", func(ctx context.Context, kind, text string) (tools.Result, error) {
		if kind == pluginhost.EmbedKindMemory {
			return tools.Result{Output: `{"vector":[1,0]}`}, nil
		}
		return working(ctx, kind, text)
	})
	checked(t, broken, b)
	if p := probesOf(t, broken, b.ap.ID); p == nil || p.State != "failed" || !strings.Contains(p.Words, "probe 2") || !strings.Contains(p.Words, "0.800") {
		t.Fatalf("a source whose second probe did not agree: %+v", p)
	}

	slow := liveApp(t)
	slow.memory.checkRetry = 1 << 62
	u := runSource(t, slow, "org.example.vectors", func(context.Context, string, string) (tools.Result, error) {
		return tools.Result{Error: "operation failed: the worker is starting"}, nil
	})
	checked(t, slow, u)
	if p := probesOf(t, slow, u.ap.ID); p == nil || p.State != "pending" || !strings.Contains(p.Words, "probe 1") {
		t.Fatalf("a source whose probe was not answered: %+v", p)
	}

	declared := liveApp(t)
	d := runSource(t, declared, "org.example.vectors", working)
	d.ap.DeclareChecksForTest(&packagefmt.Validation{Checks: []packagefmt.Check{{Name: "embeds", Sets: []string{packagefmt.AllSets}, Lane: packagefmt.LaneEmbeddings}}}, &pluginfacility.Checks{Ran: 1})
	if p := probesOf(t, declared, d.ap.ID); p != nil {
		t.Fatalf("a source that declares its own checks carries no probe verdict: %+v", p)
	}
}
