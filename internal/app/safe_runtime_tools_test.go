package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// toolStep is one tool call a scripted provider asks for.
type toolStep struct {
	tool string
	args map[string]any
}

// scriptedToolLLM is an OpenAI-shaped provider that walks its steps: the
// n-th request carrying marker, counted by the tool results it already
// holds, asks for step n, and after the last step it answers final. With
// blockAt >= 0, the request at that step waits for its caller to give up.
type scriptedToolLLM struct {
	marker  string
	steps   []toolStep
	final   string
	blockAt int
	mu      sync.Mutex
	seen    []string // the tool results of the latest request, in order
	bodies  []string // every request body, marked or not, in arrival order
	blocked chan struct{}
	once    sync.Once
}

func newScriptedToolLLM(t *testing.T, marker string, steps []toolStep, final string, blockAt int) (*scriptedToolLLM, *httptest.Server) {
	t.Helper()
	p := &scriptedToolLLM{marker: marker, steps: steps, final: final, blockAt: blockAt, blocked: make(chan struct{})}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		p.mu.Lock()
		p.bodies = append(p.bodies, string(body))
		p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if !strings.Contains(string(body), p.marker) {
			_, _ = io.WriteString(w, textReply("noted"))
			return
		}
		var req struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(body, &req)
		var results []string
		for _, m := range req.Messages {
			if m.Role == "tool" {
				var s string
				if json.Unmarshal(m.Content, &s) != nil {
					s = string(m.Content)
				}
				results = append(results, s)
			}
		}
		p.mu.Lock()
		p.seen = results
		p.mu.Unlock()
		n := len(results)
		if n == p.blockAt {
			p.once.Do(func() { close(p.blocked) })
			<-r.Context().Done()
			return
		}
		if n < len(p.steps) {
			args, _ := json.Marshal(p.steps[n].args)
			call := toolCall(fmt.Sprintf("call_%d", n), p.steps[n].tool, string(args))
			_, _ = io.WriteString(w, chatReply("", "tool_calls", []string{call}, scriptedStepUsage))
			return
		}
		_, _ = io.WriteString(w, chatReply(p.final, "stop", nil, scriptedFinalUsage))
	}))
	t.Cleanup(srv.Close)
	return p, srv
}

// What the scripted provider says each of its calls cost. The plugin
// compatibility suite drives its turns through this provider and reads a
// turn's recorded usage back (a step and the final answer: 200 in, 15 out;
// a turn stopped inside its first step: 100 in, 10 out), so the figures
// are this provider's own and not the reply helper's default.
var (
	scriptedStepUsage  = replyUsage{prompt: 100, completion: 10}
	scriptedFinalUsage = replyUsage{prompt: 100, completion: 5}
)

// results is what the model was shown for its calls, in order.
func (p *scriptedToolLLM) results() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.seen...)
}

// requests is every request body the provider received, in order.
func (p *scriptedToolLLM) requests() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.bodies...)
}

