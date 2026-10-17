package tools

import (
	"context"
	"testing"
)

type contextOp struct {
	ownedHostOp
	before func()
}

func (*contextOp) ContextProvider() bool { return true }
func (p *contextOp) Execute(ctx context.Context, args map[string]any) (Result, error) {
	if p.before != nil {
		p.before()
	}
	if err := CheckDispatch(ctx); err != nil {
		return Result{}, err
	}
	return Result{Output: `{"text":"x","detail_route":"search"}`}, nil
}
func TestContextSelectionAndDispatchRecheck(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	names := registerPlugin(t, r, "org.example.context", "search", "detail")
	hook := &contextOp{ownedHostOp: ownedHostOp{namedTool{"pl_context_hook"}, new(int)}}
	if err := r.RegisterHostOp(hook, "org.example.context"); err != nil {
		t.Fatal(err)
	}
	if len(r.ContextProviders()) != 0 {
		t.Fatal("unselected context")
	}
	if _, err := r.Offer(names[0]); err != nil {
		t.Fatal(err)
	}
	if len(r.ContextProviders()) != 1 {
		t.Fatal("selected context absent")
	}
	r.SetToolEnabled(names[0], false)
	if len(r.ContextProviders()) != 0 {
		t.Fatal("disabled selection admitted")
	}
	r.SetToolEnabled(names[0], true)
	if _, err := r.Offer(names[1]); err != nil {
		t.Fatal(err)
	}
	r.regMu.Lock()
	delete(r.offered, names[1])
	r.regMu.Unlock()
	if len(r.ContextProviders()) != 0 {
		t.Fatal("withheld selection admitted")
	}
	r.regMu.Lock()
	r.offered[names[1]] = true
	r.regMu.Unlock()
	hook.before = func() { r.SetToolEnabled(names[0], false) }
	_, err := r.Execute(WithContextSelection(WithDispatchOwner(t.Context(), hook.owner)), hook.Name(), nil)
	if err == nil {
		t.Fatal("selection change after gate not checked")
	}
}

type contextMissingArgument struct{ contextOp }

func (*contextMissingArgument) Parameters() map[string]any {
	return map[string]any{"type": "object", "required": []string{"extra"}}
}
func TestAutomaticContextSchemaRefusalIsNotModelMalformedCount(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	names := registerPlugin(t, r, "org.example.context", "search")
	if _, e := r.Offer(names[0]); e != nil {
		t.Fatal(e)
	}
	hook := &contextMissingArgument{contextOp{ownedHostOp: ownedHostOp{namedTool{"pl_context_hook"}, new(int)}}}
	if e := r.RegisterHostOp(hook, "org.example.context"); e != nil {
		t.Fatal(e)
	}
	before := r.malformedCalls.Load()
	result, e := r.Execute(WithContextSelection(WithDispatchOwner(t.Context(), hook.owner)), hook.Name(), nil)
	if e != nil || result.ReasonCode != ReasonArgumentsRequired || r.malformedCalls.Load() != before {
		t.Fatal(result, e, r.malformedCalls.Load(), before)
	}
}
