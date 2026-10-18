package tools

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

type addressedHostOp struct{ namedTool }

func (t *addressedHostOp) Parameters() map[string]any {
	return map[string]any{"type": "object", "required": []string{"address"}}
}

func TestModelHostOpRefusalIsTypedAndDoesNotBreakHostCalls(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	const name = "pl_x_send"
	if err := r.RegisterHostOp(&addressedHostOp{namedTool{name}}, "org.example.x"); err != nil {
		t.Fatal(err)
	}
	for _, args := range []map[string]any{{}, {"address": "@stranger"}} {
		res, err := r.ExecuteModel(t.Context(), name, args)
		if err != nil || res.ReasonCode != ReasonHostOnly || !strings.Contains(res.Error, "host-only") || res.Output != "" {
			t.Fatalf("model reached host operation or got an untyped refusal: %+v %v", res, err)
		}
	}
	res, err := r.Execute(t.Context(), name, map[string]any{"address": "@known"})
	if err != nil || res.Error != "" || res.Output != "ran "+name {
		t.Fatalf("host invocation failed: %+v %v", res, err)
	}
	r.Deregister(name)
	if err := r.RegisterDynamic(&namedTool{name}, "org.example.x"); err != nil {
		t.Fatal(err)
	}
	res, err = r.ExecuteModel(t.Context(), name, nil)
	if err != nil || res.Error != "" || res.Output != untrusted.Wrap("plugin org.example.x", "ran "+name) {
		t.Fatalf("retired host mark blocked an ordinary replacement: %+v %v", res, err)
	}
	if err := r.SupersedeOrigin("org.example.x", []Tool{&addressedHostOp{namedTool{name}}}, map[string]bool{name: true}); err != nil {
		t.Fatal(err)
	}
	res, err = r.ExecuteModel(t.Context(), name, map[string]any{"address": "@stranger"})
	if err != nil || res.ReasonCode != ReasonHostOnly {
		t.Fatalf("plugin update did not restore the host-only boundary: %+v %v", res, err)
	}
	if err := r.SupersedeOrigin("org.example.x", []Tool{&namedTool{name}}, nil); err != nil {
		t.Fatal(err)
	}
	res, err = r.ExecuteModel(t.Context(), name, nil)
	if err != nil || res.Error != "" || res.Output != untrusted.Wrap("plugin org.example.x", "ran "+name) {
		t.Fatalf("ordinary replacement stayed host-only: %+v %v", res, err)
	}
}

// A host op executes like any other tool and appears in no list the
// identity or operator reads. Channel adapters are why: send resolves
// through the address book, and receive's output must pass
// internal/untrusted before it can reach a prompt. Advertising the raw
// methods beside the governed ones defeats both.

type namedTool struct{ n string }

func (t *namedTool) Name() string        { return t.n }
func (t *namedTool) Description() string { return "a fake operation" }
func (t *namedTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *namedTool) Execute(context.Context, map[string]any) (Result, error) {
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
	res, err := r.Execute(context.Background(), "pl_x_send", map[string]any{})
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
		if d.Function.Name == "pl_x_send" {
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

type ownedHostOp struct {
	namedTool
	owner *int
}

func (t *ownedHostOp) DispatchOwner() any { return t.owner }
func TestHostDispatchBindsExactOwner(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	first, next := new(int), new(int)
	name := "pl_owner_observe"
	if e := r.RegisterHostOp(&ownedHostOp{namedTool{name}, next}, "owner"); e != nil {
		t.Fatal(e)
	}
	res, e := r.Execute(WithDispatchOwner(t.Context(), first), name, nil)
	if e != nil || res.ReasonCode != "HOST_OWNER_CHANGED" {
		t.Fatal(res, e)
	}
	res, e = r.Execute(WithDispatchOwner(t.Context(), next), name, nil)
	if e != nil || res.Error != "" {
		t.Fatal(res, e)
	}
}

func TestAHostOpIsNeverSeatedEvenForAMoment(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	op := &namedTool{n: "pl_org_example_mail_send"}
	r.SetStandingOffer([]StandingSeat{{Name: op.Name(), Print: Fingerprint(op)}}, nil)
	var seen atomic.Bool
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			for _, n := range r.PromptNames() {
				if n == op.Name() {
					seen.Store(true)
				}
			}
		}
	}()
	for i := 0; i < 5000 && !seen.Load(); i++ {
		if err := r.RegisterHostOp(op, "org.example.mail"); err != nil {
			t.Fatal(err)
		}
		r.Deregister(op.Name())
	}
	close(stop)
	<-done
	if seen.Load() {
		t.Fatal("a host op that a standing seat named appeared in the identity's tool list")
	}
}
