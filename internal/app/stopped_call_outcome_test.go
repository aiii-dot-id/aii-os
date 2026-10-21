package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// callsScript asks, in one response, for one call of each tool, then
// answers; once the turn's context has ended it answers nothing. calls
// counts every provider call the loop began.
type callsScript struct {
	tools []string
	calls int
}

func (s *callsScript) Chat(ctx context.Context, _ []llm.Message, _ llm.ChatOptions) (*llm.Response, error) {
	s.calls++
	if s.calls == 1 {
		var tcs []llm.ToolCall
		for i, name := range s.tools {
			var tc llm.ToolCall
			tc.ID, tc.Type, tc.Function.Name, tc.Function.Arguments = fmt.Sprintf("call-%d", i+1), "function", name, "{}"
			tcs = append(tcs, tc)
		}
		return &llm.Response{Choices: []llm.Choice{{Message: llm.Message{ToolCalls: tcs}}}}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &llm.Response{Choices: []llm.Choice{{Message: llm.Message{Content: "done"}}}}, nil
}

// toolOutcomeOf is the recorded outcome of tool's one call.
func toolOutcomeOf(t *testing.T, a *App, tool string) string {
	t.Helper()
	var outcome string
	if err := a.store.DB().QueryRow(`SELECT outcome FROM conversations WHERE kind='tool_result' AND json_extract(details, '$.tool')=?`, tool).Scan(&outcome); err != nil {
		t.Fatalf("%s's result: %v", tool, err)
	}
	return outcome
}

func TestAStoppedCallIsRecordedCancelledAndAFailedOneFailed(t *testing.T) {
	for _, c := range []struct {
		name, want string
		stop       bool
		run        func(ctx context.Context, started chan<- struct{}) (tools.Result, error)
	}{
		{"the operator's stop", "cancelled", true, func(ctx context.Context, started chan<- struct{}) (tools.Result, error) {
			close(started)
			<-ctx.Done()
			return tools.Result{}, ctx.Err()
		}},
		{"its own failure", "failed", false, func(_ context.Context, started chan<- struct{}) (tools.Result, error) {
			close(started)
			return tools.Result{}, errors.New("the disk is full")
		}},
		{"its own deadline", "failed", false, func(ctx context.Context, started chan<- struct{}) (tools.Result, error) {
			close(started)
			own, cancel := context.WithTimeout(ctx, time.Millisecond)
			defer cancel()
			<-own.Done()
			return tools.Result{}, own.Err()
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newVoiceApp(t)
			a.toolReg = tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
			started := make(chan struct{})
			a.toolReg.Register(&fixtureTool{name: "fixture_stop", run: func(ctx context.Context) (tools.Result, error) {
				return c.run(ctx, started)
			}})
			model := &callsScript{tools: []string{"fixture_stop"}}
			loop := conversation.New(model, appToolExecutor{a}, appToolDefiner{a}, appTranscript{st: a.store}, nil, testLoop(conversation.Config{}))
			ctx, done := a.beginCancellableTurn(context.Background())
			defer done()
			ended := make(chan error, 1)
			go func() {
				_, err := loop.Run(ctx, "system", []llm.Message{{Role: "user", Content: "go"}})
				ended <- err
			}()
			<-started
			if c.stop && !a.CancelTurn() {
				t.Fatal("no turn to stop")
			}
			select {
			case err := <-ended:
				if c.stop && !errors.Is(err, context.Canceled) {
					t.Fatalf("a stopped turn ends cancelled, got %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("the turn did not end")
			}
			if got := toolOutcomeOf(t, a, "fixture_stop"); got != c.want {
				t.Fatalf("the call's record says %q, want %q", got, c.want)
			}
			// A stopped turn begins no provider call after the stop: one on
			// an ended context reaches no provider and was counted as made.
			if c.stop && model.calls != 1 {
				t.Fatalf("the loop began %d provider calls; after the stop it must begin none", model.calls)
			}
		})
	}
}

// THE STOP STOPS THE BATCH. The model asked for two calls in one response;
// the operator stopped the turn during the first. The second is not run (a
// tool that never reads its context would otherwise act after the stop),
// and its record says cancelled, so the request is not lost.
func TestTheOperatorsStopStopsTheRestOfTheBatch(t *testing.T) {
	a := newVoiceApp(t)
	a.toolReg = tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
	started := make(chan struct{})
	a.toolReg.Register(&fixtureTool{name: "fixture_first", run: func(ctx context.Context) (tools.Result, error) {
		close(started)
		<-ctx.Done()
		return tools.Result{}, ctx.Err()
	}})
	var ran atomic.Bool
	a.toolReg.Register(&fixtureTool{name: "fixture_second", run: func(context.Context) (tools.Result, error) {
		ran.Store(true) // an effect that never looks at its context
		return tools.Result{Output: "written"}, nil
	}})
	model := &callsScript{tools: []string{"fixture_first", "fixture_second"}}
	loop := conversation.New(model, appToolExecutor{a}, appToolDefiner{a}, appTranscript{st: a.store}, nil, testLoop(conversation.Config{}))
	ctx, done := a.beginCancellableTurn(context.Background())
	defer done()
	ended := make(chan error, 1)
	go func() {
		_, err := loop.Run(ctx, "system", []llm.Message{{Role: "user", Content: "go"}})
		ended <- err
	}()
	<-started
	if !a.CancelTurn() {
		t.Fatal("no turn to stop")
	}
	select {
	case err := <-ended:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("a stopped turn ends cancelled, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the turn did not end")
	}
	if ran.Load() {
		t.Fatal("the second call ran after the operator's stop")
	}
	if first, second := toolOutcomeOf(t, a, "fixture_first"), toolOutcomeOf(t, a, "fixture_second"); first != "cancelled" || second != "cancelled" {
		t.Fatalf("the batch's records say %q and %q, want cancelled for both", first, second)
	}
	if model.calls != 1 {
		t.Fatalf("the loop began %d provider calls; after the stop it must begin none", model.calls)
	}
}

// SAFE's transient record says the same: a stopped call is cancelled
// there too, and the page reads it from the one mapping.
func TestSAFEsRecordOfAStoppedCallSaysCancelled(t *testing.T) {
	r := &safeToolRecord{}
	if err := r.RecordToolStart("turn_1", 1, "c", "ls", "{}", "m"); err != nil {
		t.Fatal(err)
	}
	if err := r.recordToolDone("turn_1", 1, "ls", observedOutcome(conversation.Observation{Failed: true, Cancelled: true})); err != nil {
		t.Fatal(err)
	}
	e := r.Events()[0]
	if !e.Done || e.Outcome != "cancelled" {
		t.Fatalf("SAFE's record of a stopped call: %+v", e)
	}
	for obs, want := range map[conversation.Observation]string{
		{Failed: true}:                  "failed",
		{}:                              "succeeded",
		{Failed: true, Cancelled: true}: "cancelled",
		{Failed: true, Refused: true}:   "refused",
	} {
		if got := observedOutcome(obs); string(got) != want {
			t.Errorf("observedOutcome(%+v) = %q, want %q", obs, got, want)
		}
	}
}

// argsScript asks for one call with the given arguments, then answers.
type argsScript struct {
	tool, args string
	calls      int
}

func (s *argsScript) Chat(context.Context, []llm.Message, llm.ChatOptions) (*llm.Response, error) {
	s.calls++
	if s.calls == 1 {
		var tc llm.ToolCall
		tc.ID, tc.Type, tc.Function.Name, tc.Function.Arguments = "call-1", "function", s.tool, s.args
		return &llm.Response{Choices: []llm.Choice{{Message: llm.Message{ToolCalls: []llm.ToolCall{tc}}}}}, nil
	}
	return &llm.Response{Choices: []llm.Choice{{Message: llm.Message{Content: "done"}}}}, nil
}

func TestARefusedCallIsRecordedRefusedAndAFailedOneFailed(t *testing.T) {
	// Marshalled, not spliced: a Windows path's backslashes are JSON
	// escapes, and a spliced C:\Users\... made the call's arguments
	// unreadable, so the call failed before the sandbox saw it.
	outsideArgs, err := json.Marshal(map[string]string{"file_path": filepath.Join(t.TempDir(), "secret.txt")})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, tool, args, outcome, code string
	}{
		{"refused by the sandbox", "read", string(outsideArgs), "refused", tools.ReasonPathRefused},
		{"a write with no content", "write", `{"file_path":"keep.txt"}`, "refused", tools.ReasonArgumentsRequired},
		{"a copy with no destination", "write", `{"file_path":"keep.txt","action":"copy"}`, "refused", tools.ReasonArgumentsRequired},
		{"ran and failed", "read", `{"file_path":"missing.txt"}`, "failed", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newVoiceApp(t)
			home := t.TempDir()
			keep := filepath.Join(home, "keep.txt")
			if err := os.WriteFile(keep, []byte("kept"), 0o600); err != nil {
				t.Fatal(err)
			}
			a.toolReg = tools.NewRegistry(home, nil, tools.Timeouts{})
			loop := conversation.New(&argsScript{tool: c.tool, args: c.args}, appToolExecutor{a}, appToolDefiner{a}, appTranscript{st: a.store}, nil, testLoop(conversation.Config{}))
			if _, err := loop.Run(context.Background(), "system", []llm.Message{{Role: "user", Content: "go"}}); err != nil {
				t.Fatal(err)
			}
			var outcome, code string
			if err := a.store.DB().QueryRow(`SELECT outcome, COALESCE(json_extract(details, '$.reason_code'), '') FROM conversations WHERE kind='tool_result' AND json_extract(details, '$.tool')=?`, c.tool).Scan(&outcome, &code); err != nil {
				t.Fatal(err)
			}
			if outcome != c.outcome || code != c.code {
				t.Fatalf("the call's record: outcome %q code %q, want %q %q", outcome, code, c.outcome, c.code)
			}
			if got, err := os.ReadFile(keep); err != nil || string(got) != "kept" {
				t.Fatalf("the refused call changed a file: %q %v", got, err)
			}
		})
	}
}

func TestBootSafesRecordOfEachOutcomeIsTheLiveRecords(t *testing.T) {
	outsideArgs, err := json.Marshal(map[string]string{"file_path": filepath.Join(t.TempDir(), "secret.txt")})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, tool, args, want string
		stop                   bool
		run                    func(ctx context.Context, started chan<- struct{}) (tools.Result, error)
	}{
		{"the operator's stop", "fixture_stop", "{}", "cancelled", true, func(ctx context.Context, started chan<- struct{}) (tools.Result, error) {
			close(started)
			<-ctx.Done()
			return tools.Result{}, ctx.Err()
		}},
		{"refused by the sandbox", "read", string(outsideArgs), "refused", false, nil},
		{"its own failure", "fixture_fail", "{}", "failed", false, func(_ context.Context, _ chan<- struct{}) (tools.Result, error) {
			return tools.Result{}, errors.New("the disk is full")
		}},
		{"it ran", "fixture_ok", "{}", "succeeded", false, func(_ context.Context, _ chan<- struct{}) (tools.Result, error) {
			return tools.Result{Output: "fine"}, nil
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newVoiceApp(t)
			a.toolReg = tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{})
			started := make(chan struct{})
			if c.run != nil {
				a.toolReg.Register(&fixtureTool{name: c.tool, run: func(ctx context.Context) (tools.Result, error) {
					return c.run(ctx, started)
				}})
			}
			rec := &safeToolRecord{}
			loop := conversation.New(&argsScript{tool: c.tool, args: c.args}, appToolExecutor{a}, appToolDefiner{a}, rec, nil, testLoop(conversation.Config{}))
			ctx, done := a.beginCancellableTurn(context.Background())
			defer done()
			ended := make(chan error, 1)
			go func() {
				_, err := loop.Run(ctx, "system", []llm.Message{{Role: "user", Content: "go"}})
				ended <- err
			}()
			if c.stop {
				<-started
				if !a.CancelTurn() {
					t.Fatal("no turn to stop")
				}
			}
			select {
			case <-ended:
			case <-time.After(10 * time.Second):
				t.Fatal("the turn did not end")
			}
			events := rec.Events()
			if len(events) != 1 || events[0].Tool != c.tool || !events[0].Done {
				t.Fatalf("SAFE's record must hold the one call, started and done: %+v", events)
			}
			if got := string(events[0].Outcome); got != c.want {
				t.Fatalf("boot-SAFE's record of the call says %q, want %q", got, c.want)
			}
		})
	}
}