// projectionSnapshot is a logical digest of every table the store holds,
// read through the store's own read path: rows, not the file, so WAL
// staging can neither hide a write nor fake one.
func projectionSnapshot(t *testing.T, st *store.Store) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := st.ReadWith(t.Context(), func(db store.Reader) error {
		rows, err := db.QueryContext(t.Context(), `SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
		if err != nil {
			return err
		}
		var names []string
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				rows.Close()
				return err
			}
			names = append(names, n)
		}
		rows.Close()
		for _, name := range names {
			r, err := db.QueryContext(t.Context(), `SELECT * FROM "`+strings.ReplaceAll(name, `"`, `""`)+`"`)
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			cols, _ := r.Columns()
			var lines []string
			for r.Next() {
				vals := make([]any, len(cols))
				ptrs := make([]any, len(cols))
				for i := range vals {
					ptrs[i] = &vals[i]
				}
				if err := r.Scan(ptrs...); err != nil {
					r.Close()
					return err
				}
				var b strings.Builder
				for _, v := range vals {
					if raw, ok := v.([]byte); ok {
						v = string(raw)
					}
					fmt.Fprintf(&b, "%v\x1f", v)
				}
				lines = append(lines, b.String())
			}
			r.Close()
			sort.Strings(lines)
			sum := sha256.Sum256([]byte(strings.Join(lines, "\x1e")))
			out[name] = fmt.Sprintf("%d:%s", len(lines), hex.EncodeToString(sum[:8]))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot of the projection: %v", err)
	}
	return out
}

func liveIdentity(t *testing.T, dir, providerURL string) (*App, string) {
	t.Helper()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "RuntimeSafe")
	cfg := safebootConfig(t, dir, "RuntimeSafe", keyPath, ledgerPath, dbPath)
	cfg.LLM = withTestProvider(t, dir, "test", providerURL, "m", "sk-x")
	app := New(cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("live boot: %v", err)
	}
	if _, safe := app.SafeMode(); safe {
		stopApp(t, app)
		t.Fatal("fixture: this boot was meant to be NORMAL")
	}
	return app, ledgerPath
}

func liveThenSafe(t *testing.T, dir, providerURL string) (*App, string) {
	t.Helper()
	app, ledgerPath := liveIdentity(t, dir, providerURL)
	app.enterSafe("test: integrity lost at runtime")
	if _, frozen := app.store.Frozen(); !frozen {
		stopApp(t, app)
		t.Fatal("fixture: the transition must freeze the store")
	}
	return app, ledgerPath
}

