package tools

import (
	"context"
	"strings"
	"testing"
)

type malformedRequiredTool struct{}

func (malformedRequiredTool) Name() string        { return "malformed" }
func (malformedRequiredTool) Description() string { return "a tool with a corrupt required list" }
func (malformedRequiredTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "required": []any{"key", 7}}
}
func (malformedRequiredTool) Execute(context.Context, map[string]any) (Result, error) {
	return Result{Output: "proceeded"}, nil
}

// Two required keys, both missing: the message names both, sorted.
type twoRequiredTool struct{}

func (twoRequiredTool) Name() string        { return "pair" }
func (twoRequiredTool) Description() string { return "needs two arguments" }
func (twoRequiredTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "required": []string{"zebra", "apple"}}
}
func (twoRequiredTool) Execute(context.Context, map[string]any) (Result, error) {
	return Result{Output: "proceeded"}, nil
}

func TestAMalformedRequiredListRefusesTheCallInsteadOfSkippingMembers(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	r.Register(malformedRequiredTool{})
	res, err := r.Execute(context.Background(), "malformed", map[string]any{"key": "k"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Error, "malformed tool schema") || !strings.Contains(res.Error, "required[1]") {
		t.Fatalf("a corrupt required list must refuse the call naming the member, got %+v", res)
	}
	if res.Output == "proceeded" {
		t.Fatal("the call reached the tool past a malformed schema")
	}
}

func TestMissingRequiredNamesEveryMissingKeySorted(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	r.Register(twoRequiredTool{})
	before := r.MalformedCallCount()
	res, err := r.Execute(context.Background(), "pair", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Error, "missing required apple, zebra") {
		t.Fatalf("both missing keys must be named in sorted order, got %q", res.Error)
	}
	if r.MalformedCallCount() != before+1 {
		t.Fatalf("one rejected call must count once, got %d", r.MalformedCallCount()-before)
	}
	res, _ = r.Execute(context.Background(), "pair", map[string]any{"apple": 1, "zebra": 2})
	if res.Error != "" || r.MalformedCallCount() != before+1 {
		t.Fatalf("a complete call must proceed and not count: %+v count=%d", res, r.MalformedCallCount()-before)
	}
}
