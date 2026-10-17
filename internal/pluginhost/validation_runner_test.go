//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// clockStep is a deterministic clock that moves only when a lane says the
// plugin took time.
type clockStep struct{ now time.Time }

func (c *clockStep) Now() time.Time       { return c.now }
func (c *clockStep) take(d time.Duration) { c.now = c.now.Add(d) }
func readChecks(t *testing.T, file string) []packagefmt.Check {
	t.Helper()
	v, err := packagefmt.ParseValidation([]byte(file), func(rel string) ([]byte, bool, error) {
		if rel == "embeddings.json" {
			return []byte(`{"probes":[{"kind":"cue","text":"where is the harbor","vector":[0.6,0.8]}],"probe_bound":0.95}`), true, nil
		}
		return nil, false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return v.Checks
}

// EACH LANE IS JUDGED BY ITS EXPECTATION, and a call that brought nothing
// back says nothing of the set. Every row is one check sent once.
func TestTheRunnerJudgesEachLaneByItsExpectation(t *testing.T) {
	vectorCheck := `{"checks":[{"name":"embeds the cue","sets":["*"],"lane":"embeddings","input":{"kind":"cue","text":{"$ref":"embeddings.json#/probes/0/text"}},"expect":{"vector_cosine_at_least":{"reference":{"$ref":"embeddings.json#/probes/0/vector"},"min":{"$ref":"embeddings.json#/probe_bound"}}},"within_ms":5000}]}`
	opCheck := func(expect string) string {
		return `{"checks":[{"name":"looks it up","sets":["*"],"lane":"operation","call":{"operation":"lookup","arguments":{"q":"harbor"}},"expect":` + expect + `,"within_ms":5000}]}`
	}
	for _, row := range []struct {
		name   string
		file   string
		embed  func() ([]float32, error)
		answer func() (tools.Result, error)
		want   pluginfacility.CheckOutcome
		words  string
	}{
		{name: "the vector its publisher signed", file: vectorCheck, embed: func() ([]float32, error) { return []float32{0.6, 0.8}, nil }, want: pluginfacility.CheckPassed},
		{name: "another vector", file: vectorCheck, embed: func() ([]float32, error) { return []float32{1, 0}, nil }, want: pluginfacility.CheckWrong, words: "came back at 0.600 of its reference, under the 0.950 it asks"},
		{name: "something that is no vector", file: vectorCheck, embed: func() ([]float32, error) {
			return nil, &EmbedReplyError{PluginID: "p", Detail: "the reply holds 3 numbers"}
		}, want: pluginfacility.CheckWrong, words: "not a vector"},
		{name: "past the call's deadline", file: vectorCheck, embed: func() ([]float32, error) { return nil, &EmbedCallError{PluginID: "p", Kind: EmbedTimedOut} }, want: pluginfacility.CheckTimedOut},
		{name: "a child that is gone", file: vectorCheck, embed: func() ([]float32, error) { return nil, &EmbedCallError{PluginID: "p", Kind: EmbedChildFailed} }, want: pluginfacility.CheckUnanswered},
		{name: "a result that holds the word", file: opCheck(`{"result_contains":["harbor","dusk"]}`), answer: func() (tools.Result, error) { return tools.Result{Output: `{"text":"the harbor at dusk"}`}, nil }, want: pluginfacility.CheckPassed},
		{name: "a result that lacks one", file: opCheck(`{"result_contains":["harbor","dawn"]}`), answer: func() (tools.Result, error) { return tools.Result{Output: `{"text":"the harbor at dusk"}`}, nil }, want: pluginfacility.CheckWrong, words: `lacks "dawn"`},
		{name: "a value that equals", file: opCheck(`{"json":{"path":"/hits/0/n","equals":3}}`), answer: func() (tools.Result, error) { return tools.Result{Output: `{"hits":[{"n":3}]}`}, nil }, want: pluginfacility.CheckPassed},
		{name: "a value that does not", file: opCheck(`{"json":{"path":"/hits/0/n","equals":3}}`), answer: func() (tools.Result, error) { return tools.Result{Output: `{"hits":[{"n":4}]}`}, nil }, want: pluginfacility.CheckWrong, words: "is 4, not 3"},
		{name: "a score at least", file: opCheck(`{"json":{"path":"/score","at_least":0.5}}`), answer: func() (tools.Result, error) { return tools.Result{Output: `{"score":0.5}`}, nil }, want: pluginfacility.CheckPassed},
		{name: "a score under", file: opCheck(`{"json":{"path":"/score","at_least":0.5}}`), answer: func() (tools.Result, error) { return tools.Result{Output: `{"score":0.4}`}, nil }, want: pluginfacility.CheckWrong, words: "under 0.5"},
		{name: "an error in place of a result", file: opCheck(`{"result_contains":"harbor"}`), answer: func() (tools.Result, error) { return tools.Result{Error: "plugin error -32000: no index"}, nil }, want: pluginfacility.CheckWrong, words: "no index"},
		{name: "a reply that is no response", file: opCheck(`{"result_contains":"harbor"}`), answer: func() (tools.Result, error) {
			return tools.Result{}, &ResponseContractError{Requirement: "exactly one of result|error"}
		}, want: pluginfacility.CheckWrong},
		{name: "the host refused before anything ran", file: opCheck(`{"result_contains":"harbor"}`), answer: func() (tools.Result, error) { return tools.Refusal(tools.ReasonSafeSuspended, "SAFE"), nil }, want: pluginfacility.CheckUnanswered},
		{name: "past the invocation's deadline", file: opCheck(`{"result_contains":"harbor"}`), answer: func() (tools.Result, error) {
			return tools.Result{}, &supervisor.InvokeTimeoutError{PluginID: "p", Err: context.DeadlineExceeded}
		}, want: pluginfacility.CheckTimedOut},
	} {
		t.Run(row.name, func(t *testing.T) {
			lanes := CheckLanes{
				Embed: func(context.Context, string, string) ([]float32, error) { return row.embed() },
				Operation: func(_ context.Context, op string, args map[string]any) (tools.Result, error) {
					if op != "lookup" || args["q"] != "harbor" {
						t.Fatalf("the call was %s %v", op, args)
					}
					return row.answer()
				},
			}
			got := RunChecks(t.Context(), readChecks(t, row.file), lanes, nil)
			if len(got) != 1 || got[0].Outcome != row.want || !strings.Contains(got[0].Words, row.words) {
				t.Fatalf("got %+v, want %s saying %q", got, row.want, row.words)
			}
		})
	}
}

// A CHECK PAST ITS OWN BOUND IS A TIMEOUT; A WRONG ANSWER IS WRONG AT ANY
// SPEED. The bound is measured from send to reply on the runner's clock,
// and the call is never cut at it.
func TestACheckPastItsBoundIsATimeoutAndAWrongAnswerIsWrongAtAnySpeed(t *testing.T) {
	checks := readChecks(t, `{"checks":[
		{"name":"right but late","sets":["*"],"lane":"operation","call":{"operation":"lookup"},"expect":{"result_contains":"harbor"},"within_ms":500},
		{"name":"wrong and late","sets":["*"],"lane":"operation","call":{"operation":"lookup"},"expect":{"result_contains":"dawn"},"within_ms":500}]}`)
	clock := &clockStep{now: time.Unix(1000, 0)}
	var deadlines []time.Duration
	lanes := CheckLanes{Operation: func(ctx context.Context, _ string, _ map[string]any) (tools.Result, error) {
		if d, ok := ctx.Deadline(); ok {
			deadlines = append(deadlines, time.Until(d))
		}
		clock.take(600 * time.Millisecond)
		return tools.Result{Output: "the harbor"}, nil
	}}
	got := RunChecks(t.Context(), checks, lanes, clock.Now)
	if len(got) != 2 || got[0].Outcome != pluginfacility.CheckTimedOut || got[0].Took != 600*time.Millisecond || !strings.Contains(got[0].Words, "past its bound of 500 ms") {
		t.Fatalf("a right answer past the bound: %+v", got)
	}
	if got[1].Outcome != pluginfacility.CheckWrong {
		t.Fatalf("a wrong answer past the bound is wrong: %+v", got[1])
	}
	if len(deadlines) != 0 {
		t.Fatalf("the runner put a deadline of its own on the call: %v", deadlines)
	}
}

// A RUN STOPS AT THE FIRST FAILURE FOR GOOD, NEVER AT A MOMENTARY ONE, and
// the verdict is the failure that decides: a later wrong answer outranks
// an earlier timeout.
func TestARunStopsAtTheFirstFixedFailureAndTheVerdictIsIt(t *testing.T) {
	checks := readChecks(t, `{"checks":[
		{"name":"one","sets":["*"],"lane":"operation","call":{"operation":"a"},"expect":{"result_contains":"ok"},"within_ms":500},
		{"name":"two","sets":["*"],"lane":"operation","call":{"operation":"b"},"expect":{"result_contains":"ok"},"within_ms":500},
		{"name":"three","sets":["*"],"lane":"operation","call":{"operation":"c"},"expect":{"result_contains":"ok"},"within_ms":500},
		{"name":"four","sets":["*"],"lane":"operation","call":{"operation":"d"},"expect":{"result_contains":"ok"},"within_ms":500}]}`)
	var asked []string
	lanes := CheckLanes{Operation: func(_ context.Context, op string, _ map[string]any) (tools.Result, error) {
		asked = append(asked, op)
		switch op {
		case "a":
			return tools.Result{}, &supervisor.InvokeTimeoutError{PluginID: "p", Err: context.DeadlineExceeded}
		case "b":
			return tools.Refusal(tools.ReasonSafeSuspended, "SAFE"), nil
		case "c":
			return tools.Result{Output: "no"}, nil
		}
		return tools.Result{Output: "ok"}, nil
	}}
	got := RunChecks(t.Context(), checks, lanes, nil)
	if strings.Join(asked, "") != "abc" || len(got) != 3 {
		t.Fatalf("the run must go past the momentary failures and stop at the wrong answer: asked %v, %d results", asked, len(got))
	}
	if v := Verdict(got); v == nil || v.Check != "three" || v.Outcome != pluginfacility.CheckWrong {
		t.Fatalf("the verdict is the failure for good: %+v", v)
	}
	if v := Verdict(got[:2]); v == nil || v.Check != "one" || v.Outcome != pluginfacility.CheckTimedOut {
		t.Fatalf("with none for good, the first that did not pass: %+v", v)
	}
	if Verdict([]CheckResult{{Name: "x", Outcome: pluginfacility.CheckPassed}}) != nil {
		t.Fatal("a run that passed has no verdict against it")
	}
}

// AN UNRESOLVED $REF FAILS THAT CHECK ALONE. The check whose value does not
// resolve is never sent; the one before it runs and passes; the run's
// verdict names the check, fixed, in the reader's words.
func TestAnUnresolvedRefFailsThatCheckAlone(t *testing.T) {
	checks := readChecks(t, `{"checks":[
		{"name":"embeds the cue","sets":["*"],"lane":"embeddings","input":{"kind":"cue","text":{"$ref":"embeddings.json#/probes/0/text"}},"expect":{"vector_cosine_at_least":{"reference":{"$ref":"embeddings.json#/probes/0/vector"},"min":0.95}},"within_ms":5000},
		{"name":"embeds a probe that is not there","sets":["*"],"lane":"embeddings","input":{"kind":"cue","text":{"$ref":"embeddings.json#/probes/7/text"}},"expect":{"vector_cosine_at_least":{"reference":[0.6,0.8],"min":0.95}},"within_ms":5000}]}`)
	sent := 0
	lanes := CheckLanes{Embed: func(context.Context, string, string) ([]float32, error) { sent++; return []float32{0.6, 0.8}, nil }}
	got := RunChecks(t.Context(), checks, lanes, nil)
	if sent != 1 || len(got) != 2 || got[0].Outcome != pluginfacility.CheckPassed || got[1].Outcome != pluginfacility.CheckUnresolved {
		t.Fatalf("sent %d: %+v", sent, got)
	}
	if v := Verdict(got); v == nil || v.Check != "embeds a probe that is not there" || !strings.Contains(v.Words, "points at nothing") {
		t.Fatalf("the verdict: %+v", v)
	}
}

// validatedSource is a signed native source of vectors carrying the given
// validation.json, its child the suite's fakechild in mode.
func validatedSource(t *testing.T, id, mode, validation string) (string, *Options) {
	t.Helper()
	skipWhereNativeIsRefused(t)
	skipWhereTheSandboxCannotBeEstablished(t)
	roots, role := sharedPlatformRoots(t)
	child, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	model := modeModel(t, mode)
	models, err := json.Marshal([]ModelDecl{model.decl})
	if err != nil {
		t.Fatal(err)
	}
	member := strings.Replace(goodEmbeddings, `"m.onnx"`, `"`+model.decl.Name+`"`, 1)
	extra := map[string][]byte{ModelsFile: models, EmbeddingsFile: []byte(member)}
	if validation != "" {
		extra[packagefmt.ValidationFile] = []byte(validation)
	}
	pkg := nativePackageAs(t, id, child, role, extra,
		map[string]any{"plugin_family": EmbeddingsFamily, "aiios_min_version": packagefmt.EmbeddingsMinHost},
		&packagetest.InterfaceSpec{ID: EmbeddingsInterfaceID, Version: EmbeddingsInterfaceVersion, SchemaFile: "interfaces/aii.embeddings.v1.schema.json", Methods: []string{MethodEmbed}})
	opts := supervisedOpts(Options{Roots: roots})
	useStore(t, opts, model.fetch)
	return pkg, opts
}

// probeChecks is a validation.json of the source's two signed probes, each
// taken from embeddings.json, under the given bound.
func probeChecks(withinMS int) string {
	check := func(name string, i int) string {
		p := func(path string) string {
			return `{"$ref":"embeddings.json#/probes/` + string(rune('0'+i)) + path + `"}`
		}
		return `{"name":"` + name + `","sets":["*"],"lane":"embeddings","input":{"kind":` + p("/kind") + `,"text":` + p("/text") + `},` +
			`"expect":{"vector_cosine_at_least":{"reference":` + p("/vector") + `,"min":{"$ref":"embeddings.json#/probe_bound"}}},"within_ms":` + strconv.Itoa(withinMS) + `}`
	}
	return `{"checks":[` + check("embeds the cue", 0) + `,` + check("embeds the memory", 1) + `]}`
}

// standCandidate activates pkg as a candidate — standing, healthy, its
// operations not yet reachable — and validates it through the runtime the
// facility drives.
func standCandidate(t *testing.T, pkg string, opts *Options) (*ActivePlugin, *tools.Registry, pluginfacility.Checks) {
	t.Helper()
	reg := newRegistry(t)
	ap, err := ActivateShadow(t.Context(), pkg, reg, opts)
	if err != nil {
		t.Fatalf("activate the candidate: %v", err)
	}
	t.Cleanup(func() { _ = ap.CloseQuiet(context.Background()) })
	rt := NewRuntime(RuntimeWires{Options: func() *Options { return opts }})
	checks, err := rt.Validate(t.Context(), &Activation{ID: ap.ID, Plugin: ap})
	if err != nil {
		t.Fatal(err)
	}
	return ap, reg, checks
}

// A CANDIDATE IS CHECKED THROUGH THE WALL BEFORE ANYTHING CAN REACH IT. A
// signed native source declaring its two probes as checks, by $ref into
// embeddings.json, is started under the real wall, unregistered; the
// runner asks its child each check through the operation's own tool, and
// both pass. Nothing of it is in the registry.
func TestACandidateIsCheckedThroughTheWallBeforeItIsReachable(t *testing.T) {
	pkg, opts := validatedSource(t, "org.example.checked", "vectors", probeChecks(5000))
	ap, reg, checks := standCandidate(t, pkg, opts)
	if checks.Ran != 2 || checks.Failed != nil || checks.Variant != ap.VariantID {
		t.Fatalf("a working install must pass its checks: %+v %+v", checks, checks.Failed)
	}
	if got := ap.Checked(); got == nil || got.Ran != 2 || !ap.DeclaresChecks() {
		t.Fatalf("the activation keeps what its checks came to: %+v", got)
	}
	if slices.Contains(reg.Names(), ap.Embeddings.Embed) {
		t.Fatalf("a candidate that was only checked must not be reachable: %v", reg.Names())
	}
}

// A WRONG ANSWER IS NAMED WITH THE COSINE IT REACHED: the same package whose
// child computes a memory another way fails its second check at 0.8.
func TestAWrongAnswerIsNamedWithTheCosineItReached(t *testing.T) {
	pkg, opts := validatedSource(t, "org.example.checked-broken", "vectors-broken", probeChecks(5000))
	_, _, checks := standCandidate(t, pkg, opts)
	f := checks.Failed
	if f == nil || f.Check != "embeds the memory" || f.Outcome != pluginfacility.CheckWrong || !strings.Contains(f.Words, "came back at 0.800") {
		t.Fatalf("a broken install must be named by its second check at 0.8: %+v", f)
	}
}

// A CHECK SLOWER THAN ITS BOUND IS A TIMEOUT, AND THE CHILD IS NOT STOPPED.
// The child answers each check right, 300 ms late; the bound is 50 ms. The
// call keeps the deadline every call has, so the runner waits for the
// answer, and the child that gave it is the child that is still running:
// the same process, never restarted.
func TestASlowCheckTimesOutAndTheChildIsNotStopped(t *testing.T) {
	pkg, opts := validatedSource(t, "org.example.checked-slow", "vectors-slow", probeChecks(50))
	reg := newRegistry(t)
	ap, err := ActivateShadow(t.Context(), pkg, reg, opts)
	if err != nil {
		t.Fatalf("activate the candidate: %v", err)
	}
	defer func() { _ = ap.CloseQuiet(context.Background()) }()
	pid := ap.SupervisedPid()
	rt := NewRuntime(RuntimeWires{Options: func() *Options { return opts }})
	checks, err := rt.Validate(t.Context(), &Activation{ID: ap.ID, Plugin: ap})
	if err != nil {
		t.Fatal(err)
	}
	if f := checks.Failed; f == nil || f.Outcome != pluginfacility.CheckTimedOut || !strings.Contains(f.Words, "past its bound of 50 ms") {
		t.Fatalf("a right answer past the bound is a timeout: %+v", f)
	}
	if pid == 0 || ap.SupervisedPid() != pid || ap.Restarts() != 0 {
		t.Fatalf("the child was stopped: pid %d then %d, %d restarts", pid, ap.SupervisedPid(), ap.Restarts())
	}
}

// THE HOST REFUSES, WHEN IT READS THE PACKAGE, A CHECK OF A WRITE AND A
// LANE THE PACKAGE DOES NOT SERVE. Each package is otherwise one the host
// runs; each is refused at Start, typed, before its child exists, and
// nothing of it is registered.
func TestTheHostRefusesACheckOfAWriteAndALaneThePackageDoesNotServe(t *testing.T) {
	skipWhereNativeIsRefused(t)
	skipWhereTheSandboxCannotBeEstablished(t)
	roots, role := sharedPlatformRoots(t)
	child, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	opCheck := func(lane string) string {
		return `{"checks":[{"name":"pings","sets":["*"],"lane":"` + lane + `","call":{"operation":"ping"},"expect":{"result_contains":"pong"},"within_ms":500}]}`
	}
	for _, row := range []struct {
		name, descriptor, validation, want string
	}{
		{"a check of a write", `[{"id":"ping","summary":"pings","effects":"write.local"}]`, opCheck("operation"), "whose declared effects are write.local"},
		{"a check of an operation that declares no effects", `{"interface":"quarantine.probe","v":1}`, opCheck("operation"), "whose declared effects are none"},
		{"a check of the embeddings lane on a package that is no source", `[{"id":"ping","summary":"pings","effects":"read.internal"}]`,
			`{"checks":[{"name":"embeds","sets":["*"],"lane":"embeddings","input":{"kind":"cue","text":"x"},"expect":{"vector_cosine_at_least":{"reference":[1,0],"min":0.9}},"within_ms":500}]}`, "not a source of vectors"},
		{"a session check on a package with no session lane", `[{"id":"ping","summary":"pings","effects":"read.internal"}]`,
			`{"checks":[{"name":"speaks","sets":["*"],"lane":"session","input":{"text":"hello"},"expect":{"events":{"order":["session_ready"]}},"within_ms":500}]}`, "the package's activation has no session lane"},
		{"a set the package does not declare", `[{"id":"ping","summary":"pings","effects":"read.internal"}]`,
			`{"checks":[{"name":"pings","sets":["elsewhere"],"lane":"operation","call":{"operation":"ping"},"expect":{"result_contains":"pong"},"within_ms":500}]}`, `component set "elsewhere"`},
	} {
		t.Run(row.name, func(t *testing.T) {
			pkg := nativePackageAs(t, "org.example.refused", child, role, map[string][]byte{
				"interfaces/quarantine.probe.v1.schema.json": []byte(row.descriptor),
				packagefmt.ValidationFile:                    []byte(row.validation),
			}, nil, nil)
			reg := newRegistry(t)
			ap, err := Activate(t.Context(), pkg, reg, supervisedOpts(Options{Roots: roots}))
			if ap != nil {
				_ = ap.Deactivate(context.Background())
			}
			var refused *ValidationError
			if !errors.As(err, &refused) || !refused.WaitsOnPackage() || !strings.Contains(err.Error(), row.want) {
				t.Fatalf("want the package refused saying %q, got %v", row.want, err)
			}
			for _, name := range reg.Names() {
				if strings.HasPrefix(name, "pl_org_example_refused") {
					t.Fatalf("a refused package registered %s", name)
				}
			}
		})
	}
}

// A CHECK OF A LANE THIS HOST DOES NOT RUN IS NOT RUN, NEVER A REFUSAL
// (contract v2.4). A host older than validation.json installs a package
// carrying one; a host that knows the file is no stricter about what it
// does not know. The package starts, its checks of lanes this host runs
// are run and pass, and the newer lane's check is named on the card as
// not run by this host — unanswered, held against nothing.
func TestACheckOfALaneThisHostDoesNotRunIsNotRunNeverARefusal(t *testing.T) {
	future := `{"name":"streams its reply","sets":["*"],"lane":"stream","feed":{"chunks":3},"expect":{"bytes_at_least":10},"within_ms":2000}`
	file := strings.Replace(probeChecks(5000), `{"checks":[`, `{"checks":[`+future+`,`, 1)
	pkg, opts := validatedSource(t, "org.example.checked-future", "vectors", file)
	_, _, checks := standCandidate(t, pkg, opts)
	f := checks.Failed
	if checks.Ran != 3 || f == nil || f.Check != "streams its reply" || f.Outcome != pluginfacility.CheckUnanswered || !strings.Contains(f.Words, `not run by this host: its lane "stream"`) {
		t.Fatalf("the newer lane's check is named as not run, and nothing else fails: %+v %+v", checks, f)
	}
	if f.Outcome.Fixed() {
		t.Fatal("a check this host does not run holds nothing against the set")
	}
}

// A SET WHOSE CHECKS FAILED FOR GOOD IS PASSED OVER, AND THE NEXT IS CHOSEN:
// the facility's word (Options.ChecksFailed) takes the large set off this
// machine with its reason and remedy on its card, and the compact one is
// selected; with nothing remembered the large one is.
func TestASetWhoseChecksFailedIsPassedOverForTheNext(t *testing.T) {
	s := declaredShape(t)
	opts := judgeOpts(s, gpuHost(11*gib), 500*gib)
	if sel, err := judge(t, s, opts); err != nil || sel.VariantID != "large" {
		t.Fatalf("with nothing remembered the large set is chosen: %v %v", sel.VariantID, err)
	}
	opts.ChecksFailed = map[string]string{"large": `"embeds the memory": came back at 0.800 of its reference, under the 0.950 it asks`}
	sel, err := judge(t, s, opts)
	if err != nil || sel.VariantID != "compact" {
		t.Fatalf("the next set must be chosen: %v %v", sel.VariantID, err)
	}
	large := verdictOf(t, sel.Sets, "large")
	if text := reasonsText(large); !strings.Contains(text, "its publisher's checks failed on this machine: \"embeds the memory\"") || !strings.Contains(text, "Try again") {
		t.Fatalf("the card says why and what admits it: %s", text)
	}
}
