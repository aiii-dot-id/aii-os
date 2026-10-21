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

// A plugin's context provider reads while its plugin's primary operations
// are in the identity's tool list: none, or one the operator disabled, and
// it does not; the dispatch check asks again after the gate.
func TestContextSelectionAndDispatchRecheck(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	registerPlugin(t, r, "org.example.context", "detail")
	hook := &contextOp{ownedHostOp: ownedHostOp{namedTool{"pl_context_hook"}, new(int)}}
	if err := r.RegisterHostOp(hook, "org.example.context"); err != nil {
		t.Fatal(err)
	}
	if len(r.ContextProviders()) != 0 {
		t.Fatal("a plugin with no primary operation reads context")
	}
	names := registerPrimary(t, r, "org.example.context", "search", "store")
	if len(r.ContextProviders()) != 1 {
		t.Fatal("a plugin whose primaries are listed must read context")
	}
	r.SetToolEnabled(names[0], false)
	if len(r.ContextProviders()) != 0 {
		t.Fatal("a plugin with a disabled primary read context")
	}
	r.SetToolEnabled(names[0], true)
	hook.before = func() { r.SetToolEnabled(names[0], false) }
	_, err := r.Execute(WithContextSelection(WithDispatchOwner(t.Context(), hook.owner)), hook.Name(), nil)
	if err == nil {
		t.Fatal("a change after the gate was not checked")
	}
}

type contextMissingArgument struct{ contextOp }

func (*contextMissingArgument) Parameters() map[string]any {
	return map[string]any{"type": "object", "required": []string{"extra"}}
}
func TestAutomaticContextSchemaRefusalIsNotModelMalformedCount(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	registerPrimary(t, r, "org.example.context", "search")
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
