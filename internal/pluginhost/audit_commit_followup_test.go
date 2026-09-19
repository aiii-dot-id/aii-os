// .
// .
// .
// .

package pluginhost

import (
	"context"
	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// .
type auditCommitInvoker struct{ calls atomic.Int32 }

func (i *auditCommitInvoker) Invoke(context.Context, []byte) ([]byte, error) {
	i.calls.Add(1)
	return []byte(`{"jsonrpc":"2.0","id":"h1","result":{"output":"ok"}}`), nil
}
func auditCommitActivation(t *testing.T, reg *tools.Registry) *ActivePlugin {
	ap := &ActivePlugin{ID: "org.example.audit", Version: "1", reg: reg, inv: &auditCommitInvoker{}}
	tool := ap.newOperationTool("audit.declared", "declared", "audit", nil, tools.Discovery{})
	ap.pending = []pendingTool{{tool: tool}}
	return ap
}
func TestAuditCommitRefusesPrecommitThenAdmits(t *testing.T) {
	reg := newRegistry(t)
	ap := auditCommitActivation(t, reg)
	early := ap.pending[0].tool
	spec := broker.PublishedTool{Name: "runtime.list", Summary: "list", Effects: broker.EffectsReadInternal}
	if _, err := ap.PublishTool(spec); err == nil {
		t.Error("publication admitted before commit")
	}
	if r, err := early.Execute(context.Background(), nil); err != nil || !strings.Contains(r.Error, "being admitted") {
		t.Errorf("early route did not refuse: %+v %v", r, err)
	}
	if err := ap.Redirect(nil); err != nil {
		t.Fatal(err)
	}
	name, err := ap.PublishTool(spec)
	if err != nil {
		t.Fatal(err)
	}
	r, err := reg.Execute(context.Background(), name, nil)
	if err != nil || r.Error != "" {
		t.Errorf("committed operation refused: %+v %v", r, err)
	}
	if err := ap.Deactivate(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestAuditCommitKeepsConcurrentPublicationOwned(t *testing.T) {
	reg := newRegistry(t)
	ap := auditCommitActivation(t, reg)
	prior := redirectSwapped
	defer func() { redirectSwapped = prior }()
	type answer struct {
		name string
		err  error
	}
	done := make(chan answer, 1)
	var result answer
	joined := false
	redirectSwapped = func() {
		entered := make(chan struct{})
		go func() {
			close(entered)
			n, e := ap.PublishTool(broker.PublishedTool{Name: "runtime.list", Summary: "list", Effects: broker.EffectsReadInternal})
			done <- answer{n, e}
		}()
		<-entered
		// .
		select {
		case result = <-done:
			joined = true
		case <-time.After(100 * time.Millisecond):
		}
	}
	if err := ap.Redirect(nil); err != nil {
		t.Fatal(err)
	}
	if !joined {
		select {
		case result = <-done:
		case <-time.After(time.Second):
			t.Fatal("publication did not finish after commit")
		}
	}
	if result.err != nil {
		if !strings.Contains(result.err.Error(), "being admitted") {
			t.Fatalf("unexpected publication refusal: %v", result.err)
		}
		return
	}
	found := false
	for _, name := range ap.Tools() {
		if name == result.name {
			found = true
		}
	}
	if !found {
		t.Errorf("successful publication %s is missing from activation-owned Tools", result.name)
	}
	if _, ok := reg.Get(result.name); !ok {
		t.Error("successful publication is absent from registry")
	}
	if err := ap.Deactivate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get(result.name); ok {
		t.Error("published route remains registered after Deactivate")
	}
	reg.Deregister(result.name)
}

func TestAuditCommitFailureLeavesGateClosed(t *testing.T) {
	reg := newRegistry(t)
	ap := auditCommitActivation(t, reg)
	other := &operationTool{name: "audit.collision", operation: "collision", inv: &auditCommitInvoker{}}
	if err := reg.RegisterDynamic(other, "org.example.other"); err != nil {
		t.Fatal(err)
	}
	candidate := ap.newOperationTool("audit.collision", "collision", "candidate", nil, tools.Discovery{})
	ap.pending = append(ap.pending, pendingTool{tool: candidate})
	if err := ap.registerTools(); err == nil {
		t.Fatal("fixture did not collide")
	}
	if ap.admitted.Load() {
		t.Error("failed initial commit left the dispatch gate open")
	}
	name, err := ap.PublishTool(broker.PublishedTool{Name: "runtime.list", Summary: "list", Effects: broker.EffectsReadInternal})
	if err == nil {
		t.Error("failed activation accepted a new publication")
		reg.Deregister(name)
	}
	if _, ok := reg.Get("audit.declared"); ok {
		t.Error("partial declared route was not rolled back")
	}
	reg.Deregister("audit.collision")
}
