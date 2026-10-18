package pluginhost

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// NOTHING IS CALLABLE BEFORE THE COMMIT, AND NOTHING PUBLISHED BEFORE IT
// IS LOST. A shadow activation is started and health-checkable with its
// declared routes prepared and unregistered; a guest that tries to
// publish a run-time tool then is refused by name — a route registered
// into the pre-commit set would have been swapped away whole by the
// commit, gone while the guest believed it published — and the one
// dispatch gate refuses any route that reaches the registry before the
// commit all the same. The commit opens the gate before it makes the
// routes reachable, so nothing admitted ever meets it.
func TestNothingIsCallableOrPublishedBeforeTheCommit(t *testing.T) {
	st := newBrokerStore(t)
	h, err := broker.New(broker.Config{Store: st, Grants: map[string]broker.Grant{"org.example.mcp": {Tools: true}}})
	if err != nil {
		t.Fatal(err)
	}
	reg := newRegistry(t)
	pkg := effectsPkg(t, "org.example.mcp", []byte(`[{"id":"roundtrip","summary":"Refresh the server's tools","effects":"write.local","capabilities":["tools.publish"]}]`), responderGuest(), []string{"tools.publish", "net.outbound:mcp.example.test:443"})
	ap, err := ActivateShadow(context.Background(), pkg, reg, &Options{Broker: h})
	if err != nil {
		t.Fatalf("ActivateShadow: %v", err)
	}
	t.Cleanup(func() { _ = ap.Deactivate(context.Background()) })
	if names := ap.Tools(); len(names) != 0 {
		t.Fatalf("a shadow activation's declared routes are not registered before the commit: %v", names)
	}
	spec := broker.PublishedTool{Name: "server.list_files", Summary: "List files", Effects: "read.external", Capabilities: []string{"net.outbound:mcp.example.test:443"}}
	if _, err := ap.PublishTool(spec); err == nil || !strings.Contains(err.Error(), "being admitted") {
		t.Fatalf("a publication before the commit is refused, by name: %v", err)
	}
	// The gate itself, on a route built for this activation before the
	// commit: it refuses, whatever registered it.
	early := ap.newOperationTool("early", "roundtrip", "an early route", nil, tools.Discovery{})
	if res, err := early.Execute(context.Background(), map[string]any{}); err != nil || !strings.Contains(res.Error, "being admitted") {
		t.Fatalf("the dispatch gate refuses before the commit: %+v %v", res, err)
	}
	// The commit: the gate opens, the routes go live in one step, and
	// publication is admitted from here.
	if err := ap.Redirect(nil); err != nil {
		t.Fatal(err)
	}
	if names := ap.Tools(); len(names) == 0 {
		t.Fatal("the declared routes are live after the commit")
	}
	name, err := ap.PublishTool(spec)
	if err != nil {
		t.Fatalf("publication after the commit: %v", err)
	}
	if res, err := reg.Execute(context.Background(), name, map[string]any{"path": "/"}); err != nil || strings.Contains(res.Error, "being admitted") {
		t.Fatalf("a route published after the commit answers: %+v %v", res, err)
	}
}
