package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/supervisor"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// unitSource is an admitted source as the tests of the call need one: the
// member the suite signs, on an activation that exists only to own the
// operation.
func unitSource(t *testing.T) (*Embeddings, *ActivePlugin) {
	t.Helper()
	decl, err := ParseEmbeddings([]byte(goodEmbeddings))
	if err != nil {
		t.Fatal(err)
	}
	ap := &ActivePlugin{ID: "org.example.vectors"}
	ap.Embeddings = &Embeddings{PluginID: ap.ID, Embed: "pl_org_example_vectors_embed", Decl: *decl, ModelSHA256: strings.Repeat("ab", 32)}
	return ap.Embeddings, ap
}

// standInOp is the source's operation as the registry holds it, answering
// what a test says. It is owned by an activation, as the real one is.
type standInOp struct {
	name  string
	owner *ActivePlugin
	run   func(ctx context.Context, args map[string]any) (tools.Result, error)
}

func (o *standInOp) Name() string               { return o.name }
func (o *standInOp) Description() string        { return "a source's operation" }
func (o *standInOp) Parameters() map[string]any { return map[string]any{"type": "object"} }
func (o *standInOp) DispatchOwner() any         { return o.owner }
func (o *standInOp) Execute(ctx context.Context, args map[string]any) (tools.Result, error) {
	return o.run(ctx, args)
}

// A SOURCE'S REPLY IS ONE VECTOR OF THE SIGNED LENGTH, OR IT IS REFUSED
// WHOLE. Each row is a reply a package could send. The refusals are typed,
// and no refusal repeats a word of the reply: the reason the layer is
// unavailable is read by a resident, and a reply is the one place a
// package that has been handed a memory could try to answer with text.
func TestASourcesReplyIsOneVectorOfTheSignedLengthOrRefusedWhole(t *testing.T) {
	e, _ := unitSource(t)
	if got, err := e.ReadReply(`{"vector":[0.6,0.8]}`); err != nil || len(got) != 2 || got[0] != float32(0.6) || got[1] != float32(0.8) {
		t.Fatalf("a vector of the signed length was not read: %v %v", got, err)
	}
	if got, err := e.ReadReply(` { "vector" : [ -1e-3 , 2 ] } `); err != nil || len(got) != 2 {
		t.Fatalf("white space and a negative number are a vector all the same: %v %v", got, err)
	}
	const marker = "DISREGARD-WHAT-CAME-BEFORE"
	refused := []struct{ name, reply, want string }{
		{"a second member", `{"vector":[0.6,0.8],"note":"` + marker + `"}`, "one member"},
		{"the member twice", `{"vector":[0.6,0.8],"vector":[0.8,0.6]}`, "one member"},
		{"the member under another case", `{"Vector":[0.6,0.8]}`, "one member"},
		{"text where the vector goes", `{"vector":"` + marker + `"}`, "one member"},
		{"a word among the numbers", `{"vector":[0.6,"` + marker + `"]}`, "one member"},
		{"a null among the numbers", `{"vector":[0.6,null]}`, "one member"},
		{"a list inside the list", `{"vector":[[0.6,0.8]]}`, "one member"},
		{"a bare list", `[0.6,0.8]`, "one member"},
		{"a bare string", `"` + marker + `"`, "one member"},
		{"nothing", ``, "one member"},
		{"content after the object", `{"vector":[0.6,0.8]} ` + marker, "one member"},
		{"a closer too many", `{"vector":[0.6,0.8]}}`, "one member"},
		{"null", `null`, "holds 0 numbers"},
		{"no vector", `{}`, "holds 0 numbers"},
		{"a null vector", `{"vector":null}`, "holds 0 numbers"},
		{"one number short", `{"vector":[0.6]}`, "holds 1 numbers"},
		{"one number over", `{"vector":[0.6,0.8,0.1]}`, "holds 3 numbers"},
		{"a number no vector can hold", `{"vector":[1e39,0.8]}`, "not finite"},
		{"a number no number can hold", `{"vector":[1e999,0.8]}`, "one member"},
		{"all zeros", `{"vector":[0,0]}`, "all zeros"},
		{"zeros too small to hold", `{"vector":[1e-60,-1e-60]}`, "all zeros"},
	}
	for _, c := range refused {
		got, err := e.ReadReply(c.reply)
		var typed *EmbedReplyError
		if got != nil || !errors.As(err, &typed) || typed.PluginID != e.PluginID {
			t.Errorf("%s: want the typed refusal and no vector, got %v %v", c.name, got, err)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: refused as %q, want it to say %q", c.name, err, c.want)
		}
		if strings.Contains(err.Error(), marker) {
			t.Errorf("%s: the refusal repeats the reply's own text: %q", c.name, err)
		}
	}
}

