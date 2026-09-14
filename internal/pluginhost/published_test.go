package pluginhost

import (
	"context"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
)

// .
// .
// .
// .
// .
// .
func TestPublishedToolsAreAdmittedLikeDeclaredOnes(t *testing.T) {
	st := newBrokerStore(t)
	h, err := broker.New(broker.Config{Store: st, Grants: map[string]broker.Grant{"org.example.mcp": {Tools: true}}})
	if err != nil {
		t.Fatal(err)
	}
	reg := newRegistry(t)
	pkg := effectsPkg(t, "org.example.mcp", []byte(`[{"id":"roundtrip","summary":"Refresh the server's tools","effects":"write.local","capabilities":["tools.publish"]}]`), responderGuest(), []string{"tools.publish", "net.outbound:mcp.example.test:443"})
	ap, err := Activate(context.Background(), pkg, reg, &Options{Broker: h})
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	t.Cleanup(func() { _ = ap.Deactivate(context.Background()) })

	name, err := ap.PublishTool(broker.PublishedTool{Name: "server.list_files", Summary: "List files the server holds", Input: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"path": map[string]interface{}{"type": "string"}}, "required": []interface{}{"path"}, "additionalProperties": false}, Effects: "read.external", Capabilities: []string{"net.outbound:mcp.example.test:443"}})
	if err != nil || name != "pl_org_example_mcp_dyn_server_list_files" {
		t.Fatalf("publish: %q %v", name, err)
	}
	if st, _, ok := reg.State(name); !ok || st != "inspect-only" {
		t.Fatalf("born inspect-only: %v %v", st, ok)
	}
	card, err := reg.Show("server.list_files")
	if err != nil || card.Plugin != "org.example.mcp" || card.Summary != "List files the server holds" || card.Family != "server" || card.Effects != "read.external" {
		t.Fatalf("discoverable like a declared operation: %+v %v", card, err)
	}
	res, err := reg.Execute(context.Background(), name, map[string]interface{}{})
	if err != nil || res.Error == "" || !strings.Contains(res.Error, "path") {
		t.Fatalf("the schema is enforced before the call: %+v %v", res, err)
	}
	res, err = reg.Execute(context.Background(), name, map[string]interface{}{"path": 7})
	if err != nil || !strings.Contains(res.Error, "arguments refused before the call") {
		t.Fatalf("a wrongly typed argument is refused before the call: %+v %v", res, err)
	}
	res, err = reg.Execute(context.Background(), name, map[string]interface{}{"path": "/"})
	if err != nil || res.Error != "" || !strings.Contains(res.Output, `"echoed":true`) {
		t.Fatalf("routed to the guest: %+v %v", res, err)
	}

	for what, spec := range map[string]broker.PublishedTool{
		"outside the envelope":        {Name: "server.exec", Summary: "s", Effects: "exec", Capabilities: []string{"net.outbound:elsewhere.test:443"}},
		"a bad name":                  {Name: "Server List", Summary: "s", Effects: "read.internal"},
		"no summary":                  {Name: "server.a", Effects: "read.internal"},
		"an unknown effect":           {Name: "server.b", Summary: "s", Effects: "mutate"},
		"an undeclared effect":        {Name: "server.c", Summary: "s", Effects: ""},
		"a schema outside the subset": {Name: "server.d", Summary: "s", Effects: "read.internal", Input: map[string]interface{}{"type": "object", "properties": map[string]interface{}{"x": map[string]interface{}{"$ref": "#/defs/x"}}}},
		"a duplicate":                 {Name: "server.list_files", Summary: "again", Effects: "read.internal"},
	} {
		if _, err := ap.PublishTool(spec); err == nil {
			t.Fatalf("%s must be refused", what)
		}
	}
	if err := ap.WithdrawTool("server.list_files"); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get(name); ok {
		t.Fatal("withdrawn from the registry")
	}
	if err := ap.WithdrawTool("server.list_files"); err == nil {
		t.Fatal("withdrawing twice is an error")
	}
	for i := 0; i < MaxPublishedTools; i++ {
		if _, err := ap.PublishTool(broker.PublishedTool{Name: "server.t" + strings.Repeat("x", i%3) + string(rune('a'+i%26)) + strings.Repeat("y", i/26), Summary: "s", Effects: "read.internal"}); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}
	if _, err := ap.PublishTool(broker.PublishedTool{Name: "server.one_more", Summary: "s", Effects: "read.internal"}); err == nil || !strings.Contains(err.Error(), "ceiling") {
		t.Fatalf("the thirty-third is refused: %v", err)
	}
	published := ap.Published()
	if len(published) != MaxPublishedTools {
		t.Fatalf("published: %d", len(published))
	}
	if err := ap.Deactivate(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, n := range reg.Names() {
		if strings.HasPrefix(n, "pl_org_example_mcp_dyn_") {
			t.Fatalf("the activation's end withdraws its tools: %s remains", n)
		}
	}
}

func responderGuest() []byte { return wasmgen.Responder() }

// .
// .
// .
// .
// .
// .
// .
func TestAPublishedToolAnswersToTheOperatorsGate(t *testing.T) {
	st := newBrokerStore(t)
	h, err := broker.New(broker.Config{Store: st, Grants: map[string]broker.Grant{"org.example.mcp": {Tools: true}}})
	if err != nil {
		t.Fatal(err)
	}
	reg := newRegistry(t)
	pkg := effectsPkg(t, "org.example.mcp", []byte(`[{"id":"roundtrip","summary":"Refresh","effects":"write.local","capabilities":["tools.publish"]}]`), responderGuest(), []string{"tools.publish", "net.outbound:mcp.example.test:443"})
	seam := &gatedProposer{readOnly: true}
	ap, err := Activate(context.Background(), pkg, reg, &Options{Broker: h, Acts: seam})
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	t.Cleanup(func() { _ = ap.Deactivate(context.Background()) })

	name, err := ap.PublishTool(broker.PublishedTool{Name: "server.write", Summary: "Write something out there",
		Effects: "write.external", Capabilities: []string{"net.outbound:mcp.example.test:443"}})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	res, err := reg.Execute(context.Background(), name, map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	if len(seam.gated) != 1 || seam.gated[0] != "server.write:write.external" {
		t.Fatalf("the operator's gate is consulted before anything runs: %v", seam.gated)
	}
	if !strings.Contains(res.Error, "read only") || !strings.Contains(res.Error, "nothing ran") {
		t.Fatalf("and it refuses the write, naming the operator's word: %+v", res)
	}
	// .
	// .
	if !strings.Contains(res.Error, "org.example.mcp") {
		t.Fatalf("the refusal names the plugin the operator answered about: %+v", res)
	}
}
