package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

type contScript struct {
	mu   sync.Mutex
	reqs []string
}

func (o *contScript) add(body string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.reqs = append(o.reqs, body)
	return len(o.reqs)
}

func (o *contScript) snapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.reqs...)
}

// The capped turn hands off: declared steps=1 buys 2 remaining calls, the turn
// keeps reading past its budget, the grace elapses, the forced ending
// flags — and a continuation turn arrives at the model UNPROMPTED, in
// the system voice, naming the checkpoint. The chain counter resets
// when that leg ends naturally.
// jsonStringFrom returns the JSON string value that contains marker,
// from the marker to the string's closing quote — one message's
// content out of a raw request body, escaped as the wire carries it.
func jsonStringFrom(body, marker string) string {
	i := strings.Index(body, marker)
	if i < 0 {
		return ""
	}
	for j := i; j < len(body); j++ {
		if body[j] == '\\' {
			j++ // skip the escaped character
			continue
		}
		if body[j] == '"' {
			return body[i:j]
		}
	}
	return body[i:]
}

func nothingContinues(calls int, why string) string {
	return fmt.Sprintf("[This turn ended at its budget after %d tool calls. Nothing continues by itself: %s. Ask again to continue.]", calls, why)
}

// recordedReply is the reply the identity's history holds that begins
// with said: what the next turn's request, and recall, will read.
func recordedReply(t *testing.T, a *App, said string) string {
	t.Helper()
	var content string
	if err := a.store.DB().QueryRow(`SELECT content FROM conversations WHERE role='resident' AND substr(content,1,?)=? ORDER BY turn_seq DESC LIMIT 1`,
		len(said), said).Scan(&content); err != nil {
		t.Fatalf("the record holds no reply beginning %q: %v", said, err)
	}
	return content
}