// EVERY WAY A CALL CAN FAIL IS THE HOST'S SENTENCE, AND NONE CARRIES WHAT
// THE PACKAGE SAID. The message of an error the package answered with and
// the last lines its child wrote before dying are both the package's
// words, and the supervisor's own error for a dead child quotes the
// second. Each row makes the call fail one way and looks for the marker
// in the sentence a resident would read; what was said is kept on the
// typed error for the log, and only there.
func TestACallThatFailsSaysTheHostsSentenceAndNothingThePackageSaid(t *testing.T) {
	const marker = "DISREGARD-WHAT-CAME-BEFORE"
	cases := []struct {
		name string
		run  func(ctx context.Context, args map[string]any) (tools.Result, error)
		kind string
		said bool
	}{
		{"an error the package answered with", func(context.Context, map[string]any) (tools.Result, error) {
			return tools.Result{Error: "plugin error -32000: " + marker, ReasonCode: marker}, nil
		}, EmbedPluginError, true},
		{"an error with no code", func(context.Context, map[string]any) (tools.Result, error) {
			return tools.Result{Error: "plugin error -32000: " + marker}, nil
		}, EmbedPluginError, true},
		{"a child that died talking", func(context.Context, map[string]any) (tools.Result, error) {
			return tools.Result{}, &supervisor.ChildExitError{PluginID: "org.example.vectors", Code: 9, Phase: "invoke", StderrTail: []string{marker}}
		}, EmbedChildFailed, true},
		{"a call past its deadline", func(ctx context.Context, _ map[string]any) (tools.Result, error) {
			<-ctx.Done()
			return tools.Result{}, &supervisor.InvokeTimeoutError{PluginID: "org.example.vectors", Err: ctx.Err()}
		}, EmbedTimedOut, false},
		{"a call that never got its turn", func(ctx context.Context, _ map[string]any) (tools.Result, error) {
			<-ctx.Done()
			return tools.Result{}, ctx.Err()
		}, EmbedNotAsked, false},
		{"a wait for its turn that ran out of time", func(ctx context.Context, _ map[string]any) (tools.Result, error) {
			<-ctx.Done()
			return tools.Result{Error: "the call was cancelled while waiting for this plugin's turn; nothing ran", ReasonCode: ReasonCancelledBeforeDispatch}, nil
		}, EmbedNotAsked, false},
		{"a release withdrawn meanwhile", func(context.Context, map[string]any) (tools.Result, error) {
			return tools.Refusal(ReasonActivationWithdrawn, "withdrawn"), nil
		}, EmbedReplaced, false},
		{"SAFE, said by the host", func(context.Context, map[string]any) (tools.Result, error) {
			return tools.Refusal(tools.ReasonSafeSuspended, "refused: I am in safe mode"), nil
		}, EmbedSafe, false},
		{"turned off, said by the host", func(context.Context, map[string]any) (tools.Result, error) {
			return tools.Refusal(tools.ReasonOperatorDisabled, "access denied: tool disabled by operator"), nil
		}, EmbedDisabled, false},
		{"another refusal of the host's", func(context.Context, map[string]any) (tools.Result, error) {
			return tools.Refusal(ReasonOperatorGrant, "the operator granted this plugin read only"), nil
		}, EmbedRefused, false},
		{"a result the host could not take", func(context.Context, map[string]any) (tools.Result, error) {
			return tools.Result{Error: "result refused: " + marker, ReasonCode: ReasonResultRefused}, nil
		}, EmbedPluginError, true},
		// A REASON CODE IS A FIELD THE PACKAGE FILLS IN TOO. Each of these
		// is the package answering with one of the host's own codes: it
		// must not thereby choose which of the host's sentences a resident
		// reads ("no plugin is asked while the identity is in SAFE", when it
		// is not).
		{"the package says SAFE", func(context.Context, map[string]any) (tools.Result, error) {
			return tools.Result{Error: "operation failed: " + marker, ReasonCode: tools.ReasonSafeSuspended}, nil
		}, EmbedPluginError, true},
		{"the package says the operator turned it off", func(context.Context, map[string]any) (tools.Result, error) {
			return tools.Result{Error: "operation failed: " + marker, ReasonCode: tools.ReasonOperatorDisabled}, nil
		}, EmbedPluginError, true},
		{"the package says it was replaced", func(context.Context, map[string]any) (tools.Result, error) {
			return tools.Result{Error: "operation failed: " + marker, ReasonCode: "HOST_OWNER_CHANGED"}, nil
		}, EmbedPluginError, true},
		{"the package says the wait was cancelled, with time left", func(context.Context, map[string]any) (tools.Result, error) {
			return tools.Result{Error: "operation failed: " + marker, ReasonCode: ReasonCancelledBeforeDispatch}, nil
		}, EmbedPluginError, true},
	}
	for _, c := range cases {
		e, ap := unitSource(t)
		reg := newRegistry(t)
		if err := reg.RegisterHostOp(&standInOp{name: e.Embed, owner: ap, run: c.run}, ap.ID); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		got, err := e.Ask(ctx, reg, ap, EmbedKindCue, "where is the harbor")
		cancel()
		var typed *EmbedCallError
		if got != nil || !errors.As(err, &typed) || typed.Kind != c.kind {
			t.Errorf("%s: want the typed failure %s and no vector, got %v %v", c.name, c.kind, got, err)
			continue
		}
		if strings.Contains(err.Error(), marker) {
			t.Errorf("%s: the sentence carries the package's words: %q", c.name, err)
		}
		if c.said && !strings.Contains(typed.Said, marker) {
			t.Errorf("%s: what was said is not kept for the log: %+v", c.name, typed)
		}
	}
}

