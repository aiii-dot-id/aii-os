package tools

import (
	"context"
	"testing"
)

// A host op executes like any other tool and appears in no list the
// identity or operator reads. Channel adapters are why: send resolves
// through the address book, and receive's output must pass
// internal/untrusted before it can reach a prompt. Advertising the raw
// methods beside the governed ones defeats both.

type namedTool struct{ n string }

func (t *namedTool) Name() string        { return t.n }
func (t *namedTool) Description() string { return "a fake operation" }
func (t *namedTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (t *namedTool) Execute(context.Context, map[string]interface{}) (Result, error) {
	return Result{Output: "ran " + t.n}, nil
}

func TestAHostOpIsExecutableAndInvisible(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	if err := r.RegisterHostOp(&namedTool{"pl_x_send"}, "org.example.x"); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterDynamic(&namedTool{"pl_x_memory_get"}, "org.example.x"); err != nil {
		t.Fatal(err)
	}

	// The host drives it.
	res, err := r.Execute(context.Background(), "pl_x_send", map[string]interface{}{})
	if err != nil || res.Output != "ran pl_x_send" {
		t.Fatalf("the host cannot call its own plumbing: %q %v", res.Output, err)
	}

	// Every surface the identity or operator reads derives from Names().
	for _, name := range r.Names() {
		if name == "pl_x_send" {
			t.Fatal("a host op reached Names() — the identity can see the raw pipe")
		}
	}
	for _, d := range r.ToolDefinitions() {
		fn := d.(map[string]interface{})["function"].(map[string]interface{})
		if fn["name"] == "pl_x_send" {
			t.Fatal("a host op reached the model's function list")
		}
	}
	for _, i := range r.Discover(3) {
		if i.Name == "pl_x_send" {
			t.Fatal("a host op reached the tools verb")
		}
	}
	for _, s := range r.ToolStates() {
		if s.Name == "pl_x_send" {
			t.Fatal("a host op reached the operator's toggle list")
		}
	}

	// An ordinary plugin tool is untouched: hiding is opt-in, not the
	// new default for everything a plugin brings.
	var sawOrdinary bool
	for _, name := range r.Names() {
		if name == "pl_x_memory_get" {
			sawOrdinary = true
		}
	}
	if !sawOrdinary {
		t.Fatal("an ordinary plugin tool disappeared along with the host ops")
	}
}

// Deactivation must forget the hidden mark too, or a plugin reinstalled
// under the same name inherits a decision nobody made for it.
func TestDeregisterForgetsThatItWasHidden(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	if err := r.RegisterHostOp(&namedTool{"pl_x_send"}, "org.example.x"); err != nil {
		t.Fatal(err)
	}
	r.Deregister("pl_x_send")
	if err := r.RegisterDynamic(&namedTool{"pl_x_send"}, "org.example.x"); err != nil {
		t.Fatal(err)
	}
	for _, name := range r.Names() {
		if name == "pl_x_send" {
			return
		}
	}
	t.Fatal("a name re-registered as an ordinary tool is still hidden")
}