// An operator turn after the identity ENTERS SAFE runs read, grep and ls
// to completion, a mutation is still refused, and nothing durable is
// written: every table and the ledger's bytes are as SAFE found them.
func TestRuntimeSafeRunsItsReadOnlyToolsWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	probe := filepath.Join(dir, "diagnostics.txt")
	if err := os.WriteFile(probe, []byte("line one\nSAFEPROBE needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	forbidden := filepath.Join(dir, "must-not-exist.txt")
	const ask = "SAFETOOLS check the diagnostics"
	llm, srv := newScriptedToolLLM(t, "SAFETOOLS", []toolStep{
		{"ls", map[string]any{"path": "."}},
		{"grep", map[string]any{"pattern": "SAFEPROBE", "path": "."}},
		{"read", map[string]any{"file_path": probe}},
		{"write", map[string]any{"file_path": forbidden, "content": "x"}},
	}, "VERDICT: done", -1)
	app, ledgerPath := liveThenSafe(t, dir, srv.URL)
	defer stopApp(t, app)

	ledgerBefore, _ := fileDigest(t, ledgerPath)
	before := projectionSnapshot(t, app.store)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	reply, err := app.handleMessage(ctx, ask)
	if err != nil {
		t.Fatalf("a read-only tool call failed the runtime-SAFE turn: %v", err)
	}
	if !strings.Contains(reply, "VERDICT: done") {
		t.Fatalf("the turn did not finish after its tool calls: %q", reply)
	}

	seen := llm.results()
	if len(seen) != 4 {
		t.Fatalf("four tool results must reach the model, got %d: %q", len(seen), seen)
	}
	if !strings.Contains(seen[0], "diagnostics.txt") {
		t.Errorf("ls must run and list the directory: %q", seen[0])
	}
	if !strings.Contains(seen[1], "SAFEPROBE needle") {
		t.Errorf("grep must run and find the line: %q", seen[1])
	}
	if !strings.Contains(seen[2], "line one") {
		t.Errorf("read must run and return the file: %q", seen[2])
	}
	if !strings.Contains(seen[3], "safe mode") {
		t.Errorf("a mutation must still be refused in SAFE, naming it: %q", seen[3])
	}
	if _, err := os.Stat(forbidden); !os.IsNotExist(err) {
		t.Errorf("the refused write must not have happened: %v", err)
	}

	events := app.safeTools.Events()
	if len(events) != 4 {
		t.Fatalf("the transient record must hold the four calls: %+v", events)
	}
	for i, want := range []string{"ls", "grep", "read", "write"} {
		if events[i].Tool != want || !events[i].Done {
			t.Errorf("call %d must be %s, started and done: %+v", i, want, events[i])
		}
	}
	for _, e := range events[:3] {
		if e.Outcome != "succeeded" {
			t.Errorf("a read-only call must not be recorded failed: %+v", e)
		}
	}

	after := projectionSnapshot(t, app.store)
	for name, digest := range before {
		if after[name] != digest {
			t.Errorf("table %s changed under the freeze: %s -> %s", name, digest, after[name])
		}
	}
	if len(after) != len(before) {
		t.Errorf("the set of tables changed under the freeze: %d -> %d", len(before), len(after))
	}
	if got, _ := fileDigest(t, ledgerPath); got != ledgerBefore {
		t.Error("the ledger's bytes changed under SAFE")
	}
}

func TestSafeToolCallsAreStampedByTheRunningEngine(t *testing.T) {
	boots := map[string]func(t *testing.T, dir, url string) *App{
		"live, then SAFE": func(t *testing.T, dir, url string) *App {
			app, _ := liveThenSafe(t, dir, url)
			return app
		},
		"boot SAFE": func(t *testing.T, dir, url string) *App {
			keyPath, ledgerPath, dbPath := birthFixture(t, dir, "SafeStamp")
			buildPriorProjection(t, ledgerPath, dbPath)
			tamperChain(t, keyPath, ledgerPath)
			cfg := safebootConfig(t, dir, "SafeStamp", keyPath, ledgerPath, dbPath)
			cfg.LLM = withTestProvider(t, dir, "test", url, "m", "sk-x")
			app := New(cfg)
			if err := startLiveForTest(app); err != nil {
				t.Fatalf("SAFE boot: %v", err)
			}
			if _, safe := app.SafeMode(); !safe {
				stopApp(t, app)
				t.Fatal("rig: a tampered chain did not boot SAFE")
			}
			return app
		},
	}
	for name, boot := range boots {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			_, srv := newScriptedToolLLM(t, "SAFESTAMP", []toolStep{
				{"ls", map[string]any{"path": "."}},
			}, "VERDICT: listed", -1)
			app := boot(t, dir, srv.URL)
			defer stopApp(t, app)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, err := app.handleMessage(ctx, "SAFESTAMP list the directory"); err != nil {
				t.Fatalf("the SAFE turn failed: %v", err)
			}
			events := app.safeTools.Events()
			if len(events) != 1 || events[0].Tool != "ls" || !events[0].Done {
				t.Fatalf("rig: the call is not in the transient record: %+v", events)
			}
			if events[0].StartSequence == 0 || events[0].DoneSequence <= events[0].StartSequence {
				t.Fatalf("no engine stamped the call: start %d, done %d", events[0].StartSequence, events[0].DoneSequence)
			}

			page, err := app.QueryInteractions(ctx, interaction.Query{Version: 1, Source: "transient"})
			if err != nil {
				t.Fatal(err)
			}
			var kinds []interaction.Kind
			for i, r := range page.Rows {
				kinds = append(kinds, r.Kind)
				if i > 0 && r.Sequence <= page.Rows[i-1].Sequence {
					t.Fatalf("two owners' sequences on one page: %+v", page.Rows)
				}
			}
			want := []interaction.Kind{interaction.Message, interaction.ToolCall, interaction.ToolResult, interaction.Message}
			if !reflect.DeepEqual(kinds, want) {
				t.Fatalf("the page lists %v, want the question, the call, its result and the answer: %+v", kinds, page.Rows)
			}
			if page.Rows[1].Details.Tool != "ls" || page.Rows[1].Sequence != events[0].StartSequence {
				t.Fatalf("the listed call is not the recorded one: %+v", page.Rows[1])
			}
			// And by THIS engine: its next stamp follows the page's last.
			if next, _ := app.engine.StampTransient(); next != page.Rows[3].Sequence+1 {
				t.Fatalf("the engine's next stamp is %d, after a page ending at %d", next, page.Rows[3].Sequence)
			}
		})
	}
}

