package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// holdPass makes an internal pass hold the identity's turn.
func holdPass(t *testing.T, a *App) {
	t.Helper()
	if !a.TryBeginTurn() {
		t.Fatal("could not take the turn gate for the pass")
	}
	a.turnMu.Lock()
	a.turnFacility = true
	a.turnMu.Unlock()
}

// endableSession is a tracked session whose end the test can bring: the
// engine's word that it is over, which the application then acts on.
func endableSession(a *App, id string, answer bool) (*voiceHandle, *fakeEngineSession, func()) {
	h, f := newTrackedSession(a, id, answer)
	done := make(chan struct{})
	h.done = done
	return h, f, func() {
		close(done)
		a.voiceSessionEnded(h)
	}
}

// waitsForThePass is the host's own line when spoken words meet a pass.
const waitsForThePass = "the operator's words wait for it"

// WORDS KEEP THEIR PLACE IN THE SESSION'S ORDER WHEN THE SESSION ENDS. Two
// utterances are delivered under a speaker policy while a pass holds the
// turn: the first waits for the pass, the second waits behind the first.
// The operator, unanswered, closes the session. The session's work is given
// back at once; when the pass ends the first takes the turn and the second
// is admitted behind it. Neither is dropped.
func TestWordsBehindWaitingWordsKeepTheirPlaceWhenTheirSessionEnds(t *testing.T) {

	for _, road := range []string{"a restricted speaker policy", "policy all, finals that carry a speaker track"} {
		t.Run(road, func(t *testing.T) { wordsKeepTheirPlace(t, road == "a restricted speaker policy") })
	}
}

