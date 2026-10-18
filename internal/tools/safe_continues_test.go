package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

func TestSafesAnswerForEveryRegisteredToolIsUnchanged(t *testing.T) {
	r, home, _ := writeHome(t, map[string]string{"a.txt": "alpha"})
	if err := r.RegisterDynamic(&namedTool{"pl_x_lookup"}, "org.example.x"); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterHostOp(&namedTool{"pl_x_send"}, "org.example.x"); err != nil {
		t.Fatal(err)
	}
	r.SetSafeSource(func() (string, bool) { return "the record did not verify", true })
	file := filepath.Join(home, "a.txt")
	args := map[string]map[string]any{
		"read":        {"file_path": file},
		"grep":        {"pattern": "alpha"},
		"ls":          {},
		"write":       {"file_path": filepath.Join(home, "b.txt"), "content": "x"},
		"edit":        {"file_path": file, "old_string": "alpha", "new_string": "beta"},
		"shell":       {"command": "echo ran"},
		"web_fetch":   {"url": "http://127.0.0.1:1/"},
		"pl_x_lookup": {},
		"pl_x_send":   {},
	}
	refused := map[string]bool{
		"read": false, "grep": false, "ls": false,
		"write": true, "edit": true, "shell": true, "web_fetch": true,
		"pl_x_lookup": true, "pl_x_send": true,
	}
	for _, name := range r.Names() {
		if _, known := refused[name]; !known {
			t.Errorf("%s is registered and this table has no answer for it", name)
		}
	}
	for _, name := range []string{"read", "grep", "ls", "write", "edit", "shell", "web_fetch", "pl_x_lookup", "pl_x_send"} {
		t.Run(name, func(t *testing.T) {
			if _, ok := r.Get(name); !ok {
				t.Skipf("%s is not on this host", name)
			}
			res, err := r.Execute(context.Background(), name, args[name])
			if err != nil {
				t.Fatal(err)
			}
			if got := res.ReasonCode == ReasonSafeSuspended; got != refused[name] {
				t.Fatalf("SAFE refused %s: %v, want %v (%+v)", name, got, refused[name], res)
			}
			if refused[name] && (!res.Refused || res.Output != "") {
				t.Fatalf("SAFE's refusal of %s ran something or was not a refusal: %+v", name, res)
			}
		})
	}
	if b, err := os.ReadFile(file); err != nil || string(b) != "alpha" {
		t.Fatalf("a refused edit changed the file: %q %v", b, err)
	}
	// A name nobody registered is still answered as unknown, not as SAFE.
	if _, err := r.Execute(context.Background(), "nobody_registered_this", nil); !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("an unknown name in SAFE: want ErrUnknownTool, got %v", err)
	}
}

// lookupTool is a builtin added tomorrow: it only reads (declares itself
// ReadOnly, so it may run beside other reads), and it reads the world
// outside the identity — the case a SAFE keyed on ReadOnlyTool would
// have let through.
type lookupTool struct{ ran *int }

func (t lookupTool) Name() string        { return "lookup" }
func (t lookupTool) Description() string { return "looks something up on the network" }
func (t lookupTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t lookupTool) ReadOnly() bool { return true }
func (t lookupTool) Execute(context.Context, map[string]any) (Result, error) {
	*t.ran++
	return Result{Output: "looked up"}, nil
}

// A BUILTIN NOBODY NAMED FOR SAFE IS REFUSED IN SAFE: registered through
// the builtin door, declaring itself read-only, it still does not run
// while SAFE holds — it runs again when SAFE ends.
func TestANewBuiltinIsRefusedInSafeUntilNamed(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	ran := 0
	r.Register(lookupTool{ran: &ran})
	if !r.ParallelSafe("lookup") {
		t.Fatal("the fixture must declare itself read-only, or it proves nothing about ReadOnlyTool")
	}
	safe := true
	r.SetSafeSource(func() (string, bool) { return "the record did not verify", safe })
	res, err := r.Execute(context.Background(), "lookup", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ran != 0 || res.ReasonCode != ReasonSafeSuspended || !res.Refused {
		t.Fatalf("SAFE must refuse a builtin it does not name: ran %d, %+v", ran, res)
	}
	safe = false
	if res, err := r.Execute(context.Background(), "lookup", nil); err != nil || res.Output != "looked up" || ran != 1 {
		t.Fatalf("out of SAFE the builtin runs: ran %d, %+v %v", ran, res, err)
	}
}

// SAFE ASKS THE ORIGIN AS WELL AS THE NAME: a plugin's operation is
// suspended whatever it is called, a name SAFE continues included. No
// stock registry lets a plugin hold read, grep or ls — the builtins hold
// them first and a name is never replaced — so this registry is bare.
func TestSafeRefusesAPluginsOperationWhateverItsName(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := &Registry{tools: map[string]Tool{}, sources: map[string]string{}, disabled: map[string]bool{},
		policy: firewall.DefaultPolicy(), sandbox: home}
	if err := r.RegisterDynamic(&namedTool{"grep"}, "org.example.x"); err != nil {
		t.Fatal(err)
	}
	safe := true
	r.SetSafeSource(func() (string, bool) { return "the record did not verify", safe })
	if res, err := r.Execute(context.Background(), "grep", map[string]any{}); err != nil || res.ReasonCode != ReasonSafeSuspended || res.Output != "" {
		t.Fatalf("SAFE must suspend a plugin's operation named grep: %+v %v", res, err)
	}
	safe = false
	if res, err := r.Execute(context.Background(), "grep", map[string]any{}); err != nil || res.Output != "ran grep" {
		t.Fatalf("out of SAFE the operation runs: %+v %v", res, err)
	}
}