// promisesInTheRecord counts the identity's recorded replies that say the
// work continues in a fresh turn.
func promisesInTheRecord(t *testing.T, a *App) int {
	t.Helper()
	var n int
	if err := a.store.DB().QueryRow(`SELECT COUNT(*) FROM conversations WHERE role='resident' AND instr(content,'continues automatically in a fresh turn')>0`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCappedTurnContinuesInAFreshTurn(t *testing.T) {
	app, dir := dispatchApp(t)
	defer stopApp(t, app)

	if err := writeFileForTest(filepath.Join(dir, "probe.txt"), "leg probe"); err != nil {
		t.Fatal(err)
	}
	readArgs, _ := json.Marshal(map[string]string{"file_path": filepath.Join(dir, "probe.txt")})

	obs := &contScript{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := obs.add(string(body))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case n == 1: // two setup calls; the declaration buys 2 remaining calls
			_, _ = io.WriteString(w, callReply("",
				toolCall("c1a", "work", `{"action":"start","description":"leg one"}`),
				toolCall("c1b", "work", `{"action":"update","plan":"read the probe","steps":1}`),
			))
		case n <= 7: // two remaining calls, then four grace rounds
			_, _ = io.WriteString(w, callReply("", toolCall(fmt.Sprintf("c%d", n), "read", string(readArgs))))
		case n == 8: // the forced no-tools ending of leg one
			_, _ = io.WriteString(w, textReply("leg one checkpointed"))
		default: // the continuation turn: answer and end naturally
			_, _ = io.WriteString(w, textReply("resuming from the checkpoint"))
		}
	}))
	defer srv.Close()

	app.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1}))

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := app.acquireTurn(ctx); err != nil {
		t.Fatalf("acquire turn: %v", err)
	}
	reply, err := app.observeChat(ctx, "start leg one", func(kind, name, args string) {})
	if err != nil {
		t.Fatalf("turn failed: %v", err)
	}
	// A leg was scheduled, so the loop's own sentence stands, to the byte
	// (two setup calls and six reads), and it is what the record holds.
	const said = "leg one checkpointed\n\n[This turn ended at its declared budget after 8 tool calls; the work continues automatically in a fresh turn.]"
	if reply != said {
		t.Errorf("the continued turn's reply = %q, want %q", reply, said)
	}
	if got := recordedReply(t, app, "leg one checkpointed"); got != said {
		t.Errorf("the record holds %q for the continued turn, want %q", got, said)
	}

	// The continuation runs in the background once the gate frees; wait
	// for its request to reach the model, then for its turn to finish
	// (the chain counter resetting is the finish line).
	deadline := time.Now().Add(30 * time.Second)
	var contReq string
	for time.Now().Before(deadline) {
		for _, b := range obs.snapshot() {
			if strings.Contains(b, "budget checkpoint — continuation 1") {
				contReq = b
			}
		}
		if contReq != "" {
			app.turn.mu.Lock()
			chain := app.turn.contChain
			app.turn.mu.Unlock()
			if chain == 0 {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if contReq == "" {
		t.Fatal("no continuation turn reached the model — the capped turn died at its cap")
	}
	// The fact names how the leg ended and carries the resume card: the
	// plan the capped leg authored reaches the fresh leg in the one
	// message no accordion folds, not only in the Ring 4 section that
	// yields first under pressure. Asserted on the fact's own message —
	// Ring 4 renders the same card into the system prompt, and a check
	// over the whole request would pass on that copy.
	fact := jsonStringFrom(contReq, "budget checkpoint — continuation 1")
	for _, want := range []string{"still active", "declare steps= for this leg", "ended at its declared tool budget", "Resume point: read the probe"} {
		if !strings.Contains(fact, want) {
			t.Errorf("the continuation fact omits %q: %q", want, fact)
		}
	}
	app.turn.mu.Lock()
	chain := app.turn.contChain
	app.turn.mu.Unlock()
	if chain != 0 {
		t.Errorf("contChain = %d after a natural leg, want 0 — the chain never resets and would exhaust", chain)
	}

	// Both legs minted rows: the capped one deep, the continuation light.
	var rows int
	if err := app.store.DB().QueryRow(`SELECT COUNT(*) FROM turn_metrics`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows < 2 {
		t.Errorf("turn_metrics holds %d row(s), want >= 2 — the continuation leg left no measured trace", rows)
	}
}

func TestATokenFencedTurnContinuesInAFreshTurn(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	// A fence the first call's spend crosses; the rounds stay as booted.
	app.conv.SetTurnBounds(app.configSnapshot().Agency.MaxToolRounds, 500, conversation.Checkpoint{})

	obs := &contScript{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := obs.add(string(body))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case n == 1: // open the work, spending past the fence in one call
			_, _ = io.WriteString(w, chatReply("", "tool_calls", []string{
				toolCall("f1a", "work", `{"action":"start","description":"fenced work"}`),
				toolCall("f1b", "work", `{"action":"update","plan":"read past the fence"}`),
			}, replyUsage{prompt: 1_000, completion: 5}))
		case n == 2: // the fence's one bounded wrap-up, no tools
			_, _ = io.WriteString(w, textReply("fenced here"))
		default: // the continuation turn: answer and end naturally
			_, _ = io.WriteString(w, textReply("resuming after the fence"))
		}
	}))
	defer srv.Close()
	app.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1}))

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := app.acquireTurn(ctx); err != nil {
		t.Fatalf("acquire turn: %v", err)
	}
	reply, err := app.observeChat(ctx, "start the fenced work", func(kind, name, args string) {})
	if err != nil {
		t.Fatalf("turn failed: %v", err)
	}
	if !strings.Contains(reply, "token budget (500) is spent") {
		t.Fatalf("rig: the turn did not end at its token fence: %q", reply)
	}

	deadline := time.Now().Add(30 * time.Second)
	var contReq string
	for time.Now().Before(deadline) {
		for _, b := range obs.snapshot() {
			if strings.Contains(b, "budget checkpoint — continuation 1") {
				contReq = b
			}
		}
		if contReq != "" {
			app.turn.mu.Lock()
			chain := app.turn.contChain
			app.turn.mu.Unlock()
			if chain == 0 {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if contReq == "" {
		t.Fatal("no continuation turn reached the model — the fenced turn died at its fence")
	}
	fact := jsonStringFrom(contReq, "budget checkpoint — continuation 1")
	for _, want := range []string{"ended because it spent its token budget", "still active", "Resume point: read past the fence"} {
		if !strings.Contains(fact, want) {
			t.Errorf("the continuation fact omits %q: %q", want, fact)
		}
	}
	for _, wrong := range []string{"declared tool budget", "context filled"} {
		if strings.Contains(fact, wrong) {
			t.Errorf("the continuation fact names an ending that did not happen (%q): %q", wrong, fact)
		}
	}
}

// WORDS LEFT BY A CAPPED TURN ARE READ BEFORE ITS CONTINUATION. A turn that
// ends at a budget starts its continuation leg waiting on the gate before it
// ends. Words the operator says during its closing call, which no tool
// boundary follows, used to wait behind that leg: the work went on for a
// whole leg without the correction it had been sent. Their turn runs first
// now, on the token the capped turn held, and the leg that follows has them
// in its history.
func TestWordsLeftByACappedTurnAreReadBeforeItsContinuation(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	// A fence the first call's spend crosses; the rounds stay as booted.
	app.conv.SetTurnBounds(app.configSnapshot().Agency.MaxToolRounds, 500, conversation.Checkpoint{})

	const words = "stop, that is the wrong repository"
	const leg = "budget checkpoint — continuation 1"
	obs := &contScript{}
	steered := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := obs.add(string(body))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case n == 1: // open the work, spending past the fence in one call
			_, _ = io.WriteString(w, chatReply("", "tool_calls", []string{
				toolCall("w1a", "work", `{"action":"start","description":"fenced work"}`),
				toolCall("w1b", "work", `{"action":"update","plan":"read past the fence"}`),
			}, replyUsage{prompt: 1_000, completion: 5}))
		case n == 2: // the fence's closing call: the operator speaks, and no boundary follows
			ok, err := app.Steer(words)
			if err == nil && !ok {
				err = fmt.Errorf("no turn was running to take the words")
			}
			steered <- err
			_, _ = io.WriteString(w, textReply("fenced here"))
		default: // the words' own turn and the continuation leg: each ends naturally
			_, _ = io.WriteString(w, textReply("heard"))
		}
	}))
	defer srv.Close()
	app.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1}))

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := app.acquireTurn(ctx); err != nil {
		t.Fatalf("acquire turn: %v", err)
	}
	if _, err := app.observeChat(ctx, "start the fenced work", func(kind, name, args string) {}); err != nil {
		t.Fatalf("turn failed: %v", err)
	}
	if err := <-steered; err != nil {
		t.Fatal(err)
	}

	// The continuation leg's request, wherever it falls among the requests.
	deadline := time.Now().Add(30 * time.Second)
	var reqs []string
	cont := -1
	for cont < 0 && time.Now().Before(deadline) {
		reqs = obs.snapshot()
		for i, b := range reqs {
			if strings.Contains(b, leg) {
				cont = i
				break
			}
		}
		if cont < 0 {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if cont < 0 {
		t.Fatal("no continuation leg reached the model")
	}
	// Words reach a request through the record alone, and only their own
	// turn records these: a leg that holds them ran after it.
	if !strings.Contains(reqs[cont], words) {
		t.Fatalf("the continuation leg (request %d of %d) went on with the work before the words the capped turn left were read", cont+1, len(reqs))
	}
	// The leg holds the gate until it ends; the teardown follows that.
	for app.TurnActive() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
}

// Delivered means done: a turn that ends at its cap AFTER delivering
// its session has nothing to continue — no wake, no chain.
func TestDeliveredSessionDoesNotContinue(t *testing.T) {
	app, dir := dispatchApp(t)
	defer stopApp(t, app)

	if err := writeFileForTest(filepath.Join(dir, "probe.txt"), "leg probe"); err != nil {
		t.Fatal(err)
	}
	readArgs, _ := json.Marshal(map[string]string{"file_path": filepath.Join(dir, "probe.txt")})

	obs := &contScript{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := obs.add(string(body))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case n == 1:
			_, _ = io.WriteString(w, callReply("",
				toolCall("c1a", "work", `{"action":"start","description":"delivered leg"}`),
				toolCall("c1b", "work", `{"action":"update","plan":"read then deliver","steps":1}`),
			))
		case n <= 6:
			_, _ = io.WriteString(w, callReply("", toolCall(fmt.Sprintf("c%d", n), "read", string(readArgs))))
		case n == 7: // deliver INSIDE the grace window, still tool-calling
			_, _ = io.WriteString(w, callReply("", toolCall("c7", "work", `{"action":"deliver","result":"served: done before the cap"}`)))
		case n == 8:
			_, _ = io.WriteString(w, textReply("delivered and capped"))
		default:
			_, _ = io.WriteString(w, textReply("this turn should never have happened"))
		}
	}))
	defer srv.Close()

	app.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1}))

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := app.acquireTurn(ctx); err != nil {
		t.Fatalf("acquire turn: %v", err)
	}
	reply, err := app.observeChat(ctx, "start the delivered leg", func(kind, name, args string) {})
	if err != nil {
		t.Fatalf("turn failed: %v", err)
	}
	// And the reply says so. It used to end on the loop's sentence, "the
	// work continues automatically in a fresh turn", which the loop writes
	// before the app has looked for a session to continue.
	said := "delivered and capped\n\n" + nothingContinues(8, "no work session is active")
	if reply != said {
		t.Errorf("the reply of a capped turn that delivered its session = %q, want %q", reply, said)
	}
	if got := recordedReply(t, app, "delivered and capped"); got != said {
		t.Errorf("the record holds %q, want the reply as it was given: %q", got, said)
	}
	if n := promisesInTheRecord(t, app); n != 0 {
		t.Errorf("%d recorded reply(ies) promise a fresh turn nothing scheduled", n)
	}

	// Absence with a stated window: give a wrongly-scheduled wake ample
	// time to reach the model, then read the count.
	time.Sleep(2 * time.Second)
	for _, b := range obs.snapshot() {
		if strings.Contains(b, "budget checkpoint") {
			t.Fatal("a continuation turn fired for a delivered session")
		}
	}
	app.turn.mu.Lock()
	chain := app.turn.contChain
	app.turn.mu.Unlock()
	if chain != 0 {
		t.Errorf("contChain = %d for a delivered session, want 0", chain)
	}
}