func wordsKeepTheirPlace(t *testing.T, restricted bool) {
	a := newVoiceApp(t)
	logs := logsink.CaptureForTest(t)
	h, _, end := endableSession(a, "vs-order-ended", true)
	page := &pageLog{}
	// deliver sends one final and what decides it: under the restricted
	// policy the engine's observation, under "all" the host's own wait for
	// the track's speaker running out.
	deliver := func(seq, observation int64, text string) {
		if restricted {
			a.voiceEngineEvent(nil, finalRaw(h.id, seq, text), page.enqueue)
			a.voiceEngineEvent(nil, observationRaw(h.id, observation, map[string]any{"refers_to": seq, "decision": "uncertain"}), page.enqueue)
			return
		}
		a.voiceEngineEvent(nil, uuidFinal(h.id, seq, "track-a", text), page.enqueue)
		a.decideHeld(h, seq, "", nil, true, nil)
	}
	if restricted {
		a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "ignore", UIDs: []string{"visitor"}}
	}
	entered, release := make(chan string, 2), make(chan struct{})
	prior := voiceWake
	voiceWake = func(_ *App, _ context.Context, _, text string) (string, error) {
		entered <- text
		<-release
		return "", nil
	}
	t.Cleanup(func() { voiceWake = prior })
	t.Cleanup(func() { close(release); h.work.Wait() })

	holdPass(t, a)
	deliver(7, 20, "first words")
	holdWait(t, "the first words to reach the wait for the pass", func() bool { return logs.Contains(waitsForThePass) })
	deliver(8, 21, "second words")
	holdWait(t, "the second words to be shown on the page as delivered", func() bool { return page.find("transcript_final", 8) != nil })

	// The operator closes the session. The pass still holds the turn.
	end()
	given := make(chan struct{})
	go func() { h.work.Wait(); close(given) }()
	select {
	case <-given:
	case <-time.After(2 * time.Second):
		t.Error("the ended session's work was still held by words waiting behind the pass")
	}
	select {
	case text := <-entered:
		t.Fatalf("a turn ran while the pass held the gate: %q", text)
	case <-time.After(100 * time.Millisecond):
	}

	// The pass ends: the first words take the turn, the second are admitted
	// behind them into the turn now running.
	a.releaseTurn()
	select {
	case text := <-entered:
		if text != voiceMarker+"first words" {
			t.Fatalf("the later words took the turn ahead of the earlier: %q", text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the first words never took the turn after the pass ended")
	}
	holdWait(t, "the second words to be admitted after their session ended", func() bool {
		a.turnMu.Lock()
		defer a.turnMu.Unlock()
		return len(a.steers) == 1
	})
	said := a.DrainSteering()
	if len(said) != 1 || !strings.Contains(said[0], "second words") {
		t.Fatalf("the second words lost their place when their session ended: %v", said)
	}
}

// FINISH IS NOT HELD BEHIND A PASS PAST THE BOUND, through the observer.
// Words arrive while a pass holds the turn, the operator finishes, and the
// pass outlasts voicePassWait: the session's claim on the reply ends there,
// the drain is requested and says why no spoken reply came, and the words
// are answered when the pass ends.
func TestFinishIsNotHeldBehindAPassPastTheBoundThroughTheObserver(t *testing.T) {
	a := newVoiceApp(t)
	logs := logsink.CaptureForTest(t)
	h, f := newTrackedSession(a, "vs-finish-pass", true)
	saved := voicePassWait
	voicePassWait = 50 * time.Millisecond
	t.Cleanup(func() { voicePassWait = saved })
	var mu sync.Mutex
	var woke []string
	prior := voiceWake
	voiceWake = func(_ *App, _ context.Context, role, text string) (string, error) {
		mu.Lock()
		woke = append(woke, role+": "+text)
		mu.Unlock()
		return "I hear you.", nil
	}
	t.Cleanup(func() { voiceWake = prior })
	priorSynth := voiceSynthesize
	voiceSynthesize = func(*App, context.Context, string, uint64, string) replyVerdict { return replySuperseded }
	t.Cleanup(func() { voiceSynthesize = priorSynth })

	holdPass(t, a)
	a.voiceEngineEvent(nil, finalRaw(h.id, 4, "are you there?"), dropEvent)
	holdWait(t, "the words to reach the wait for the pass", func() bool { return logs.Contains(waitsForThePass) })
	a.voiceInputFinished(h.id)
	select {
	case <-h.drained:
	case <-time.After(3 * time.Second):
		a.releaseTurn()
		t.Fatal("Finish was held behind the pass past the bound: the drain never came while the pass held the turn")
	}
	f.mu.Lock()
	closed := append([]string(nil), f.closed...)
	f.mu.Unlock()
	if len(closed) != 1 || !strings.HasPrefix(closed[0], "drain: no reply yet") || !strings.Contains(closed[0], "waited for an internal pass") {
		t.Fatalf("the drain did not say why no spoken reply came: %q", closed)
	}
	mu.Lock()
	n := len(woke)
	mu.Unlock()
	if n != 0 {
		t.Fatalf("a turn ran while the pass held the gate: %v", woke)
	}

	// The pass ends: the words take the turn as the operator's and are answered.
	a.releaseTurn()
	holdWait(t, "the words to take the turn after the pass", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(woke) == 1
	})
	mu.Lock()
	got := woke[0]
	mu.Unlock()
	if got != roleOperator+": "+voiceMarker+"are you there?" {
		t.Fatalf("the words ran as the operator's turn: %q", got)
	}
	holdWait(t, "the gate to be given back after the turn", func() bool {
		if !a.TryBeginTurn() {
			return false
		}
		a.releaseTurn()
		return true
	})
}

