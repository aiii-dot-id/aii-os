package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

type contScript struct {
	mu   sync.Mutex
	reqs []string
	// seen is when and where each request arrived, beside reqs, for the
	// account a failed test logs (logOnFailure).
	seen []contArrival
}

// contArrival is one request's arrival: when, and the path it was sent to.
type contArrival struct {
	at   time.Time
	path string
}

func (o *contScript) add(body string) int { return o.addAt("", body) }

// addAt keeps a request's body with when it arrived and its path.
func (o *contScript) addAt(path, body string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.reqs = append(o.reqs, body)
	o.seen = append(o.seen, contArrival{at: time.Now(), path: path})
	return len(o.reqs)
}

func (o *contScript) logOnFailure(t *testing.T) {
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		o.mu.Lock()
		reqs, seen := append([]string(nil), o.reqs...), append([]contArrival(nil), o.seen...)
		o.mu.Unlock()
		t.Logf("the fixture model received %d request(s):", len(reqs))
		for i, body := range reqs {
			var at, path string
			if i < len(seen) {
				at, path = seen[i].at.Format("15:04:05.000000"), seen[i].path
			}
			t.Logf("  #%d %s %s %s", i+1, at, path, describeRequest(body))
		}
	})
}

// describeRequest is one request body as logOnFailure says it: the tools
// it offered (a turn's, a facility's, or none), its tool_choice, the
// top-level fields a caller may name itself by, and its last message's
// role and first 200 bytes.
func describeRequest(body string) string {
	var req struct {
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
		ToolChoice json.RawMessage `json:"tool_choice"`
		User       string          `json:"user"`
		Metadata   json.RawMessage `json:"metadata"`
		CacheKey   string          `json:"prompt_cache_key"`
		Messages   []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		return fmt.Sprintf("(not a chat request: %v) %.200q", err, body)
	}
	var tools []string
	for _, tl := range req.Tools {
		tools = append(tools, tl.Function.Name)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "tools=%d%v", len(tools), tools)
	if len(req.ToolChoice) > 0 {
		fmt.Fprintf(&b, " tool_choice=%s", req.ToolChoice)
	}
	if req.User != "" {
		fmt.Fprintf(&b, " user=%q", req.User)
	}
	if len(req.Metadata) > 0 {
		fmt.Fprintf(&b, " metadata=%s", req.Metadata)
	}
	if req.CacheKey != "" {
		fmt.Fprintf(&b, " prompt_cache_key=%q", req.CacheKey)
	}
	fmt.Fprintf(&b, " messages=%d", len(req.Messages))
	if n := len(req.Messages); n > 0 {
		last := req.Messages[n-1]
		var text string
		if json.Unmarshal(last.Content, &text) != nil {
			text = string(last.Content) // structured content: as sent
		}
		fmt.Fprintf(&b, " last(%s)=%.200q", last.Role, text)
	}
	return b.String()
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

// scheduledOrWhy is scheduleContinuation as these tests ask it: whether a fresh
// turn was scheduled, and why not.
func (a *App) scheduledOrWhy(reason capReason, from string) (bool, string) {
	e := a.scheduleContinuation(reason, from, false)
	return e.kind == continued, e.why
}

// nothingContinues is the closing line of a capped turn's reply when no
// fresh turn was scheduled for it: the operator's sentence (2026-10-13),
// with the turn's tool calls and why nothing follows.
func nothingContinues(calls int, why string) string {
	return fmt.Sprintf("[This turn ended at its budget after %d tool calls. Nothing continues by itself: %s. Ask again to continue.]", calls, why)
}

// recordedReply is the reply the identity's history holds that begins
// with said: what the next turn's request, and recall, will read.
func recordedReply(t *testing.T, a *App, said string) string {
	t.Helper()
	var id, content string
	if err := a.store.DB().QueryRow(`SELECT id, content FROM conversations WHERE role='resident' AND substr(content,1,?)=? ORDER BY turn_seq DESC LIMIT 1`,
		len(said), said).Scan(&id, &content); err != nil {
		t.Fatalf("the record holds no reply beginning %q: %v", said, err)
	}
	return content + loopNotesBeside(t, a, id)
}

// loopNotesBeside is the loop's notes recorded beside the row id
// (store.ReasonLoopNote), each after a blank line: the turn's ending as
// these tests read it, the reply and then what the loop said of the turn.
func loopNotesBeside(t *testing.T, a *App, id string) string {
	t.Helper()
	rows, err := a.store.DB().Query(`SELECT content FROM conversations WHERE role='system' AND related_id=? AND json_extract(details,'$.reason_code')=? ORDER BY turn_seq`, id, store.ReasonLoopNote)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out string
	for rows.Next() {
		var note string
		if err := rows.Scan(&note); err != nil {
			t.Fatal(err)
		}
		out += "\n\n" + note
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// holdsNote reports whether one of the loop's notes in a record read by
// recordedReply (the reply, then each note after a blank line) begins and
// ends so, wherever it stands among them.
func holdsNote(record, begins, ends string) bool {
	parts := strings.Split(record, "\n\n")
	return slices.ContainsFunc(parts[1:], func(note string) bool {
		return strings.HasPrefix(note, begins) && strings.HasSuffix(note, ends)
	})
}

// promisesInTheRecord counts the loop's notes in the record that say the
// work continues in a fresh turn.
func promisesInTheRecord(t *testing.T, a *App) int {
	t.Helper()
	var n int
	if err := a.store.DB().QueryRow(`SELECT COUNT(*) FROM conversations WHERE instr(content,'continues automatically in a fresh turn')>0 AND (role='resident' OR json_extract(details,'$.reason_code')=?)`, store.ReasonLoopNote).Scan(&n); err != nil {
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
			_, _ = io.WriteString(w, callReply("", toolCall(fmt.Sprintf("c%d", n), "read", probeArgs(dir, n))))
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
	if recordedReply(t, app, reply) != said {
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
	fenceTurnAt(t, app, 500)

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
	if !strings.Contains(recordedReply(t, app, reply), "token budget (500) is spent") {
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
	// The fence ended on a call with the tools withdrawn, so the card does
	// not send the leg back to a plan that call could not update: it
	// asks for the plan to be reconciled with the answer.
	for _, want := range []string{"ended because it spent its token budget", "still active", "Resume point: read past the fence",
		"may be older than that turn's closing answer", "reconcile them with that answer and the recorded results", "Do not replay completed calls"} {
		if !strings.Contains(fact, want) {
			t.Errorf("the continuation fact omits %q: %q", want, fact)
		}
	}
	for _, wrong := range []string{"declared tool budget", "context filled", "resume from plan= and next_move= in your working state"} {
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
	fenceTurnAt(t, app, 500)

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
			_, _ = io.WriteString(w, callReply("", toolCall(fmt.Sprintf("c%d", n), "read", probeArgs(dir, n))))
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
	// And the reply closes the turn. It used to end on the loop's sentence, "the
	// work continues automatically in a fresh turn", and then on "Ask again to
	// continue": neither is so of work that was delivered.
	said := "delivered and capped"
	if recordedReply(t, app, reply) != said {
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

// A SESSION'S WORK IS NOT BOUNDED BY THE CHAIN: work continues. The chain of
// eight (docs/BETA1_CONTRACT.md) bounded consecutive capped turns so a
// stuck task could not chain for ever; what stops an active session's
// work now is read from its record (continueWork: the operator's hold,
// the day's ceiling, a stuck pattern, a fresh turn that records nothing),
// and the bound stands only for a deadline's resume with no session. The
// turn gate is held throughout: the first cap queues the leg, and each
// cap after it finds that leg queued and queues no second one.
func TestASessionsWorkIsNotBoundedByTheChain(t *testing.T) {
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
	// Stop while the leg still waits: the shutdown cancel reaches it
	// first, and only then is the gate let go, so it never runs.
	t.Cleanup(func() {
		stop := stopAppAside(app)
		<-app.bgCtx.Done()
		app.releaseTurn()
		select {
		case <-stop.done:
			stop.check(t)
		case <-time.After(15 * time.Second):
			t.Error("Stop did not return after the waiting leg was cancelled")
		}
	})
	for cap := 1; cap <= limits.DefaultContinuations+2; cap++ {
		if scheduled, why := app.scheduledOrWhy(capDeclared, ""); !scheduled || why != "" {
			t.Fatalf("capped turn %d was not continued (%q); the chain's bound does not govern a session's work", cap, why)
		}
	}
	app.turn.mu.Lock()
	queued := app.turn.legsQueued
	app.turn.mu.Unlock()
	if queued != 1 {
		t.Fatalf("%d legs queued for ten caps, want the one that carries the work on", queued)
	}
	if on := resumesOnOfSession(t, app, "ws_chain"); on != "now" {
		t.Fatalf("the condition is %q, want now", on)
	}
}

// A CAPPED TURN THAT NOTHING CONTINUES SAYS SO, AND WHY (the expert review
// of 2026-10-12, h-turn-01). The loop closes a turn cut by a budget with
// "the work continues automatically in a fresh turn" before the app has
// decided. With continuation switched off no fresh turn was scheduled and
// the reply kept the sentence (the chain's bound, the other case once
// here, no longer governs a session's work: continueWork): shown to the
// operator, and recorded in the identity's history, where nothing kept it.
// The reply now closes on what the app did, and the record holds that
// reply and no other line.
func TestACappedTurnNothingContinuesSaysSoAndWhy(t *testing.T) {
	// declared ends a turn at its declared budget after eight tool calls;
	// fenced ends one at its token fence after two.
	declared := func(t *testing.T, app *App, dir string) (script func(n int) string, final string) {
		if err := writeFileForTest(filepath.Join(dir, "probe.txt"), "leg probe"); err != nil {
			t.Fatal(err)
		}
		return func(n int) string {
			switch {
			case n == 1:
				return callReply("",
					toolCall("c1a", "work", `{"action":"start","description":"capped work"}`),
					toolCall("c1b", "work", `{"action":"update","plan":"read the probe","steps":1}`))
			case n <= 7:
				return callReply("", toolCall(fmt.Sprintf("c%d", n), "read", probeArgs(dir, n)))
			}
			return textReply("capped here")
		}, "capped here"
	}
	fenced := func(t *testing.T, app *App, _ string) (script func(n int) string, final string) {
		fenceTurnAt(t, app, 500)
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

			if reply != final {
				t.Fatalf("the reply = %q, want the identity's words %q", reply, final)
			}
			if want, got := final+"\n\n"+nothingContinues(c.calls, c.why), recordedReply(t, app, final); got != want {
				t.Errorf("the record holds %q, want %q", got, want)
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
			fenceTurnAt(t, app, 500)

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
			// THE CAP'S NOTE STANDS BESIDE THE REPLY, NOT ALWAYS LAST. A leg
			// refused at its first request said nothing, so its own note
			// ("This turn ended without a reply") stands beside the newest
			// words, which are this reply. Once the turn gate is released the
			// leg can land that note before this read. The leg's notice is
			// waited for first, so this read always sees both.
			if !c.ran {
				waitUntil(t, func() bool { return outboxReply(t, app, "continuation_") != "" }, "the operator's notice that the leg did not run")
			}
			if record := recordedReply(t, app, reply); !holdsNote(record, "[This turn's token budget (500) is spent: ", "while its work session is still active, the work continues automatically in a fresh turn.]") {
				t.Fatalf("rig: the capped turn's reply holds no note of its cap that says its work continues in a fresh turn: %q", record)
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

// A HANDOFF IN WORDS IS KEPT (the 04:19Z miss; a review's falsifier). The checkpoint
// tells the model that, while its work session is active, the work continues in a
// fresh turn. A model that answers that ask in words alone, inside the grace, ended
// a turn the host then took for a natural finish: nothing was scheduled and nothing
// said, and the reply's promise stood in the record over a silent identity. Answered
// so, with the session active, a fresh turn starts and both readers are told.
func TestAHandoffInWordsStartsTheFreshTurnItPromises(t *testing.T) {
	app, dir := dispatchApp(t)
	defer stopApp(t, app)
	if err := writeFileForTest(filepath.Join(dir, "probe.txt"), "leg probe"); err != nil {
		t.Fatal(err)
	}
	const handoff = "My work session is still open, so this continues in a fresh turn on its own."
	obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case n == 1: // two setup calls; the declaration buys one remaining call
			_, _ = io.WriteString(w, callReply("",
				toolCall("c1a", "work", `{"action":"start","description":"handed on"}`),
				toolCall("c1b", "work", `{"action":"update","plan":"read the probe","steps":1}`)))
		case n <= 3: // past the declaration: the checkpoint arms on this round
			_, _ = io.WriteString(w, callReply("", toolCall(fmt.Sprintf("c%d", n), "read", probeArgs(dir, n))))
		case n == 4: // the checkpoint's ask, answered in words, inside the grace
			_, _ = io.WriteString(w, textReply(handoff))
		default: // the fresh turn
			_, _ = io.WriteString(w, textReply("the next leg took it up"))
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := app.acquireTurn(ctx); err != nil {
		t.Fatalf("acquire turn: %v", err)
	}
	reply, err := app.observeChat(ctx, "begin", func(kind, name, args string) {})
	if err != nil {
		t.Fatalf("turn failed: %v", err)
	}
	if reply != handoff {
		t.Fatalf("the reply is %q", reply)
	}
	// The identity's record says a fresh turn follows.
	if got := recordedReply(t, app, handoff); !strings.Contains(got, "the work continues automatically in a fresh turn") {
		t.Errorf("the record holds %q: it does not say the work continues", got)
	}
	// And one does.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && len(obs.snapshot()) < 5 {
		time.Sleep(20 * time.Millisecond)
	}
	if n := len(obs.snapshot()); n < 5 {
		t.Fatalf("no fresh turn started after the promise: the model was asked %d times", n)
	}
	// The fresh turn's words are on the page: it moved nothing, so they
	// are not sent (the floor for an unpromised leg, 2026-11-06), and the
	// row beside them says so.
	waitUntil(t, replyRecorded(app, "the next leg took it up"), "the fresh turn's words")
	if r := waitRowBeside(t, app); r != wordsHeldUnpromisedRow {
		t.Errorf("the row beside the fresh turn's words: %q", r)
	}
	if got := outboxReply(t, app, "wake_cont_"); got != "" {
		t.Errorf("a fresh turn that moved nothing reached the operator's outbox: %q", got)
	}
}
