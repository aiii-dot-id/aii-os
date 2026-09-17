package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// .
// .
// .
// .
// .
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

	// .
	if !a.TryBeginTurn() {
		t.Fatal("could not take the turn gate")
	}
	if err := a.observeVoice(context.Background(), heardUtterance{Source: "speech engine", Text: "Ember, can you hear me?", Answer: true, SessionID: "vs-7", Gen: 3, Operator: true}); err != nil {
		t.Fatal(err)
	}
	// .
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
	if err != nil || latest == nil || !strings.HasPrefix(latest.Content, voiceMarker+"Ember, can you hear me?") {
		t.Fatalf("the operator's spoken words must be operator evidence, marked as spoken: %+v %v", latest, err)
	}
	// .
	// .
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
	// .
	ve, ok := voiceEventFor(pluginhost.Event{Type: "transcript_final", SessionID: "vs-7", Raw: []byte(`{"text":"hi"}`)}, false)
	if !ok || !ve.Operator || !ve.Final {
		t.Fatalf("transcript event = %+v", ve)
	}
	// .
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

// .
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
	releaseVoice(entries, "lost again")
	done2 := make(chan struct{})
	go func() { h.work.Wait(); close(done2) }()
	select {
	case <-done2:
	case <-time.After(2 * time.Second):
		t.Fatal("a lost turn must release the session's work, once")
	}
}

// .
// .
// .
// .
// .
// .
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

	// .
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
	// .
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
	defer mu.Unlock()
	if len(woke) != 1 || woke[0] != roleOperator+": "+voiceMarker+"are you there?" {
		t.Fatalf("the operator's words ran as the operator's turn: %v", woke)
	}
	if !a.TryBeginTurn() {
		t.Fatal("the gate was released after the turn")
	}
	a.releaseTurn()

	// .
	saved := voicePassWait
	voicePassWait = 50 * time.Millisecond
	t.Cleanup(func() { voicePassWait = saved })
	if !a.TryBeginTurn() {
		t.Fatal("gate")
	}
	a.turnMu.Lock()
	a.turnFacility = true
	a.turnMu.Unlock()
	err := a.observeVoice(context.Background(), heardUtterance{Source: "speech engine", Text: "still there?", Answer: true, SessionID: "vs-9", Sequence: 5, Gen: 1, Operator: true})
	if err == nil || !strings.Contains(err.Error(), "did not end in time") {
		t.Fatalf("a pass that never ends refuses by name: %v", err)
	}
	waited := make(chan struct{})
	go func() { h.work.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(2 * time.Second):
		t.Fatal("the refused words released the session's work")
	}
	a.releaseTurn()
}