func TestStopCancelsTheInFlightTurn(t *testing.T) {
	app, _ := dispatchApp(t)

	// Only the PROBE turn hangs. A server that hangs every request
	// also captures whatever background turn (metabolism, a brief)
	// fires mid-test — and that turn's own fresh budget is exactly
	// what Stop would then sit behind, deadlocking the test instead
	// of testing the deadlock.
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "hang forever") {
			<-r.Context().Done() // the probe blocks until cancelled
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, textReply("ok"))
	}))
	defer hang.Close()
	app.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: hang.URL, Model: "fake", MaxOutputTokens: 64, Retries: -1}))

	turnDone := make(chan error, 1)
	go func() {
		ctx := context.Background()
		if err := app.acquireTurn(ctx); err != nil {
			turnDone <- err
			return
		}
		_, err := app.observeChat(ctx, "hang forever", func(kind, name, args string) {})
		turnDone <- err
	}()

	// The turn is in flight once the hanging server holds a request.
	deadline := time.Now().Add(5 * time.Second)
	for app.turnCancelRegistered() == false && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}

	stop := stopAppAside(app)
	select {
	case <-stop.done:
		stop.check(t)
	case <-time.After(15 * time.Second):
		t.Fatal("Stop sat behind the in-flight turn — the cancel hook did not fire")
	}
	select {
	case err := <-turnDone:
		if err == nil {
			t.Fatal("the hung turn reported success — it can only have been cancelled")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the turn never ended after Stop")
	}
}