// THE CALL IS PINNED TO THE ACTIVATION THE SOURCE WAS ADMITTED ON. An
// update takes the operation's name for a new release, which may name
// another model. A call made for the old activation must then reach
// nothing: its vector would be stamped with a basis that did not make it.
// And what the host hands over is exactly the kind and the text.
func TestTheCallReachesOnlyTheActivationItWasMadeFor(t *testing.T) {
	e, ap := unitSource(t)
	reg := newRegistry(t)
	successor := &ActivePlugin{ID: ap.ID}
	var seen []map[string]any
	if err := reg.RegisterHostOp(&standInOp{name: e.Embed, owner: successor, run: func(_ context.Context, args map[string]any) (tools.Result, error) {
		seen = append(seen, args)
		return tools.Result{Output: `{"vector":[0.6,0.8]}`}, nil
	}}, ap.ID); err != nil {
		t.Fatal(err)
	}
	got, err := e.Ask(context.Background(), reg, ap, EmbedKindCue, "where is the harbor")
	var typed *EmbedCallError
	if got != nil || !errors.As(err, &typed) || typed.Kind != EmbedReplaced || len(seen) != 0 {
		t.Fatalf("a call for a replaced activation must reach nothing: %v %v, the successor saw %d calls", got, err, len(seen))
	}
	got, err = e.Ask(context.Background(), reg, successor, EmbedKindMemory, "the ship reached the harbor at dusk")
	if err != nil || len(got) != 2 || len(seen) != 1 {
		t.Fatalf("the call for the activation that holds the operation: %v %v", got, err)
	}
	if len(seen[0]) != 2 || seen[0]["kind"] != EmbedKindMemory || seen[0]["text"] != "the ship reached the harbor at dusk" {
		t.Fatalf("the source was handed %v, want the kind and the text and nothing else", seen[0])
	}
}

