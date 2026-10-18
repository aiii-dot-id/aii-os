package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

func TestTheOperatorsSpokenWordsAreTheOperatorsAndAreAnsweredAloud(t *testing.T) {
	a := newVoiceApp(t)
	h := &voiceHandle{id: "vs-7", drained: make(chan struct{})}
	a.voiceSessions.Store("vs-7", h)
	a.voiceModes.Store("vs-7", true)
	var mu sync.Mutex
	var spoken []string
	prev := voiceSynthesize
	voiceSynthesize = func(app *App, _ context.Context, session string, gen uint64, reply string) replyVerdict {
		mu.Lock()
		spoken = append(spoken, session+"/"+reply)
		mu.Unlock()
		h.replyOutcome.Store("reply admitted")
		return replyAdmitted
	}
	defer func() { voiceSynthesize = prev }()

	// A turn is running; the operator speaks.
	if !a.TryBeginTurn() {
		t.Fatal("could not take the turn gate")
	}
	if err := a.observeVoice(context.Background(), heardUtterance{Source: "speech engine", Text: "Vega, can you hear me?", Answer: true, SessionID: "vs-7", Gen: 3, Operator: true}); err != nil {
		t.Fatal(err)
	}
	// Finish must wait: the work is held.
	waited := make(chan struct{})
	go func() { h.work.Wait(); close(waited) }()
	select {
	case <-waited:
		t.Fatal("the session's work was released before the turn answered")
	case <-time.After(50 * time.Millisecond):
	}
	if said := a.DrainSteering(); len(said) != 1 || !strings.Contains(said[0], "can you hear me") {
		t.Fatalf("the words did not reach the running turn: %v", said)
	}
	latest, err := a.store.GetLatestOperatorTurn()
	if err != nil || latest == nil || !strings.HasPrefix(latest.Content, voiceMarker+"Vega, can you hear me?") {
		t.Fatalf("the operator's spoken words must be operator evidence, marked as spoken: %+v %v", latest, err)
	}
	// The turn ends with a reply: spoken to the session, the work released,
	// and the turn's own path told not to print it twice.
	a.settleVoice(context.Background(), "Yes, I hear you.")
	a.releaseTurn()
	select {
	case <-waited:
	case <-time.After(2 * time.Second):
		t.Fatal("the session's work was not released after the reply")
	}
	mu.Lock()
	got := strings.Join(spoken, ",")
	mu.Unlock()
	if got != "vs-7/Yes, I hear you." {
		t.Fatalf("spoken = %q", got)
	}
	if !a.voiceReplyShown.Swap(false) {
		t.Fatal("an admitted spoken reply must mark the bubble as shown")
	}
	// The page is told the transcript is the operator's.
	ve, ok := voiceEventFor(decodeVoiceFrame(pluginhost.Event{Type: "transcript_final", SessionID: "vs-7", Raw: []byte(`{"text":"hi"}`)}), false)
	if !ok || !ve.Operator || !ve.Final {
		t.Fatalf("transcript event = %+v", ve)
	}
	// A plugin-pushed utterance with no session keeps the participant framing.
	if !a.TryBeginTurn() {
		t.Fatal("gate")
	}
	if err := a.observeVoice(context.Background(), heardUtterance{Source: "plugin id.test.voice", Text: "a stranger speaks"}); err != nil {
		t.Fatal(err)
	}
	a.DrainSteering()
	a.releaseTurn()
	if latest, _ := a.store.GetLatestOperatorTurn(); latest == nil || strings.Contains(latest.Content, "stranger") {
		t.Fatalf("a plugin-pushed utterance became operator evidence: %+v", latest)
	}
}

// Silence releases the session without speaking; a lost turn releases too.
func TestASpokenUtteranceIsReleasedWhenTheTurnAnswersWithSilenceOrIsLost(t *testing.T) {
	a := newVoiceApp(t)
	h := &voiceHandle{id: "vs-8", drained: make(chan struct{})}
	a.voiceSessions.Store("vs-8", h)
	a.voiceModes.Store("vs-8", true)
	calls := 0
	prev := voiceSynthesize
	voiceSynthesize = func(*App, context.Context, string, uint64, string) replyVerdict { calls++; return replySuperseded }
	defer func() { voiceSynthesize = prev }()
	if !a.TryBeginTurn() {
		t.Fatal("gate")
	}
	if err := a.observeVoice(context.Background(), heardUtterance{Text: "anything", Answer: true, SessionID: "vs-8", Gen: 1, Operator: true}); err != nil {
		t.Fatal(err)
	}
	a.DrainSteering()
	a.settleVoice(context.Background(), "   ")
	a.releaseTurn()
	done := make(chan struct{})
	go func() { h.work.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("silence must release the session's work")
	}
	if calls != 0 {
		t.Fatal("silence is not spoken")
	}
	entries := []steerEntry{{role: roleOperator, content: "x", voice: a.voiceBindingFor(heardUtterance{SessionID: "vs-8", Gen: 2})}}
	releaseVoice(entries, "lost")
	releaseVoice(entries, "lost again") // once
	done2 := make(chan struct{})
	go func() { h.work.Wait(); close(done2) }()
	select {
	case <-done2:
	case <-time.After(2 * time.Second):
		t.Fatal("a lost turn must release the session's work, once")
	}
}