// The same, through the boot-SAFE constructor itself: a read the sandbox
// refuses is recorded refused in the record the SAFE page reads.
func TestBootSafeRecordsARefusedReadRefused(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "SafeRefused")
	buildPriorProjection(t, ledgerPath, dbPath)
	tamperChain(t, keyPath, ledgerPath)

	const ask = "FOLDPROBE read the secret"
	outside, err := json.Marshal(map[string]string{"file_path": filepath.Join(t.TempDir(), "secret.txt")})
	if err != nil {
		t.Fatal(err)
	}
	_, srv := newFoldProbeLLM(t, ask, "read", string(outside))
	cfg := safebootConfig(t, dir, "SafeRefused", keyPath, ledgerPath, dbPath)
	cfg.LLM = withTestProvider(t, dir, "test", srv.URL, "m", "sk-x")

	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("SAFE boot must come up: %v", err)
	}
	defer stopApp(t, app)
	if _, safe := app.SafeMode(); !safe {
		t.Fatal("fixture: this boot was meant to be SAFE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := app.handleMessage(ctx, ask); err != nil {
		t.Fatalf("the SAFE conversation's turn: %v", err)
	}
	events := app.safeTools.Events()
	if len(events) != 1 || events[0].Tool != "read" || !events[0].Done {
		t.Fatalf("the transient record must hold the one call, started and done: %+v", events)
	}
	if events[0].Outcome != "refused" {
		t.Fatalf("boot-SAFE's record of a read the sandbox refused says %q, want refused", events[0].Outcome)
	}
}