// WORDS WAITING FOR THE TURN KEEP THEIR SPEAKER WHEN THEIR SESSION ENDS. A
// listed speaker's words are delivered with their observation and wait for
// a pass; the session ends; the pass ends and the words are recorded. The
// turn that holds them names who spoke.
func TestWordsWaitingBehindAPassKeepTheirSpeakerWhenTheirSessionEnds(t *testing.T) {
	a := newVoiceApp(t)
	logs := logsink.CaptureForTest(t)
	voiceModeDoor(t, a, listenInteractive, speakOn)
	a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "only", UIDs: []string{"quinn"}}
	h, _, end := endableSession(a, "vs-speaker-kept", true)
	stubWake(t, func() (string, error) { return "", nil })
	key := voiceRefKey(h.id, 7)

	holdPass(t, a)
	a.voiceEngineEvent(nil, finalRaw(h.id, 7, "words with a known speaker"), dropEvent)
	a.voiceEngineEvent(nil, observationRaw(h.id, 20, knownObservation(7, "Quinn", "quinn")), dropEvent)
	holdWait(t, "the words to reach the wait for the pass", func() bool { return logs.Contains(waitsForThePass) })
	if _, held := a.speakerPending.Load(key); !held {
		a.releaseTurn()
		t.Fatal("rig: the observation was not held for its delivered final")
	}

	end()
	if _, held := a.speakerPending.Load(key); !held {
		t.Error("the session's end dropped the observation of words still waiting for the turn")
	}
	// A meeting begun meanwhile makes the words the room's, so the host's own
	// recorder writes them when they take the turn and the test can read the
	// turn that holds them.
	voiceModeDoor(t, a, listenMeeting, speakOff)
	a.releaseTurn()
	holdWait(t, "the words to be recorded after the pass", func() bool {
		_, found := spokenAttribution(t, a, key)
		return found
	})

	holdWait(t, "the turn that recorded the words to end", func() bool {
		if !a.TryBeginTurn() {
			return false
		}
		a.releaseTurn()
		return true
	})
	got, _ := spokenAttribution(t, a, key)
	if !strings.Contains(got, `"speaker_id":"quinn"`) {
		t.Fatalf("the words lost their speaker when their session ended: %q", got)
	}
	if _, held := a.speakerPending.Load(key); held {
		t.Fatal("the observation remained pending after its turn took it")
	}
}

func TestWordsInTheSessionsOrderAreRecordedWhenACloseComesBeforeTheirTurn(t *testing.T) {
	a := newVoiceApp(t)
	h, _ := newTrackedSession(a, "vs-order-closed", false) // a room: the host's own recorder writes the words
	ahead := make(chan struct{})
	h.heardTail = ahead // earlier words, not yet admitted
	h.work.Add(1)
	a.admitHeldInOrder(h, &heldFinal{heard: heardUtterance{Source: "speech engine", Text: "said before the close", SessionID: h.id, Sequence: 8, Operator: true}})
	if err := h.Close(context.Background(), "abort"); err != nil {
		t.Fatal(err)
	}
	close(ahead)
	settled := make(chan struct{})
	go func() { h.work.Wait(); close(settled) }()
	select {
	case <-settled:
	case <-time.After(5 * time.Second):
		t.Fatal("the words in the session's order never settled after the earlier words were admitted")
	}
	latest, err := a.store.GetLatestOperatorTurn()
	if err != nil || latest == nil || latest.Content != voiceMarker+voiceRoomNote+"said before the close" {
		t.Fatalf("a close before their turn dropped the words: latest=%+v err=%v", latest, err)
	}
}

// THE WAIT IN THE SESSION'S ORDER DOES NOT OUTLIVE A STOP. Words wait
// behind words that will never be admitted; the identity stops and the
// plugin teardown ends the session. The wait ends, the session's work is
// given back, and the words are refused by name: nothing is left for the
// teardown to wait on, and nothing is dropped in silence.
func TestTheWaitInTheSessionsOrderEndsWhenTheIdentityStopsAndTheSessionIsOver(t *testing.T) {
	a := newVoiceApp(t)
	logs := logsink.CaptureForTest(t)
	lifetime, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	a.bgCtx, a.bgCancel = lifetime, stop
	done := make(chan struct{})
	h := &voiceHandle{id: "vs-order-stop", done: done, drained: make(chan struct{})}
	a.voiceSessions.Store(h.id, h)
	a.voiceModes.Store(h.id, true)
	ahead := make(chan struct{}) // words heard earlier that are never admitted
	h.heardTail = ahead
	h.work.Add(1)
	a.admitHeldInOrder(h, &heldFinal{heard: heardUtterance{Source: "speech engine", Text: "words behind words that never go", Answer: true, SessionID: h.id, Sequence: 8, Operator: true}})

	given := make(chan struct{})
	go func() { h.work.Wait(); close(given) }()
	// The session ending alone ends the session's claim, not the wait.
	close(done)
	select {
	case <-given:
	case <-time.After(2 * time.Second):
		t.Fatal("the ended session's work was still held by words waiting in its order")
	}
	if logs.Contains("was not recorded") {
		t.Fatalf("the words were let go when their session ended:\n%s", logs.String())
	}
	// The identity stops: now nothing ahead can be admitted, and the wait ends.
	stop()
	holdWait(t, "the words to be refused by name at the stop", func() bool {
		return logs.Contains("final 8 on " + h.id + " was not recorded: the identity is stopping")
	})
}