// AN INSTALL IS TOLD FROM ITS PUBLISHER'S VECTORS BY ITS PROBES, AND A
// MISS IS NAMED BY NUMBER. Every probe is asked, in the member's order,
// with its own kind and text; the first that is not answered or does not
// agree ends the check.
func TestAnInstallThatDoesNotAgreeWithItsProbesIsNamedByProbe(t *testing.T) {
	e, _ := unitSource(t)
	signed := func(_ context.Context, kind, text string) ([]float32, error) {
		for _, p := range e.Decl.Probes {
			if p.Kind == kind && p.Text == text {
				return []float32{float32(p.Vector[0]), float32(p.Vector[1])}, nil
			}
		}
		return nil, fmt.Errorf("asked for a text that is no probe: %s %q", kind, text)
	}
	if err := e.Probe(context.Background(), signed); err != nil {
		t.Fatalf("an install that returns what was signed: %v", err)
	}

	// The second probe, a memory, computed another way: 0.8 of what was
	// signed, under the 0.95 the member asks.
	var asked int
	broken := func(ctx context.Context, kind, text string) ([]float32, error) {
		asked++
		if kind == EmbedKindMemory {
			return []float32{1, 0}, nil
		}
		return signed(ctx, kind, text)
	}
	err := e.Probe(context.Background(), broken)
	var miss *ProbeError
	if !errors.As(err, &miss) || miss.Probe != 2 || miss.Kind != EmbedKindMemory || miss.Err != nil || math.Abs(miss.Cosine-0.8) > 1e-6 || miss.Bound != 0.95 {
		t.Fatalf("want probe 2 named, a memory at 0.8 under 0.95, got %v (%+v)", err, miss)
	}
	if asked != 2 || !strings.Contains(err.Error(), "probe 2") {
		t.Fatalf("the check asked %d probes and said %q", asked, err)
	}

	// A probe that is not answered at all ends it too, with why.
	down := errors.New("the child is gone")
	err = e.Probe(context.Background(), func(context.Context, string, string) ([]float32, error) { return nil, down })
	if !errors.As(err, &miss) || miss.Probe != 1 || !errors.Is(err, down) {
		t.Fatalf("an unanswered probe must be named with its cause: %v", err)
	}

	// A vector of another length agrees with nothing.
	err = e.Probe(context.Background(), func(context.Context, string, string) ([]float32, error) { return []float32{0.6, 0.8, 0}, nil })
	if !errors.As(err, &miss) || miss.Probe != 1 || miss.Cosine != 0 {
		t.Fatalf("a vector of another length must miss: %v", err)
	}
}

// WHO MADE A KEPT VECTOR IS READ FROM ITS BASIS. The host stamps a
// source's basis itself, so its shape names the package whether or not
// that package is running; any other basis is a provider's. A package
// that made the vectors and has stopped must still be known as their
// maker, or the record would pass to whatever else is there.
func TestWhoMadeAKeptVectorIsReadFromItsBasis(t *testing.T) {
	e, _ := unitSource(t)
	if id, ok := SourceOfBasis(e.Basis()); !ok || id != e.PluginID {
		t.Fatalf("the basis the host stamps, %q, was not read back as its package's: %q %v", e.Basis(), id, ok)
	}
	for basis, want := range map[string]string{
		"org.example.vectors/0123456789ab/f1":     "org.example.vectors",
		"org.example.vectors/0123456789ab/f65535": "org.example.vectors",
		"a/0123456789ab/f1":                       "a",
		"Local/bge-m3":                            "",
		"Local/BAAI/bge-m3":                       "",
		"org.example.vectors/bge-m3":              "",
		"org.example.vectors/0123456789ab":        "",
		"org.example.vectors/0123456789ab/f":      "",
		"org.example.vectors/0123456789ab/1":      "",
		"org.example.vectors/0123456789AB/f1":     "",
		"org.example.vectors/0123456789a/f1":      "",
		"org.example.vectors/0123456789abc/f1":    "",
		"Org.Example/0123456789ab/f1":             "",
		"/0123456789ab/f1":                        "",
		"org.example.vectors/0123456789ab/f1/x":   "",
		"":                                        "",
	} {
		if id, ok := SourceOfBasis(basis); id != want || ok != (want != "") {
			t.Errorf("basis %q read as %q %v, want %q", basis, id, ok, want)
		}
	}
}

// A PROBE EXACTLY AT THE BOUND AGREES, and one that is not a number does
// not. The bound is the least agreement the package asks of itself, so
// meeting it is enough; and a cosine that is not a number is under no
// bound and over none.
func TestAProbeAtTheBoundAgreesAndOneThatIsNoNumberDoesNot(t *testing.T) {
	e, _ := unitSource(t)
	e.Decl.ProbeBound = 1
	e.Decl.Probes = []EmbeddingProbe{{Kind: EmbedKindCue, Text: "a", Vector: ProbeVector{1, 0}}}
	if err := e.Probe(context.Background(), func(context.Context, string, string) ([]float32, error) { return []float32{1, 0}, nil }); err != nil {
		t.Fatalf("a probe that meets its bound exactly was refused: %v", err)
	}
	nan := float32(math.NaN())
	var miss *ProbeError
	if err := e.Probe(context.Background(), func(context.Context, string, string) ([]float32, error) { return []float32{nan, 0}, nil }); !errors.As(err, &miss) {
		t.Fatalf("a vector that is not numbers passed its probe: %v", err)
	}
}