// THE CHAIN IS BOUNDED AT EIGHT. Eight consecutive capped turns each hand their work
// to a fresh leg; the ninth stands down, so a stuck task cannot chain for
// ever. The turn gate is held throughout: the scheduled legs wait behind
// it and none reaches a model, because what is under test is the
// decision, not the legs.
func TestTheContinuationChainStandsDownAfterEightLegs(t *testing.T) {
	app, _ := dispatchApp(t)
	if !agencyOn(app.configSnapshot().Agency.TurnContinuation) {
		t.Fatal("rig: turn continuation is on unless the config says otherwise")
	}
	if err := app.store.StartWorkSession(t.Context(), "ws_chain", "a task that never finishes"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := app.acquireTurn(ctx); err != nil {
		t.Fatalf("acquire turn: %v", err)
	}
	// Stop while the legs still wait: the shutdown cancel reaches them
	// first, and only then is the gate let go, so none of them runs.
	t.Cleanup(func() {
		stop := stopAppAside(app)
		<-app.bgCtx.Done()
		app.releaseTurn()
		select {
		case <-stop.done:
			stop.check(t)
		case <-time.After(15 * time.Second):
			t.Error("Stop did not return after the waiting legs were cancelled")
		}
	})

	for leg := 1; leg <= 8; leg++ {
		if scheduled, why := app.scheduleContinuation(capDeclared, ""); !scheduled || why != "" {
			t.Fatalf("capped turn %d was not continued (%q); the bound is eight legs", leg, why)
		}
	}
	scheduled, why := app.scheduleContinuation(capDeclared, "")
	if scheduled {
		t.Fatal("a ninth consecutive capped turn was continued: the chain has no bound")
	}
	if want := "the host has already started 8 fresh turns in a row, its limit (agency.max_turn_continuations)"; why != want {
		t.Errorf("the ninth capped turn is told %q, want %q", why, want)
	}
	// Stood down, it stays down for turns nobody asked for: a tenth is
	// refused too.
	if scheduled, _ := app.scheduleContinuation(capDeclared, ""); scheduled {
		t.Fatal("the chain stood down and then continued with nobody having looked")
	}

	app.resetAsk()
	for leg := 1; leg <= 8; leg++ {
		if scheduled, why := app.scheduleContinuation(capDeclared, ""); !scheduled {
			t.Fatalf("after the operator's own message capped turn %d was refused its continuation (%q); a new chain is eight legs", leg, why)
		}
	}
	if scheduled, _ := app.scheduleContinuation(capDeclared, ""); scheduled {
		t.Fatal("the new chain has no bound: a ninth leg after the operator's message was continued")
	}
}

func TestACappedTurnNothingContinuesSaysSoAndWhy(t *testing.T) {
	// declared ends a turn at its declared budget after eight tool calls;
	// fenced ends one at its token fence after two.
	declared := func(t *testing.T, app *App, dir string) (script func(n int) string, final string) {
		if err := writeFileForTest(filepath.Join(dir, "probe.txt"), "leg probe"); err != nil {
			t.Fatal(err)
		}
		readArgs, _ := json.Marshal(map[string]string{"file_path": filepath.Join(dir, "probe.txt")})
		return func(n int) string {
			switch {
			case n == 1:
				return callReply("",
					toolCall("c1a", "work", `{"action":"start","description":"capped work"}`),
					toolCall("c1b", "work", `{"action":"update","plan":"read the probe","steps":1}`))
			case n <= 7:
				return callReply("", toolCall(fmt.Sprintf("c%d", n), "read", string(readArgs)))
			}
			return textReply("capped here")
		}, "capped here"
	}
	fenced := func(t *testing.T, app *App, _ string) (script func(n int) string, final string) {
		app.conv.SetTurnBounds(app.configSnapshot().Agency.MaxToolRounds, 500, conversation.Checkpoint{})
		return func(n int) string {
			if n == 1 {
				return chatReply("", "tool_calls", []string{
					toolCall("f1a", "work", `{"action":"start","description":"capped work"}`),
					toolCall("f1b", "work", `{"action":"update","plan":"read past the fence"}`),
				}, replyUsage{prompt: 1_000, completion: 5})
			}
			return textReply("capped here")
		}, "capped here"
	}
	switchedOff := func(app *App) {
		off := false
		app.cfgMu.Lock()
		app.cfg.Agency.TurnContinuation = &off
		app.cfgMu.Unlock()
	}
	chainSpent := func(app *App) {
		app.turn.mu.Lock()
		app.turn.contChain = defaultMaxTurnContinuations
		app.turn.mu.Unlock()
	}
	for _, c := range []struct {
		name    string
		ending  func(*testing.T, *App, string) (func(int) string, string)
		refuse  func(*App)
		midTurn bool // refuse once the turn has begun
		calls   int
		why     string
	}{
		{name: "switched off", ending: declared, refuse: switchedOff, calls: 8,
			why: "continuation is switched off (agency.turn_continuation)"},
		// The operator's message starts a new chain as its turn begins
		// (resetAsk), so the chain is spent while the turn runs.
		{name: "chain spent", ending: declared, refuse: chainSpent, midTurn: true, calls: 8,
			why: "the host has already started 8 fresh turns in a row, its limit (agency.max_turn_continuations)"},
		{name: "switched off at the token fence", ending: fenced, refuse: switchedOff, calls: 2,
			why: "continuation is switched off (agency.turn_continuation)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			app, dir := dispatchApp(t)
			defer stopApp(t, app)
			script, final := c.ending(t, app, dir)
			obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) {
				if c.midTurn && n == 2 {
					c.refuse(app)
				}
				_, _ = io.WriteString(w, script(n))
			})

			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			if err := app.acquireTurn(ctx); err != nil {
				t.Fatalf("acquire turn: %v", err)
			}
			if !c.midTurn {
				c.refuse(app) // with the gate held: no other turn's ending moves the chain
			}
			reply, err := app.observeChat(ctx, "start the capped work", func(kind, name, args string) {})
			if err != nil {
				t.Fatalf("turn failed: %v", err)
			}

			// The identity's words, then the one line, at every ending: no
			// promise, and nothing kept of the line it stands in place of.
			if want := final + "\n\n" + nothingContinues(c.calls, c.why); reply != want {
				t.Fatalf("the reply = %q, want %q", reply, want)
			}
			// The record holds this reply, and no row holds the other line.
			if got := recordedReply(t, app, final); got != reply {
				t.Errorf("the record holds %q, want the reply as it was given: %q", got, reply)
			}
			if n := promisesInTheRecord(t, app); n != 0 {
				t.Errorf("%d recorded reply(ies) promise a fresh turn nothing scheduled", n)
			}
			for _, b := range obs.snapshot() {
				if strings.Contains(b, "budget checkpoint — continuation") {
					t.Fatal("a continuation leg reached the model")
				}
			}
		})
	}
}

