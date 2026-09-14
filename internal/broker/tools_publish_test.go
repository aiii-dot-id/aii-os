package broker

import (
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

type fakePublisher struct {
	published []PublishedTool
	withdrawn []string
	refuse    string
}

func (f *fakePublisher) PublishTool(spec PublishedTool) (string, error) {
	if f.refuse != "" {
		return "", fmt.Errorf("%s", f.refuse)
	}
	f.published = append(f.published, spec)
	return "pl_p_dyn_" + strings.ReplaceAll(spec.Name, ".", "_"), nil
}
func (f *fakePublisher) WithdrawTool(name string) error {
	f.withdrawn = append(f.withdrawn, name)
	return nil
}

// .
// .
// .
func TestPublishingToolsPassesTheRingsAndIsReceipted(t *testing.T) {
	st := newStore(t)
	h := newHost(t, st, Config{Grants: map[string]Grant{"p": {Tools: true}, "q": {KV: true}}})
	pub := &fakePublisher{}
	b := h.Bind("p", packagefmt.TierT1, []string{"tools.publish"})
	b.SetPublisher(pub)
	spec := `{"name":"server.list","summary":"List files on the server","input":{"type":"object","properties":{"path":{"type":"string"}}},"effects":"read.external","capabilities":[]}`
	m := dispatch(t, b, `{"operation":"tools.publish","arguments":`+spec+`}`)
	rec := wantResult(t, m, statusSucceeded, "")
	if len(pub.published) != 1 || pub.published[0].Name != "server.list" || !strings.Contains(string(m["operation_result"]), `"tool":"pl_p_dyn_server_list"`) {
		t.Fatalf("published: %+v %s", pub.published, m["operation_result"])
	}
	if !strings.Contains(string(rec["target"]), "server.list") {
		t.Fatalf("receipted by name: %s", rec["target"])
	}
	wantResult(t, dispatch(t, b, `{"operation":"tools.withdraw","target":{"name":"server.list"}}`), statusSucceeded, "")
	if len(pub.withdrawn) != 1 {
		t.Fatal("withdrawn")
	}
	wantResult(t, dispatch(t, b, `{"operation":"tools.withdraw"}`), statusDenied, reasonTargetInvalid)
	wantResult(t, dispatch(t, b, `{"operation":"tools.publish"}`), statusDenied, reasonArgumentInvalid)
	pub.refuse = "capability net.outbound:x is outside the envelope"
	wantResult(t, dispatch(t, b, `{"operation":"tools.publish","arguments":`+spec+`}`), statusDenied, reasonArgumentInvalid)

	noEnvelope := h.Bind("p", packagefmt.TierT1, nil)
	noEnvelope.SetPublisher(pub)
	if reply, _ := noEnvelope.Dispatch(t.Context(), "invoke-call", []byte(`{"operation":"tools.publish","arguments":`+spec+`}`)); !strings.Contains(string(reply), reasonNotInEnvelope) {
		t.Fatalf("outside the envelope: %s", reply)
	}
	noGrant := h.Bind("q", packagefmt.TierT1, []string{"tools.publish"})
	noGrant.SetPublisher(pub)
	if reply, _ := noGrant.Dispatch(t.Context(), "invoke-call", []byte(`{"operation":"tools.publish","arguments":`+spec+`}`)); !strings.Contains(string(reply), "plugins.grants.q.tools") {
		t.Fatalf("without the operator's word: %s", reply)
	}
	t0 := h.Bind("p", packagefmt.TierT0, []string{"tools.publish"})
	t0.SetPublisher(pub)
	if reply, _ := t0.Dispatch(t.Context(), "invoke-call", []byte(`{"operation":"tools.publish","arguments":`+spec+`}`)); !strings.Contains(string(reply), reasonTierDenied) {
		t.Fatalf("T0 cannot grow the identity's tools: %s", reply)
	}
	b.BeginOperation(OperationScope{Operation: "look", Effects: EffectsReadInternal, Capabilities: []string{"tools.publish"}, Declared: true})
	if reply, _ := b.Dispatch(t.Context(), "invoke-call", []byte(`{"operation":"tools.publish","arguments":`+spec+`}`)); !strings.Contains(string(reply), "declares effects read.internal") {
		t.Fatalf("a read-only operation cannot publish: %s", reply)
	}
	b.EndOperation()
	bare := h.Bind("p", packagefmt.TierT1, []string{"tools.publish"})
	wantResult(t, dispatch(t, bare, `{"operation":"tools.publish","arguments":`+spec+`}`), statusDenied, reasonPolicyDeny)
}
