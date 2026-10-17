package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"reflect"
	"strings"
	"testing"
)

func TestContextReadRefusesEveryNonLocalOperation(t *testing.T) {
	h, b := memHost(t, Config{})
	b.BeginOperation(OperationScope{Operation: "context", ContextRead: true, Declared: true, Capabilities: []string{"ring4.memory"}, Effects: EffectsReadInternal})
	for op, spec := range operations {
		if spec.contextRead {
			continue
		}
		wantErrorReason(t, dispatch(t, b, memParams(op, `{}`)), "CONTEXT_READ_ONLY")
	}
	var n int
	if err := h.cfg.Store.DB().QueryRow(`SELECT COUNT(*) FROM plugin_memories`).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

func TestContextReadDoesNotReinforceByIDOrQuery(t *testing.T) {
	h, b := memHost(t, Config{})
	note := remember(t, b, `{"text":"compiler fixture recipe"}`)
	ref := store.MemoryRef{Store: "plugin_memories", ID: note.ID}
	before, _, err := h.cfg.Store.MemoryAccessOf(ref)
	if err != nil {
		t.Fatal(err)
	}
	b.BeginOperation(OperationScope{Operation: "context", ContextRead: true, Declared: true, Capabilities: []string{"ring4.memory"}, Effects: EffectsReadInternal})
	for _, args := range []string{fmt.Sprintf(`{"id":%q}`, note.ID), `{"query":"compiler fixture recipe"}`, `{"recent":true}`} {
		r := recall(t, b, args)
		if len(r.Hits) != 1 {
			t.Fatal(args, r)
		}
	}
	after, _, err := h.cfg.Store.MemoryAccessOf(ref)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("context read reinforced: before=%+v after=%+v err=%v", before, after, err)
	}
	b.EndOperation()
	recall(t, b, fmt.Sprintf(`{"id":%q}`, note.ID))
	after, _, err = h.cfg.Store.MemoryAccessOf(ref)
	if err != nil || after.Count != before.Count+1 {
		t.Fatal("normal detail semantics changed", before, after, err)
	}
}

type contextCountingEmbedder struct{ calls int }

func (*contextCountingEmbedder) Basis() (string, error) { return "fixture/v1", nil }
func (*contextCountingEmbedder) Floor() float64         { return 0.5 }
func (e *contextCountingEmbedder) Embed(_ context.Context, _ memory.Kind, inputs []string) (string, [][]float32, error) {
	e.calls++
	out := make([][]float32, len(inputs))
	for i := range out {
		out[i] = []float32{1, 0}
	}
	return "fixture/v1", out, nil
}
func TestContextQueryNeverCallsInstalledMeaningProvider(t *testing.T) {
	e := &contextCountingEmbedder{}
	_, b := memHost(t, Config{Meaning: e})
	remember(t, b, `{"text":"compiler fixture recipe"}`)
	before := e.calls
	b.BeginOperation(OperationScope{Operation: "context", ContextRead: true, Declared: true, Capabilities: []string{"ring4.memory"}, Effects: EffectsReadInternal})
	recall(t, b, `{"query":"compiler fixture recipe"}`)
	if e.calls != before {
		t.Fatal("context called embedding provider", e.calls, before)
	}
	b.EndOperation()
	recall(t, b, `{"query":"compiler fixture recipe"}`)
	if e.calls <= before {
		t.Fatal("control never consulted installed embedding provider")
	}
}

func TestContextRecallBudgetIsHonestAndOrdinaryDetailUnchanged(t *testing.T) {
	h, b := memHost(t, Config{})
	for i := 0; i < 5; i++ {
		if e := h.cfg.Store.PluginMemoryAdd(b.storageScope(), store.PluginMemory{ID: fmt.Sprintf("long-%d", i), Text: "compiler fixture " + strings.Repeat("a", 16000)}, 100, ""); e != nil {
			t.Fatal(e)
		}
	}
	b.BeginOperation(OperationScope{Operation: "context", ContextRead: true, Declared: true, Capabilities: []string{"ring4.memory"}, Effects: EffectsReadInternal})
	r := dispatch(t, b, memParams(opMemoryRecall, `{"query":"compiler fixture","limit":5}`))
	wantResult(t, r, statusSucceeded, "")
	var page recallResult
	if e := json.Unmarshal(r["operation_result"], &page); e != nil {
		t.Fatal(e)
	}
	if len(r["operation_result"]) > contextRecallResultBytes || page.Matched != 5 || page.Shown >= 5 || page.Shown < 1 || !page.Truncated || page.Status != "partial" {
		t.Fatal(page, len(r["operation_result"]))
	}

	if e := h.cfg.Store.PluginMemoryAdd(b.storageScope(), store.PluginMemory{ID: "escaped", Text: strings.Repeat("\x01", 16000)}, 100, ""); e != nil {
		t.Fatal(e)
	}
	wantResult(t, dispatch(t, b, memParams(opMemoryRecall, `{"id":"escaped"}`)), statusFailed, reasonMemorySourceUnavailable)
	b.EndOperation()
	r = dispatch(t, b, memParams(opMemoryRecall, `{"id":"escaped"}`))
	wantResult(t, r, statusSucceeded, "")
	if len(r["operation_result"]) <= contextRecallResultBytes {
		t.Fatal("fixture did not cross context bound")
	}
}

func TestAnyWordsBrokerScopePrecedesLimitAndDefaultsReinforce(t *testing.T) {
	h, b := memHost(t, Config{})
	memoryTask(t, b, "task-a", "project-a")
	eligible := remember(t, b, `{"text":"compiler fixture recipe","applicability":"project"}`)
	memoryTask(t, b, "task-b", "project-b")
	for i := 0; i < 12; i++ {
		remember(t, b, fmt.Sprintf(`{"text":%q,"applicability":"task"}`, fmt.Sprintf("compiler fixture schema repair missing argument unrelated private %d", i)))
	}
	setMemoryTask(b, "task-a")
	ref := store.MemoryRef{Store: "plugin_memories", ID: eligible.ID}
	before, _, e := h.cfg.Store.MemoryAccessOf(ref)
	if e != nil {
		t.Fatal(e)
	}
	r := recall(t, b, `{"query":"compiler fixture schema repair missing argument","any_words":true,"limit":1,"selection":"relevant","decay":"none"}`)
	if len(r.Hits) != 1 || r.Hits[0].ID != eligible.ID || r.Matched != 1 {
		t.Fatal("scope applied after candidate limit", r)
	}
	after, _, e := h.cfg.Store.MemoryAccessOf(ref)
	if e != nil || after.Count != before.Count+1 {
		t.Fatal("ordinary AnyWords did not retain recall reinforcement", before, after, e)
	}
	for _, args := range []string{`{"query":"compiler","any_words":true,"exact":true}`, `{"any_words":true,"recent":true}`, fmt.Sprintf(`{"id":%q,"any_words":true}`, eligible.ID), `{"any_words":true}`} {
		wantResult(t, dispatch(t, b, memParams(opMemoryRecall, args)), statusDenied, reasonArgumentInvalid)
	}
}