// A runtime-SAFE turn still ends when its caller gives up, and the
// identity still stops while one is in flight: the transient record holds
// no lock across the provider call.
func TestRuntimeSafeTurnEndsOnCancelAndOnShutdown(t *testing.T) {
	for _, how := range []string{"cancel", "stop"} {
		t.Run(how, func(t *testing.T) {
			dir := t.TempDir()
			llm, srv := newScriptedToolLLM(t, "SAFEHANG", []toolStep{
				{"ls", map[string]any{"path": "."}},
			}, "never", 1)
			app, _ := liveThenSafe(t, dir, srv.URL)
			stopped := false
			defer func() {
				if !stopped {
					stopApp(t, app)
				}
			}()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := app.handleMessage(ctx, "SAFEHANG list and wait")
				done <- err
			}()
			select {
			case <-llm.blocked:
			case <-time.After(20 * time.Second):
				t.Fatal("the turn never reached its second provider call")
			}
			if events := app.safeTools.Events(); len(events) != 1 || events[0].Tool != "ls" || !events[0].Done {
				t.Fatalf("the call before the hang must be in the transient record: %+v", events)
			}

			var halted *asideStop
			if how == "cancel" {
				cancel()
			} else {
				halted = stopAppAside(app)
				stopped = true
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("a turn that was given up must say so")
				}
			case <-time.After(15 * time.Second):
				t.Fatalf("%s did not end the runtime-SAFE turn", how)
			}
			if halted != nil {
				select {
				case <-halted.done:
					halted.check(t)
				case <-time.After(30 * time.Second):
					t.Fatal("Stop did not return with a runtime-SAFE turn in flight")
				}
			}
		})
	}
}

// A call that started before the freeze and finishes after it does not
// fail its turn: its durable start stays open (the next boot names it),
// and every call begun under the freeze is recorded in memory only.
func TestAToolCallThatStraddlesTheFreezeFinishesItsTurn(t *testing.T) {
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rec := &safeToolRecord{}
	tr := appTranscript{st: st, safe: rec}

	if tr.TranscriptResultExcerptLimit() != store.TranscriptResultLimit {
		t.Fatal("unfrozen, the durable record's excerpt is offered")
	}
	if err := tr.RecordToolStart("t0", 1, "c0", "ls", `{}`, "m"); err != nil {
		t.Fatal(err)
	}
	if durable, err := tr.RecordToolDoneDurable("t0", 1, "ls", `{}`, conversation.Observation{Text: "ok"}); err != nil || !durable {
		t.Fatalf("unfrozen completion must report durable coverage: durable=%v err=%v", durable, err)
	}
	if err := tr.RecordToolStart("t1", 1, "c1", "ls", `{}`, "m"); err != nil {
		t.Fatalf("an unfrozen start is durable: %v", err)
	}
	st.SetFrozen(true, "test")
	if durable, err := tr.RecordToolDoneDurable("t1", 1, "ls", `{}`, conversation.Observation{Text: "ok"}); err != nil || durable {
		t.Fatalf("a call begun before SAFE succeeds transiently, not durably: durable=%v err=%v", durable, err)
	}
	if err := tr.RecordToolStart("t2", 1, "c2", "grep", `{}`, "m"); err != nil {
		t.Fatalf("a start under the freeze is transient: %v", err)
	}
	if durable, err := tr.RecordToolDoneDurable("t2", 1, "grep", `{}`, conversation.Observation{Text: "ok"}); err != nil || durable {
		t.Fatalf("a done under the freeze is transient: durable=%v err=%v", durable, err)
	}
	if tr.TranscriptResultExcerptLimit() != 0 {
		t.Fatal("frozen, nothing of a result is retained, so no excerpt is offered")
	}
	events := rec.Events()
	if len(events) != 2 || events[0].TurnID != "t1" || !events[0].Done || events[0].DurableParent == "" || events[0].StartedAt != "" || events[1].TurnID != "t2" || !events[1].Done {
		t.Fatalf("SAFE keeps the observed completion without inventing a transient start, plus the call begun under the freeze: %+v", events)
	}
	// The claim the warning makes is true: t1's durable start is still open.
	if open, err := st.ToolCallStarted("t1", 1); err != nil || !open {
		t.Fatalf("the freeze refused its done, so t1's durable call stays open: open=%v err=%v", open, err)
	}
	// A completion nobody started, anywhere, is the loop's own error —
	// SAFE does not invent a start to swallow it.
	if err := tr.RecordToolDone("t3", 1, "ls", `{}`, conversation.Observation{Text: "ok"}); err == nil {
		t.Fatal("a done with no start, durable or transient, must not be reported as recorded")
	}
}