// THE REASON IS RECORDED BEFORE THE SESSION'S WORK IS GIVEN BACK. When a pass
// outlasts the bound, passOutlastsSession gives back both counts the words
// hold on their session (the binding's and the door's) and notes why no
// spoken reply came; the drain waits on those counts and then reads the
// note. Given back first, the drain could read before the note and say "no
// utterance after Finish" (seen on the Linux gate host, 1 and 2 in 30 runs of
// TestFinishIsNotHeldBehindAPassPastTheBoundThroughTheObserver, on main too).
func TestTheReasonIsRecordedBeforeTheSessionsWorkIsGivenBack(t *testing.T) {
	a := newVoiceApp(t)
	h, _ := newTrackedSession(a, "vs-reason-first", true)
	b := a.voiceBindingFor(heardUtterance{SessionID: h.id, Sequence: 7, Answer: true})
	var seen []string
	look := func(inner func()) func() {
		return func() {
			r, _ := h.replyOutcome.Load().(string)
			seen = append(seen, r)
			inner()
		}
	}
	b.done = look(b.done)
	h.work.Add(1)
	b.words = a.deliverSpoken(heardUtterance{SessionID: h.id, Sequence: 7}, look(h.work.Done))
	a.passOutlastsSession(b, "the pass has held the turn for 50ms", false)
	if len(seen) != 2 {
		t.Fatalf("both counts the words hold go back: %d did", len(seen))
	}
	for _, r := range seen {
		if !strings.HasPrefix(r, "no reply yet:") {
			t.Fatalf("the session's work was given back before the reason was recorded: the drain could read %q", r)
		}
	}
}

// The same order on the two other roads a session's claim ends by with a
// reason: words that waited behind earlier words of their session, and
// words a meeting made the room's while they waited in the queue.
func TestEveryReasonIsRecordedBeforeItsWorkIsGivenBack(t *testing.T) {
	a := newVoiceApp(t)
	h, _ := newTrackedSession(a, "vs-reason-roads", true)
	var seen []string
	look := func(inner func()) func() {
		return func() {
			r, _ := h.replyOutcome.Load().(string)
			seen = append(seen, r)
			h.replyOutcome.Store("")
			inner()
		}
	}

	h.work.Add(1)
	behind := heardUtterance{SessionID: h.id, Sequence: 8, Answer: true}
	behind.words = a.deliverSpoken(behind, look(h.work.Done))
	a.orderOutlastsSession(h, behind, "the words ahead have waited 50ms")

	b := a.voiceBindingFor(heardUtterance{SessionID: h.id, Sequence: 9, Answer: true})
	b.done = look(b.done)
	a.recordRoom([]steerEntry{{role: roleOperator, content: voiceMarker + "for the room", voice: b}})

	if len(seen) != 2 {
		t.Fatalf("each road gives the session's work back once: %d did", len(seen))
	}
	for i, want := range []string{"no reply yet:", "recorded (meeting)"} {
		if !strings.HasPrefix(seen[i], want) {
			t.Fatalf("road %d gave the session's work back before its reason was recorded: the drain could read %q", i+1, seen[i])
		}
	}
}
