package tools

import (
	"context"
	"testing"
)

// .
// .
// .
// .
// .

type declaredTool struct {
	name string
	safe bool
}

func (d *declaredTool) Name() string                       { return d.name }
func (d *declaredTool) Description() string                { return "" }
func (d *declaredTool) Parameters() map[string]interface{} { return map[string]interface{}{} }
func (d *declaredTool) Execute(context.Context, map[string]interface{}) (Result, error) {
	return Result{}, nil
}
func (d *declaredTool) ReplaySafe() bool { return d.safe }

func TestReplaySafetyIsDeclaredNeverGuessed(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	for _, name := range []string{"read", "grep", "ls"} {
		if !r.ReplaySafe(name) {
			t.Errorf("%s declares itself read-only and may be repeated", name)
		}
	}
	for _, name := range []string{"write", "edit", "shell", "web_fetch", "work", "tools", "no_such_tool", ""} {
		if r.ReplaySafe(name) {
			t.Errorf("%q was licensed for replay: nothing it declares says it is safe", name)
		}
	}
	// .
	// .
	_ = r.RegisterDynamic(&declaredTool{name: "pl_org_example_get", safe: false}, "org.example")
	_ = r.RegisterDynamic(&declaredTool{name: "pl_org_example_purge", safe: true}, "org.example")
	if r.ReplaySafe("pl_org_example_get") {
		t.Error("a name ending in _get was taken for a read")
	}
	if !r.ReplaySafe("pl_org_example_purge") {
		t.Error("an operation that declares itself safe to repeat was refused on its name")
	}
	// .
	// .
	_ = r.RegisterDynamic(&namedTool{n: "pl_org_example_silent"}, "org.example")
	if r.ReplaySafe("pl_org_example_silent") {
		t.Error("a tool that declares nothing was licensed")
	}
}