// Outside SAFE nothing is diverted: an ordinary failure of the durable
// record still fails its turn, which is what makes the freeze the only
// thing the transient record answers for.
func TestOutsideSafeTheDurableRecordsFailuresStand(t *testing.T) {
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	tr := appTranscript{st: st, safe: &safeToolRecord{}}
	if err := tr.RecordToolDone("nobody", 1, "ls", `{}`, conversation.Observation{Text: "ok"}); err == nil {
		t.Fatal("a completion with no started row is an error the loop must see")
	}
}

// A CHILD ALREADY RUNNING WHEN SAFE BEGINS keeps its record too. No
// sub-agent can START under SAFE, because the queue is frozen, so the
// wiring that matters is the one already in flight: the child's own loop
// diverts to the same transient record, its call still runs, and nothing
// of it reaches the projection.
func TestASubagentRunningWhenSafeBeginsRecordsTransiently(t *testing.T) {
	dir := t.TempDir()
	const goal = "SAFECHILD look around"
	arrived, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	rounds := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if !strings.Contains(string(body), "SAFECHILD") {
			_, _ = io.WriteString(w, textReply("noted"))
			return
		}
		mu.Lock()
		rounds++
		k := rounds
		mu.Unlock()
		if k == 1 {
			once.Do(func() { close(arrived) })
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			_, _ = io.WriteString(w, callReply("", toolCall("call_child", "ls", `{"path":"."}`)))
			return
		}
		_, _ = io.WriteString(w, textReply("VERDICT: done"))
	}))
	t.Cleanup(srv.Close)

	app, _ := liveIdentity(t, dir, srv.URL)
	defer stopApp(t, app)
	req := identity.SubagentRequest{Goal: goal, Depth: 1, SessionID: "ws_safe", WallSeconds: 30, Context: "folded"}
	payload, _ := json.Marshal(req)
	if _, enq, err := app.store.EnqueueWorkWithSessionBelowLimit(&store.WorkItem{
		Kind: identity.SubagentWorkKind, Payload: string(payload), DedupKey: "ws_safe", Source: "identity", LeaseMs: 65000,
	}, 4, "ws_safe", store.SubagentDescription(goal)); err != nil || !enq {
		t.Fatalf("enqueue the child: %v %v", enq, err)
	}
	if app.queueWake != nil {
		app.queueWake()
	}
	select {
	case <-arrived:
	case <-time.After(60 * time.Second):
		t.Fatal("the child never reached its provider")
	}

	app.enterSafe("test: integrity lost while a child was running")
	before := projectionSnapshot(t, app.store)
	close(release)

	var events []SafeToolEvent
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); {
		// The record is written when the call starts and updated when it
		// ends; wait for the end, which is what the assertion below reads.
		if events = app.safeTools.Events(); len(events) > 0 && events[0].Done {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(events) != 1 || events[0].Tool != "ls" || !events[0].Done || events[0].Outcome != "succeeded" {
		t.Fatalf("the running child's call belongs in the transient record, started and done: %+v", events)
	}
	for name, digest := range before {
		if after := projectionSnapshot(t, app.store); after[name] != digest {
			t.Errorf("table %s changed under the freeze: %s -> %s", name, digest, after[name])
		}
	}
}

func TestARuntimeSafeTurnCarriesItsTriggersSection(t *testing.T) {
	var mu sync.Mutex
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		_ = json.Unmarshal(body, &req)
		var all []string
		for _, m := range req.Messages {
			var s string
			if json.Unmarshal(m.Content, &s) != nil {
				s = string(m.Content)
			}
			all = append(all, s)
		}
		mu.Lock()
		requests = append(requests, strings.Join(all, "\n"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, textReply("noted"))
	}))
	defer srv.Close()
	app, _ := liveIdentity(t, t.TempDir(), srv.URL)
	defer stopApp(t, app)
	sent := func(marker string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := app.handleMessage(ctx, marker+" are you there?"); err != nil {
			t.Fatalf("%s turn: %v", marker, err)
		}
		mu.Lock()
		defer mu.Unlock()
		for i := len(requests) - 1; i >= 0; i-- {
			if strings.Contains(requests[i], marker) {
				return requests[i]
			}
		}
		t.Fatalf("no request carried %s", marker)
		return ""
	}

	if normal := sent("NORMALTURN"); strings.Contains(normal, "# SAFE MODE") {
		t.Fatalf("a NORMAL turn carries a SAFE section:\n%s", normal)
	}

	door := &ledgerAdapter{Ledger: app.ledger, kp: app.keyPair, st: &failingProjection{}, onIntegrity: app.integrityLost}
	if _, err := door.Append(ledger.EventExperienceCreate, 3, map[string]any{"id": "exp_diverged", "content": "observed"}, ""); err == nil {
		t.Fatal("rig: the failing projection took the event")
	}
	if reason, safe := app.SafeMode(); !safe || !strings.HasPrefix(reason, safePostures[safeDivergence].what+": ") {
		t.Fatalf("rig: the divergence did not enter SAFE as one: %q", reason)
	}

	safe := sent("SAFETURN")
	for _, want := range []string{safePostures[safeDivergence].why, "As this runtime states it: ", "did not materialize", safeHolds(false)} {
		if !strings.Contains(safe, want) {
			t.Errorf("the SAFE turn's request does not carry %q:\n%s", want, safe)
		}
	}
	if n := strings.Count(safe, "# SAFE MODE"); n != 1 {
		t.Errorf("the request carries %d SAFE sections, want 1", n)
	}
	for tr := safeRecord; tr <= lastSafeTrigger; tr++ {
		if tr != safeDivergence && strings.Contains(safe, safePostures[tr].why) {
			t.Errorf("the request carries trigger %d's paragraph", tr)
		}
	}
	if strings.Contains(safe, safeHolds(true)) {
		t.Error("a runtime SAFE is told the boot posture's list")
	}

	// UNDER PRESSURE IT STAYS WHOLE: the same turn composed with almost
	// no room folds or drops what may yield, and keeps the SAFE section.
	facts, err := app.buildTurnFacts(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	work, err := app.buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	notice := app.safeTurnSection()
	p, err := app.composer.ComposeTurn(work, facts, notice, app.composer.MaxTokens())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Turn, "# Not Shown") {
		t.Fatalf("rig: nothing yielded, so this was no pressure:\n%s", p.Turn)
	}
	if notice == "" || !strings.Contains(p.Turn, notice) {
		t.Fatalf("under pressure the SAFE section did not survive whole:\n%s", p.Turn)
	}
}