func TestALegThatDoesNotRunIsToldToTheOperator(t *testing.T) {
	for _, c := range []struct {
		name string
		leg  func(n int, w http.ResponseWriter) // the leg's requests, from its first (n == 3)
		ran  bool
	}{
		{name: "the provider refuses its first request", leg: func(_ int, w http.ResponseWriter) {
			w.WriteHeader(529)
			_, _ = io.WriteString(w, `{"error":{"message":"overloaded"}}`)
		}},
		{name: "the provider fails it after a tool call and words", ran: true, leg: func(n int, w http.ResponseWriter) {
			if n == 3 {
				_, _ = io.WriteString(w, callReply("the second part, begun", toolCall("l2a", "work", `{"action":"update","plan":"the second part"}`)))
				return
			}
			w.WriteHeader(529)
			_, _ = io.WriteString(w, `{"error":{"message":"overloaded"}}`)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			app, _ := dispatchApp(t)
			defer stopApp(t, app)
			app.conv.SetTurnBounds(app.configSnapshot().Agency.MaxToolRounds, 500, conversation.Checkpoint{})

			fixtureModel(t, app, func(n int, w http.ResponseWriter) {
				switch {
				case n == 1: // open the work, spending past the fence in one call
					_, _ = io.WriteString(w, chatReply("", "tool_calls", []string{
						toolCall("l1a", "work", `{"action":"start","description":"fenced work"}`),
						toolCall("l1b", "work", `{"action":"update","plan":"read past the fence"}`),
					}, replyUsage{prompt: 1_000, completion: 5}))
				case n == 2: // the fence's one bounded wrap-up
					_, _ = io.WriteString(w, textReply("fenced here"))
				default:
					c.leg(n, w)
				}
			})

			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			if err := app.acquireTurn(ctx); err != nil {
				t.Fatalf("acquire turn: %v", err)
			}
			reply, err := app.observeChat(ctx, "start the fenced work", func(kind, name, args string) {})
			if err != nil {
				t.Fatalf("turn failed: %v", err)
			}
			if !strings.HasSuffix(reply, "while its work session is still active, the work continues automatically in a fresh turn.]") {
				t.Fatalf("rig: the capped turn was not handed to a fresh turn: %q", reply)
			}

			if c.ran {
				// The leg's words are in the record beside the substrate's
				// marker, and that is the whole of what is said of it.
				var marker string
				waitUntil(t, func() bool {
					turns, err := app.store.RecentTurnsIncludingSystem(50)
					if err != nil {
						t.Fatal(err)
					}
					for _, turn := range turns {
						if turn.Role == "system" && strings.Contains(turn.Content, "the reply above is incomplete") {
							marker = turn.Content
						}
					}
					return marker != ""
				}, "the interrupted leg's marker in the record")
				if got := recordedReply(t, app, "the second part, begun"); got != "the second part, begun" {
					t.Errorf("the record holds the leg's words as %q", got)
				}
				if !strings.Contains(marker, "529") {
					t.Errorf("the marker does not name the provider's failure: %q", marker)
				}
				// The leg holds the gate until it has returned, its notice
				// (if it wrote one) before that.
				if err := app.acquireTurn(ctx); err != nil {
					t.Fatal(err)
				}
				app.releaseTurn()
				if got := outboxReply(t, app, "continuation_"); got != "" {
					t.Errorf("a leg that ran a tool and spoke is told to the operator as %q", got)
				}
				return
			}
			var notice string
			waitUntil(t, func() bool {
				notice = outboxReply(t, app, "continuation_")
				return notice != ""
			}, "the operator's notice that the leg did not run")
			if !strings.HasPrefix(notice, "The next part of fenced work did not run: ") || !strings.HasSuffix(notice, ".") {
				t.Errorf("the operator is told %q, want \"The next part of fenced work did not run: <cause>.\"", notice)
			}
			if !strings.Contains(notice, "529") {
				t.Errorf("the notice does not carry the provider's refusal as the cause: %q", notice)
			}
			// The failed leg left no words of its own on the outbox.
			if got := outboxReply(t, app, "wake_cont_"); got != "" {
				t.Errorf("a leg that failed before a word delivered %q", got)
			}
		})
	}
}
