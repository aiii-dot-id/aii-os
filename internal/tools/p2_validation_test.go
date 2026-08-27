package tools

import (
	"context"
	"strings"
	"testing"
)

// missingArgTool advertises a required key its Execute never reads — the
// validation must fire before Execute is reached.
type missingArgTool struct{ called bool }

func (m *missingArgTool) Name() string        { return "probe_tool" }
func (m *missingArgTool) Description() string { return "test fixture" }
func (m *missingArgTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"target": map[string]interface{}{"type": "string"},
		},
		"required": []string{"target"},
	}
}
func (m *missingArgTool) Execute(ctx context.Context, args map[string]interface{}) (Result, error) {
	m.called = true
	return Result{Output: "ran"}, nil
}

// TestP2RequiredValidation: a call missing a schema-required argument is
// rejected at the registry gate with a named error, never dispatched, and
// counted as malformed (P3).
func TestP2RequiredValidation(t *testing.T) {
	r := NewRegistry("", nil, Timeouts{})
	m := &missingArgTool{}
	r.Register(m)

	res, err := r.Execute(context.Background(), "probe_tool", map[string]interface{}{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(res.Error, "malformed arguments") || !strings.Contains(res.Error, "target") {
		t.Fatalf("expected named missing-arg error, got: %q", res.Error)
	}
	if m.called {
		t.Fatal("tool must not run when required args are missing")
	}
	if r.MalformedCallCount() != 1 {
		t.Fatalf("malformed counter = %d, want 1", r.MalformedCallCount())
	}

	// Present key passes and is NOT counted.
	res, err = r.Execute(context.Background(), "probe_tool", map[string]interface{}{"target": "x"})
	if err != nil {
		t.Fatalf("valid call errored: %v", err)
	}
	if res.Output != "ran" {
		t.Fatalf("tool did not run on valid args: %+v", res)
	}
	if r.MalformedCallCount() != 1 {
		t.Fatalf("valid call changed counter: %d", r.MalformedCallCount())
	}

	// Present-but-nil counts as missing (the nil-args shape that started this).
	res, _ = r.Execute(context.Background(), "probe_tool", map[string]interface{}{"target": nil})
	if !strings.Contains(res.Error, "malformed arguments") || !strings.Contains(res.Error, "target") {
		t.Fatalf("nil required arg must be rejected, got: %+v", res)
	}
	if r.MalformedCallCount() != 2 {
		t.Fatalf("counter after nil-arg call = %d, want 2", r.MalformedCallCount())
	}
}

// freeTool advertises no required keys — validation must leave it untouched.
type freeTool struct{}

func (f *freeTool) Name() string        { return "free_tool" }
func (f *freeTool) Description() string { return "test fixture" }
func (f *freeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (f *freeTool) Execute(ctx context.Context, args map[string]interface{}) (Result, error) {
	return Result{Output: "ran"}, nil
}

// TestP2NoRequiredSchema: tools without a required list are untouched —
// validation adds no gate they never asked for.
func TestP2NoRequiredSchema(t *testing.T) {
	r := NewRegistry("", nil, Timeouts{})
	r.Register(&freeTool{})

	res, err := r.Execute(context.Background(), "free_tool", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(res.Output, "malformed") {
		t.Fatalf("free tool must not be validated: %+v", res)
	}
	if r.MalformedCallCount() != 0 {
		t.Fatalf("counter must stay zero: %d", r.MalformedCallCount())
	}
}