func TestSpokenWordsWaitForAnInternalPass(t *testing.T) {
	a := newVoiceApp(t)
	h := &voiceHandle{id: "vs-9", done: make(chan struct{}), drained: make(chan struct{})}
	a.voiceSessions.Store("vs-9", h)
	a.voiceModes.Store("vs-9", true)
	var mu sync.Mutex
	var woke []string
	stubWake(t, func() (string, error) { return "I hear you.", nil })
	prev := voiceWake
	voiceWake = func(app *App, ctx context.Context, role, text string) (string, error) {
		mu.Lock()
		woke = append(woke, role+": "+text)
		mu.Unlock()
		return "I hear you.", nil
	}
	t.Cleanup(func() { voiceWake = prev })
	prevSynth := voiceSynthesize
	voiceSynthesize = func(*App, context.Context, string, uint64, string) replyVerdict { return replyAdmitted }
	t.Cleanup(func() { voiceSynthesize = prevSynth })

	// A facility pass holds the turn.
	if !a.TryBeginTurn() {
		t.Fatal("could not take the turn gate")
	}
	a.turnMu.Lock()
	a.turnFacility = true
	a.turnMu.Unlock()

	result := make(chan error, 1)
	go func() {
		result <- a.observeVoice(context.Background(), heardUtterance{Source: "speech engine", Text: "are you there?", Answer: true, SessionID: "vs-9", Sequence: 4, Gen: 1, Operator: true})
	}()
	select {
	case err := <-result:
		t.Fatalf("the words must wait for the pass, not resolve: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	mu.Lock()
	n := len(woke)
	mu.Unlock()
	if n != 0 {
		t.Fatal("no turn ran while the pass held the gate")
	}
	// The pass ends: the words take the turn and are answered.
	a.releaseTurn()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("the waiting words were answered: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the words never took the turn after the pass ended")
	}
	mu.Lock()
	if len(woke) != 1 || woke[0] != roleOperator+": "+voiceMarker+"are you there?" {
		mu.Unlock()
		t.Fatalf("the operator's words ran as the operator's turn: %v", woke)
	}
	mu.Unlock()
	if !a.TryBeginTurn() {
		t.Fatal("the gate was released after the turn")
	}
	a.releaseTurn()

	// A pass that outlasts the bound: the session's work is given back at
	// the bound, the words wait on, and they are answered when it ends.
	saved := voicePassWait
	voicePassWait = 50 * time.Millisecond
	t.Cleanup(func() { voicePassWait = saved })
	if !a.TryBeginTurn() {
		t.Fatal("gate")
	}
	a.turnMu.Lock()
	a.turnFacility = true
	a.turnMu.Unlock()
	late := make(chan error, 1)
	parked := &passWaitContext{Context: context.Background(), entered: make(chan struct{})}
	go func() {
		late <- a.observeVoice(parked, heardUtterance{Source: "speech engine", Text: "still there?", Answer: true, SessionID: "vs-9", Sequence: 5, Gen: 1, Operator: true})
	}()
	select {
	case <-parked.entered: // the words hold the session's work and are at the wait
	case <-time.After(5 * time.Second):
		t.Fatal("the words never reached the wait for the pass")
	}
	waited := make(chan struct{})
	go func() { h.work.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(2 * time.Second):
		t.Fatal("a pass that outlasted the bound still held the session's work")
	}
	select {
	case err := <-late:
		t.Fatalf("the words must outlast the bound, not resolve at it: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	a.releaseTurn()
	select {
	case err := <-late:
		if err != nil {
			t.Fatalf("the words that outlasted the bound were answered: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the words never took the turn after the long pass ended")
	}
	mu.Lock()
	n = len(woke)
	last := ""
	if n > 0 {
		last = woke[n-1]
	}
	mu.Unlock()
	if n != 2 || last != roleOperator+": "+voiceMarker+"still there?" {
		t.Fatalf("the words that outlasted the bound ran as the operator's turn: %v", woke)
	}
	if !a.TryBeginTurn() {
		t.Fatal("the gate was released after the late turn")
	}
	a.releaseTurn()
}

func TestSpokenWordsBehindAnInternalPassOutliveTheirSession(t *testing.T) {
	a := newVoiceApp(t)
	done := make(chan struct{})
	h := &voiceHandle{id: "vs-10", done: done, drained: make(chan struct{})}
	a.voiceSessions.Store("vs-10", h)
	a.voiceModes.Store("vs-10", true)
	var mu sync.Mutex
	var woke []string
	stubWake(t, func() (string, error) { return "I hear you.", nil })
	prev := voiceWake
	voiceWake = func(app *App, ctx context.Context, role, text string) (string, error) {
		mu.Lock()
		woke = append(woke, role+": "+text)
		mu.Unlock()
		return "I hear you.", nil
	}
	t.Cleanup(func() { voiceWake = prev })
	prevSynth := voiceSynthesize
	voiceSynthesize = func(*App, context.Context, string, uint64, string) replyVerdict { return replySuperseded }
	t.Cleanup(func() { voiceSynthesize = prevSynth })
	hold := func() {
		t.Helper()
		if !a.TryBeginTurn() {
			t.Fatal("could not take the turn gate")
		}
		a.turnMu.Lock()
		a.turnFacility = true
		a.turnMu.Unlock()
	}

	hold()
	result := make(chan error, 1)
	parked := &passWaitContext{Context: context.Background(), entered: make(chan struct{})}
	go func() {
		result <- a.observeVoice(parked, heardUtterance{Source: "speech engine", Text: "are you there?", Answer: true, SessionID: "vs-10", Sequence: 4, Gen: 1, Operator: true})
	}()
	select {
	case <-parked.entered: // the words hold the session's work and are at the wait
	case <-time.After(5 * time.Second):
		t.Fatal("the words never reached the wait for the pass")
	}
	select {
	case err := <-result:
		t.Fatalf("the words must wait for the pass, not resolve: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	// The operator, unanswered, closes the session. The pass still holds the turn.
	close(done)
	released := make(chan struct{})
	go func() { h.work.Wait(); close(released) }()
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("the ended session's work was still held by its waiting words")
	}
	select {
	case err := <-result:
		t.Fatalf("the words were discarded when their session ended, before the pass ended: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	mu.Lock()
	n := len(woke)
	mu.Unlock()
	if n != 0 {
		t.Fatal("a turn ran while the pass held the gate")
	}
	// The pass ends: the words take the turn and are answered.
	a.releaseTurn()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("the words that outlived their session were answered: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the words never took the turn after the pass ended")
	}
	mu.Lock()
	if len(woke) != 1 || woke[0] != roleOperator+": "+voiceMarker+"are you there?" {
		t.Fatalf("the words ran as the operator's turn, once: %v", woke)
	}
	mu.Unlock()
	if !a.TryBeginTurn() {
		t.Fatal("the gate was released after the turn")
	}
	a.releaseTurn()

	// Only stopping ends the wait: the caller's context is cancelled while
	// words wait behind a pass. They are refused by name, no turn runs, and
	// the gate is the pass's still.
	hold()
	cancellable, cancel := context.WithCancel(context.Background())
	ctx := &passWaitContext{Context: cancellable, entered: make(chan struct{})}
	stopped := make(chan error, 1)
	go func() {
		stopped <- a.observeVoice(ctx, heardUtterance{Source: "speech engine", Text: "one more thing", Answer: true, SessionID: "vs-10", Sequence: 5, Gen: 1, Operator: true})
	}()
	select {
	case <-ctx.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the words never reached the wait for the pass")
	}
	cancel()
	select {
	case err := <-stopped:
		if err == nil || !strings.Contains(err.Error(), "the wait for the internal pass was cancelled") {
			t.Fatalf("a cancelled wait refuses by name: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled wait never returned")
	}
	mu.Lock()
	if len(woke) != 1 {
		t.Fatalf("a turn ran for words whose wait was cancelled: %v", woke)
	}
	mu.Unlock()
	if a.TryBeginTurn() {
		t.Fatal("the cancelled wait took the pass's gate")
	}
	a.releaseTurn()
}
