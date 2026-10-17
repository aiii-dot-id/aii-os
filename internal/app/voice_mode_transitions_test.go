package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// A MEETING ENTERED AFTER THE OPEN MAKES THE LANE'S WORDS THE ROOM'S.
// The lane was opened as a conversation; the mode in force when its
// final arrives says the words are heard, not addressed: recorded with
// the room note, no turn woken.
func TestAMeetingEnteredAfterTheOpenRecordsTheConversationsFinalsAsTheRooms(t *testing.T) {
	a := newVoiceApp(t)
	voiceModeDoor(t, a, listenInteractive, speakOn)
	h, _ := newTrackedSession(a, "vs-meeting-later", true)
	if got := a.voiceOpenMode(h.id, "conversation"); got != "conversation" {
		t.Fatalf("opened as %q", got)
	}
	var wakes atomic.Int32
	stubWake(t, func() (string, error) { wakes.Add(1); return "", nil })

	voiceModeDoor(t, a, listenMeeting, speakOff)
	page := &pageLog{}
	a.voiceEngineEvent(nil, finalRaw(h.id, 1, "words heard after the meeting began"), page.enqueue)
	awaitWork(t, h)
	if n := wakes.Load(); n != 0 {
		t.Fatalf("a meeting entered after the open still woke %d turn(s) for the room's words", n)
	}
	latest, err := a.store.GetLatestOperatorTurn()
	if err != nil || latest == nil || latest.Content != voiceMarker+voiceRoomNote+"words heard after the meeting began" {
		t.Fatalf("the room's words must be recorded with the room note: latest=%+v err=%v", latest, err)
	}
}

// A FINAL HELD FOR ITS SPEAKER IS ADMITTED UNDER THE MODE IN FORCE WHEN
// IT IS RELEASED, not the one when it was heard: the meeting committed
// during the hold makes it the room's.
func TestAHeldFinalReleasedAfterAMeetingCommitIsTheRooms(t *testing.T) {
	a := newVoiceApp(t)
	a.cfg.Speech.Speakers = SpeakerPolicyConfig{Mode: "ignore", UIDs: []string{"visitor"}, Unidentified: "deliver", Revision: 3}
	voiceModeDoor(t, a, listenInteractive, speakOn)
	h, _ := newTrackedSession(a, "vs-held", true)
	var wakes atomic.Int32
	stubWake(t, func() (string, error) { wakes.Add(1); return "", nil })
	page := &pageLog{}

	a.voiceEngineEvent(nil, finalRaw(h.id, 7, "held words"), page.enqueue)
	if page.find("transcript_final", 7) != nil {
		t.Fatal("the restricted policy did not hold the final")
	}
	voiceModeDoor(t, a, listenMeeting, speakOff)
	a.voiceEngineEvent(nil, observationRaw(h.id, 21, map[string]any{"refers_to": 7, "speaker": "Quinn", "speaker_id": "quinn", "decision": "known"}), page.enqueue)
	a.voiceInputFinished(h.id)
	awaitDrained(t, h)
	if n := wakes.Load(); n != 0 {
		t.Fatalf("a held final released into a meeting still woke %d turn(s)", n)
	}
	latest, err := a.store.GetLatestOperatorTurn()
	if err != nil || latest == nil || latest.Content != voiceMarker+voiceRoomNote+"held words" {
		t.Fatalf("the released words must be recorded as the room's: latest=%+v err=%v", latest, err)
	}
}

// OFF ENTERED AFTER THE OPEN STILL ANSWERS WHAT WAS HEARD. The lane's
// remaining finals are the tail of what was said while it listened —
// the Finish that enters earbuds is exactly this transition, and it owes
// those words their reply (as text when speak is off). The microphone
// is the page's to end; the host does not discard spoken words.
func TestOffEnteredAfterTheOpenStillAnswersWhatWasHeard(t *testing.T) {
	a := newVoiceApp(t)
	voiceModeDoor(t, a, listenInteractive, speakOn)
	h, _ := newTrackedSession(a, "vs-off-later", true)
	var wakes atomic.Int32
	stubWake(t, func() (string, error) { wakes.Add(1); return "", nil })

	voiceModeDoor(t, a, listenOff, speakOff)
	a.voiceEngineEvent(nil, finalRaw(h.id, 1, "the last thing said before off"), (&pageLog{}).enqueue)
	awaitWork(t, h)
	if n := wakes.Load(); n != 1 {
		t.Fatalf("the tail of what was heard woke %d turn(s), want 1", n)
	}
}

// modeErrBarrier is a context whose Err() parks its first caller until
// released — the synthesis admission's own ctx.Err() check, inside the
// admit lock, becomes the place a commit is raced against.
type modeErrBarrier struct {
	context.Context
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func newModeErrBarrier() *modeErrBarrier {
	return &modeErrBarrier{Context: context.Background(), entered: make(chan struct{}), release: make(chan struct{})}
}

func (c *modeErrBarrier) Err() error {
	c.once.Do(func() { close(c.entered); <-c.release })
	return c.Context.Err()
}

func awaitEntered(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s never happened", what)
	}
}

func awaitWork(t *testing.T, h *voiceHandle) {
	t.Helper()
	done := make(chan struct{})
	go func() { h.work.Wait(); close(done) }()
	awaitEntered(t, done, "the heard utterance's settlement")
}

func TestSpeakOffCommittedBeforeAdmissionIsSeenUnderTheAdmitLock(t *testing.T) {
	a := newVoiceApp(t)
	voiceModeDoor(t, a, listenInteractive, speakOn)
	f := &fakeEngineSession{}
	h := &voiceHandle{id: "vs-race", v: f, done: make(chan struct{})}
	a.voiceSessions.Store(h.id, h)
	var refs []dashboard.VoiceReplyRef
	var mu sync.Mutex
	a.voiceReplySink = func(ref dashboard.VoiceReplyRef, _ string) { mu.Lock(); refs = append(refs, ref); mu.Unlock() }

	c := newModeErrBarrier()
	verdict := make(chan replyVerdict, 1)
	go func() { verdict <- a.synthesizeReply(c, h.id, h.gen.Load(), "this must remain text") }()
	awaitEntered(t, c.entered, "the admission's context check")
	// The commit lands while the admission is parked INSIDE the lock: the
	// door takes no session's admit lock, so it returns.
	voiceModeDoor(t, a, listenInteractive, speakOff)
	close(c.release)
	select {
	case v := <-verdict:
		if v != replyAdmitted {
			t.Fatalf("a reply delivered as text = %v", v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the admission never settled")
	}
	if got := f.synthed(); len(got) != 0 {
		t.Fatalf("speak=off was committed before the enqueue, yet the engine was handed: %v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(refs) != 1 || !refs[0].TextOnly {
		t.Fatalf("the words must still reach the page, text-only: %+v", refs)
	}
}

// SPEAK OFF COMMITTED DURING THE ENQUEUE — after the lock's check, before
// the record of the synthesis exists for the commit's own fence to find
// — is caught after the acknowledgement: the synthesis is fenced by its
// id and the reply is delivered as text.
func TestSpeakOffCommittedDuringTheEnqueueFencesTheSynthesis(t *testing.T) {
	a := newVoiceApp(t)
	voiceModeDoor(t, a, listenInteractive, speakOn)
	f := &fakeEngineSession{}
	h := &voiceHandle{id: "vs-enqueue", v: f, done: make(chan struct{})}
	a.voiceSessions.Store(h.id, h)
	var refs []dashboard.VoiceReplyRef
	var mu sync.Mutex
	a.voiceReplySink = func(ref dashboard.VoiceReplyRef, _ string) { mu.Lock(); refs = append(refs, ref); mu.Unlock() }
	// The engine has the text and is producing; the commit lands here,
	// inside the enqueue, where nothing yet names the synthesis in flight.
	f.onSynthesize = func() { voiceModeDoor(t, a, listenInteractive, speakOff) }

	if v := a.synthesizeReply(context.Background(), h.id, h.gen.Load(), "spoken into a mode that closed"); v != replyAdmitted {
		t.Fatalf("verdict = %v, want the reply delivered (as text)", v)
	}
	if p := f.producingNow(); p != "" {
		t.Fatalf("the engine is still producing %s after speak turned off during its admission", p)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(refs) != 1 || !refs[0].TextOnly || refs[0].SynthesisID != "" {
		t.Fatalf("the reply must be delivered text-only once: %+v", refs)
	}
	if outcome, _ := h.replyOutcome.Load().(string); outcome != replyNotSpokenOutcome {
		t.Fatalf("the drain would not say why nothing was spoken: %q", outcome)
	}
}

// SPEAK OFF COMMITTED WHILE THE ACKNOWLEDGEMENT IS PENDING fences the
// synthesis at the commit — before the acknowledgement arrives, by the
// door's own hand — so a reply admitted under the old value does not
// keep being made while the engine takes its time.
func TestSpeakOffCommittedWhileTheAcknowledgementIsPendingFencesTheSynthesis(t *testing.T) {
	a := newVoiceApp(t)
	voiceModeDoor(t, a, listenInteractive, speakOn)
	f := &fakeEngineSession{entered: make(chan struct{}), ackGate: make(chan struct{})}
	h := &voiceHandle{id: "vs-ack", v: f, done: make(chan struct{})}
	a.voiceSessions.Store(h.id, h)
	a.voiceReplySink = func(dashboard.VoiceReplyRef, string) {}

	verdict := make(chan replyVerdict, 1)
	go func() {
		verdict <- a.synthesizeReply(context.Background(), h.id, h.gen.Load(), "held at the acknowledgement")
	}()
	awaitEntered(t, f.entered, "the enqueue")
	if p := f.producingNow(); p == "" {
		t.Fatal("the fixture engine is not producing the enqueued synthesis")
	}
	awaitInflight(t, h)
	voiceModeDoor(t, a, listenInteractive, speakOff)
	// The fence is the COMMIT's: proved before the acknowledgement is released.
	if p := f.producingNow(); p != "" {
		t.Fatalf("the commit of speak=off left the engine producing %s", p)
	}
	close(f.ackGate)
	select {
	case v := <-verdict:
		if v != replyAdmitted {
			t.Fatalf("verdict = %v, want the reply delivered (as text)", v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the admission never settled")
	}
}

// THE TAKEN-OVER VOICE READS THE MODE UNDER THE SAME LOCK: a commit that
// lands while the takeover is parked inside it mints nothing.
func TestTheFallbackVoiceReadsTheModeUnderTheAdmitLock(t *testing.T) {
	a := newVoiceApp(t)
	voiceModeDoor(t, a, listenInteractive, speakOn)
	f := &fakeEngineSession{}
	h := &voiceHandle{id: "vs-fallback", v: f, done: make(chan struct{})}
	h.hush = func(why string) { a.hushFallback(h, why) }
	a.voiceSessions.Store(h.id, h)
	var minted atomic.Int32
	prev := voiceFallbackMint
	voiceFallbackMint = func(*App, string) string { minted.Add(1); return "cloud-1" }
	t.Cleanup(func() { voiceFallbackMint = prev })
	var refs []dashboard.VoiceReplyRef
	var mu sync.Mutex
	a.voiceReplySink = func(ref dashboard.VoiceReplyRef, _ string) { mu.Lock(); refs = append(refs, ref); mu.Unlock() }

	c := newModeErrBarrier()
	took := make(chan bool, 1)
	go func() {
		took <- a.speakFallback(c, &voiceBinding{session: h.id, gen: h.gen.Load()}, "refused by the engine")
	}()
	awaitEntered(t, c.entered, "the takeover's context check")
	voiceModeDoor(t, a, listenInteractive, speakOff)
	close(c.release)
	select {
	case ok := <-took:
		if !ok {
			t.Fatal("the takeover must still deliver the words")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the takeover never settled")
	}
	if n := minted.Load(); n != 0 {
		t.Fatalf("speak=off was committed before the takeover minted, yet %d reply was bought", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(refs) != 1 || !refs[0].TextOnly {
		t.Fatalf("the words must reach the page text-only: %+v", refs)
	}
}

// A CLOUD REPLY MINTED UNDER ON IS NEITHER PLAYED NOR KEPT SPEAKING AFTER
// OFF: the play is refused where the audio would be delivered, and the
// commit hushes what is being spoken. A sample is not a reply.
func TestACloudReplyMintedBeforeOffIsNotPlayedAfterIt(t *testing.T) {
	a := newVoiceApp(t)
	path := filepath.Join(filepath.Dir(a.cfg.SourcePath), "providers.json")
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"my-voice","url":"http://127.0.0.1:9","api_key":"vk-1234"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a.cfg.Speech.TTS = TTSConfig{Provider: "my-voice", Model: "m", Voice: "v"}
	voiceModeDoor(t, a, listenInteractive, speakOn)
	id, err := a.speakMint(dashboard.SpeakText{Text: "bought while speaking was on"})
	if err != nil {
		t.Fatal(err)
	}
	a.spokenMu.Lock()
	r := a.spoken[id]
	a.spokenMu.Unlock()
	// Being spoken (started) when the commit lands.
	r.mu.Lock()
	r.started = true
	r.mu.Unlock()

	voiceModeDoor(t, a, listenInteractive, speakOff)
	r.mu.Lock()
	done, rerr := r.done, r.err
	r.mu.Unlock()
	if !done || !errors.Is(rerr, errHushed) {
		t.Fatalf("the commit of speak=off did not hush the reply being spoken: done=%v err=%v", done, rerr)
	}
	if err := a.speakPlay(context.Background(), id, &strings.Builder{}); !errors.Is(err, errSpeakOff) {
		t.Fatalf("a reply minted under on was played after off: %v", err)
	}
	// A sample minted under off is refused by neither door.
	sid, err := a.speakMint(dashboard.SpeakText{Sample: true, Provider: "my-voice", Model: "m", Voice: "v2"})
	if err != nil {
		t.Fatalf("an audition was refused: %v", err)
	}
	a.spokenMu.Lock()
	s := a.spoken[sid]
	a.spokenMu.Unlock()
	s.mu.Lock()
	s.started, s.done = true, true // never reaches the unreachable service
	s.mu.Unlock()
	if err := a.speakPlay(context.Background(), sid, &strings.Builder{}); errors.Is(err, errSpeakOff) {
		t.Fatalf("an audition was refused at play as if it were a reply: %v", err)
	}
}

// AN OFF THAT COMPLETED WHILE THE ENGINE HELD THE TEXT IS NOT UNDONE BY
// THE ON THAT FOLLOWED IT. Both commits land inside the enqueue — after
// the lock's check, before the record of the synthesis exists for the
// commit's fence to find — and the value in force afterwards says on.
// The admission asks by revision, not by value: an off newer than the
// one it was admitted under fences the synthesis it enqueued.
func TestSpeakOffThenOnDuringTheEnqueueStillFencesTheOldSynthesis(t *testing.T) {
	a := newVoiceApp(t)
	voiceModeDoor(t, a, listenInteractive, speakOn)
	f := &fakeEngineSession{}
	h := &voiceHandle{id: "vs-pulse", v: f, done: make(chan struct{})}
	a.voiceSessions.Store(h.id, h)
	var refs []dashboard.VoiceReplyRef
	var mu sync.Mutex
	a.voiceReplySink = func(ref dashboard.VoiceReplyRef, _ string) { mu.Lock(); refs = append(refs, ref); mu.Unlock() }
	f.onSynthesize = func() {
		voiceModeDoor(t, a, listenInteractive, speakOff)
		voiceModeDoor(t, a, listenInteractive, speakOn)
	}

	if v := a.synthesizeReply(context.Background(), h.id, h.gen.Load(), "reply from before the completed off"); v != replyAdmitted {
		t.Fatalf("verdict = %v, want the reply delivered (as text)", v)
	}
	if p := f.producingNow(); p != "" {
		t.Fatalf("off completed while this synthesis was producing, and the on that followed revived it unfenced: %s; ops=%v", p, f.opsSeen())
	}
	fenced := false
	for _, op := range f.opsSeen() {
		if strings.HasPrefix(op, "interrupt:vs-pulse-syn-") {
			fenced = true
		}
	}
	if !fenced {
		t.Fatalf("the synthesis was never fenced by its id: ops=%v", f.opsSeen())
	}
	mu.Lock()
	delivered := append([]dashboard.VoiceReplyRef(nil), refs...)
	mu.Unlock()
	if len(delivered) != 1 || !delivered[0].TextOnly {
		t.Fatalf("the words must reach the page text-only once: %+v", delivered)
	}
	// And the reply after it, admitted under the on with no commit in its
	// enqueue, is spoken.
	f.onSynthesize = nil
	if v := a.synthesizeReply(context.Background(), h.id, h.gen.Load(), "the next reply, under on"); v != replyAdmitted || f.producingNow() == "" {
		t.Fatalf("a reply admitted after the on must be spoken: verdict %v, producing %q", v, f.producingNow())
	}
}

// passWaitContext signals when the words reach the wait for an internal
// pass — awaitTurnGate selects on Done() — so a commit can be placed
// there, at the actual wait, and not before it.
type passWaitContext struct {
	context.Context
	once    sync.Once
	entered chan struct{}
}

func (c *passWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

// WORDS THAT WAITED FOR A PASS ARE ADMITTED UNDER THE MODE IN FORCE WHEN
// THE TURN IS TAKEN. They arrived as a question while an internal pass
// held the turn; the meeting began while they waited; the pass ends and
// they are the room's — recorded with the note, no turn woken, the
// binding released, the drain told why.
func TestAMeetingBegunWhileTheWordsWaitedForAPassIsTheRooms(t *testing.T) {
	a := newVoiceApp(t)
	voiceModeDoor(t, a, listenInteractive, speakOn)
	h, _ := newTrackedSession(a, "vs-pass", true)
	var wakes atomic.Int32
	stubWake(t, func() (string, error) { wakes.Add(1); return "", nil })
	if !a.TryBeginTurn() {
		t.Fatal("the turn could not be taken for the pass")
	}
	a.turnMu.Lock()
	a.turnFacility = true
	a.turnMu.Unlock()

	ctx := &passWaitContext{Context: context.Background(), entered: make(chan struct{})}
	done := make(chan struct{})
	h.work.Add(1)
	go func() {
		defer h.work.Done()
		a.handleHeard(ctx, heardUtterance{Source: "speech engine", Text: "words waiting behind the pass", Answer: true, Operator: true, SessionID: h.id, Gen: h.gen.Load(), Sequence: 1})
		close(done)
	}()
	select {
	case <-ctx.entered:
	case <-time.After(5 * time.Second):
		a.releaseTurn()
		t.Fatal("the words never reached the wait for the pass")
	}
	voiceModeDoor(t, a, listenMeeting, speakOff)
	a.releaseTurn() // the pass ends; the words take the turn
	awaitEntered(t, done, "the words' settlement")
	if n := wakes.Load(); n != 0 {
		t.Fatalf("a meeting begun while the words waited still woke %d turn(s)", n)
	}
	latest, err := a.store.GetLatestOperatorTurn()
	if err != nil || latest == nil || latest.Content != voiceMarker+voiceRoomNote+"words waiting behind the pass" {
		t.Fatalf("the words must be recorded as the room's: latest=%+v err=%v", latest, err)
	}
	if outcome, _ := h.replyOutcome.Load().(string); outcome != "recorded (meeting), no reply" {
		t.Fatalf("the drain would not say why nothing was answered: %q", outcome)
	}
	if !a.TryBeginTurn() {
		t.Fatal("the turn was not released after the words were recorded")
	}
	a.releaseTurn()
}

// WORDS QUEUED FOR A RUNNING TURN ARE ADMITTED UNDER THE MODE IN FORCE AT
// THEIR DELIVERY. A steer with a voice binding was heard as a question;
// the meeting began while it waited for the tool boundary; the boundary
// empties the queue and the words are the room's — recorded with the
// note, not handed to the model, the binding released. Under interactive
// the same queue delivers them, marked, as it always did.
func TestAMeetingBegunWhileTheWordsWaitedInTheSteerQueueIsTheRooms(t *testing.T) {
	a := newVoiceApp(t)
	voiceModeDoor(t, a, listenInteractive, speakOn)
	h, _ := newTrackedSession(a, "vs-steer", true)
	if !a.TryBeginTurn() {
		t.Fatal("the turn could not be taken")
	}
	defer a.releaseTurn()
	heard := func(seq int64, text string) {
		h.work.Add(1)
		defer h.work.Done()
		a.handleHeard(context.Background(), heardUtterance{Source: "speech engine", Text: text, Answer: true, Operator: true, SessionID: h.id, Gen: h.gen.Load(), Sequence: seq})
	}
	heard(1, "queued behind the running turn")
	if n := len(a.PendingSteers()); n != 1 {
		t.Fatalf("the words were not queued for the running turn: %d pending", n)
	}
	voiceModeDoor(t, a, listenMeeting, speakOff)
	if out := a.DrainSteering(); len(out) != 0 {
		t.Fatalf("the room's words were delivered to the model: %q", out)
	}
	latest, err := a.store.GetLatestOperatorTurn()
	if err != nil || latest == nil || latest.Content != voiceMarker+voiceRoomNote+"queued behind the running turn" {
		t.Fatalf("the words must be recorded as the room's: latest=%+v err=%v", latest, err)
	}
	if outcome, _ := h.replyOutcome.Load().(string); outcome != "recorded (meeting), no reply" {
		t.Fatalf("the drain would not say why nothing was answered: %q", outcome)
	}

	voiceModeDoor(t, a, listenInteractive, speakOn)
	heard(2, "asked while listening")
	out := a.DrainSteering()
	if len(out) != 1 || !strings.Contains(out[0], "asked while listening") || strings.Contains(out[0], voiceRoomNote) {
		t.Fatalf("under interactive the queue must deliver the question, marked, without the room note: %q", out)
	}
}

// A HUSH IS TERMINAL, AND THE START ASKS THE MODE AGAIN. A reply minted
// under on and hushed by the commit of off before it began must not
// begin when something asks for it; and in the instant between the
// mode's publication and the hush that follows it, the start itself
// refuses. Either way the reply's hold on the month is given back.
func TestAReplyHushedBeforeItBeganDoesNotBegin(t *testing.T) {
	a := newVoiceApp(t)
	path := filepath.Join(filepath.Dir(a.cfg.SourcePath), "providers.json")
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"my-voice","url":"http://127.0.0.1:9","api_key":"vk-1234"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a.cfg.Speech.TTS = TTSConfig{Provider: "my-voice", Model: "m", Voice: "v"}
	voiceModeDoor(t, a, listenInteractive, speakOn)
	reserved := func() int {
		a.speechReserveMu.Lock()
		defer a.speechReserveMu.Unlock()
		return a.speechReservedChars
	}
	minted := func(text string) *spokenReply {
		t.Helper()
		id, err := a.speakMint(dashboard.SpeakText{Text: text})
		if err != nil {
			t.Fatal(err)
		}
		a.spokenMu.Lock()
		defer a.spokenMu.Unlock()
		return a.spoken[id]
	}

	// Minted, then hushed by the commit of off, then asked to start.
	r := minted("hushed before it began")
	if reserved() == 0 {
		t.Fatal("the fixture reply holds nothing against the month")
	}
	voiceModeDoor(t, a, listenInteractive, speakOff)
	voiceModeDoor(t, a, listenInteractive, speakOn) // on again: the mode would allow a start; the hush does not
	a.speakStart(r)
	r.mu.Lock()
	started, done, rerr := r.started, r.done, r.err
	r.mu.Unlock()
	if started || !done || !errors.Is(rerr, errHushed) {
		t.Fatalf("a hushed reply began anyway: started=%v done=%v err=%v", started, done, rerr)
	}
	if n := reserved(); n != 0 {
		t.Fatalf("a reply that will never be spoken still holds %d characters against the month", n)
	}

	// Minted under on; the mode is published off but the hush has not
	// arrived yet: the start refuses on the mode.
	voiceModeDoor(t, a, listenInteractive, speakOn)
	r = minted("started after the mode moved")
	a.publishVoiceMode(VoiceModeConfig{Listen: listenInteractive, Speak: speakOff, Revision: 99})
	a.speakStart(r)
	r.mu.Lock()
	started, done, rerr = r.started, r.done, r.err
	r.mu.Unlock()
	if started || !done || !errors.Is(rerr, errSpeakOff) {
		t.Fatalf("a reply started after speak turned off began anyway: started=%v done=%v err=%v", started, done, rerr)
	}
	if n := reserved(); n != 0 {
		t.Fatalf("a refused start still holds %d characters against the month", n)
	}
	// A sample begins under every mode.
	sid, err := a.speakMint(dashboard.SpeakText{Sample: true, Provider: "my-voice", Model: "m", Voice: "v2"})
	if err != nil {
		t.Fatal(err)
	}
	a.spokenMu.Lock()
	s := a.spoken[sid]
	a.spokenMu.Unlock()
	a.speakStart(s)
	s.mu.Lock()
	sampleStarted := s.started
	s.mu.Unlock()
	if !sampleStarted {
		t.Fatal("an audition was refused at its start as if it were a reply")
	}
	a.hushSpoken(sid)
}

// A SILENT TURN TAKES NOTHING OVER. With an output-only session speaking
// a reply, a turn that answers with nothing must not claim the session —
// claiming it fences the reply being spoken and puts nothing in its place.
func TestASilentTurnDoesNotInterruptTheReplyBeingSpoken(t *testing.T) {
	a := newVoiceApp(t)
	voiceModeDoor(t, a, listenInteractive, speakOn)
	f := &fakeEngineSession{}
	h := &voiceHandle{id: "vs-out", v: f, b: &audio.Binding{OutputHandle: "out-1", Contained: true}, done: make(chan struct{}), engine: speakingEngine(t, a)}
	a.voiceSessions.Store(h.id, h)
	a.voiceReplySink = func(dashboard.VoiceReplyRef, string) {}
	if a.voiceOutputSession(h.engine) != h {
		t.Fatal("the fixture session is not seen as output-only")
	}
	if v := a.synthesizeReply(context.Background(), h.id, h.gen.Load(), "the answer being spoken"); v != replyAdmitted || f.producingNow() == "" {
		t.Fatalf("the fixture reply was not admitted: %v", v)
	}
	a.settleVoice(context.Background(), "   ")
	if p := f.producingNow(); p == "" {
		t.Fatalf("a silent turn fenced the reply being spoken: ops=%v", f.opsSeen())
	}
	for _, op := range f.opsSeen() {
		if strings.HasPrefix(op, "interrupt:") {
			t.Fatalf("a silent turn sent an interrupt: ops=%v", f.opsSeen())
		}
	}
	// A turn with words takes the voice over, fencing the earlier reply.
	a.settleVoice(context.Background(), "the next answer")
	interrupted := false
	for _, op := range f.opsSeen() {
		if strings.HasPrefix(op, "interrupt:vs-out-syn-1") {
			interrupted = true
		}
	}
	if !interrupted {
		t.Fatalf("a newer reply did not take the voice over: ops=%v", f.opsSeen())
	}
}

// A SESSION WITH NO INPUT HEARS NOTHING. A transcript the engine sends on
// a session opened with a speaker and no microphone is refused: no page
// sees it, no record keeps it, no turn wakes for it — whatever the driver
// forwarded.
func TestATranscriptOnASessionWithNoInputIsRefused(t *testing.T) {
	a := newVoiceApp(t)
	voiceModeDoor(t, a, listenInteractive, speakOn)
	f := &fakeEngineSession{}
	h := &voiceHandle{id: "vs-noinput", v: f, b: &audio.Binding{OutputHandle: "out-1", Contained: true}, done: make(chan struct{}), drained: make(chan struct{})}
	a.voiceSessions.Store(h.id, h)
	a.voiceModes.Store(h.id, true)
	var wakes atomic.Int32
	stubWake(t, func() (string, error) { wakes.Add(1); return "", nil })
	page := &pageLog{}
	a.voiceEngineEvent(nil, finalRaw(h.id, 1, "words nobody spoke"), page.enqueue)
	awaitWork(t, h)
	if n := wakes.Load(); n != 0 {
		t.Fatalf("a transcript on a session with no input woke %d turn(s)", n)
	}
	if latest, err := a.store.GetLatestOperatorTurn(); err == nil && latest != nil && strings.Contains(latest.Content, "words nobody spoke") {
		t.Fatalf("a transcript on a session with no input was recorded as the operator's: %+v", latest)
	}
	if page.find("transcript_final", 1) != nil {
		t.Fatal("a transcript on a session with no input reached the page")
	}
	// The same words on a session with a microphone are the operator's.
	m, _ := newTrackedSession(a, "vs-mic", true)
	a.voiceEngineEvent(nil, finalRaw(m.id, 1, "words the operator spoke"), page.enqueue)
	awaitWork(t, m)
	if n := wakes.Load(); n != 1 {
		t.Fatalf("words spoken into a microphone woke %d turn(s), want 1", n)
	}
}

// THE HALF A CHANGE DOES NOT NAME IS KEPT INSIDE THE DOOR. A change naming
// only listen keeps the speak in force — merged in the transaction, not
// filled from a read a moment earlier — so a speak the operator turned
// off cannot be put back by an identity that only changed how it hears.
func TestTheDoorKeepsTheHalfAChangeDoesNotName(t *testing.T) {
	a := newVoiceApp(t)
	voiceModeDoor(t, a, listenInteractive, speakOff)
	_, _, rev := a.VoiceMode()
	if _, err := a.applyConfigChangeWith(map[string]any{
		"speech.mode": map[string]any{"listen": listenMeeting},
	}, func(*Config) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	listen, speak, rev2 := a.VoiceMode()
	if listen != listenMeeting || speak != speakOff || rev2 != rev+1 {
		t.Fatalf("a listen-only change gave (%s, %s, rev %d); want (meeting, off, rev %d)", listen, speak, rev2, rev+1)
	}
	// The identity's port sends only what the identity named.
	if _, err := (voiceModeAdapter{a}).SetMode("", speakOn); err != nil {
		t.Fatal(err)
	}
	if listen, speak, _ := a.VoiceMode(); listen != listenMeeting || speak != speakOn {
		t.Fatalf("a speak-only change from the identity gave (%s, %s); want (meeting, on)", listen, speak)
	}
	if _, err := (voiceModeAdapter{a}).SetMode(listenOff, ""); err != nil {
		t.Fatal(err)
	}
	if listen, speak, _ := a.VoiceMode(); listen != listenOff || speak != speakOn {
		t.Fatalf("a listen-only change from the identity gave (%s, %s); want (off, on)", listen, speak)
	}
}

// blockedFence holds the FIRST engine fence it is asked for until released,
// so a commit can be caught inside its bounded wait on one session.
type blockedFence struct {
	*fakeEngineSession
	once    *sync.Once
	entered chan string
	release <-chan struct{}
}

func (f *blockedFence) InterruptFor(ctx context.Context, sid, id, why string) error {
	f.once.Do(func() {
		f.entered <- sid
		select {
		case <-f.release:
		case <-ctx.Done():
		}
	})
	return f.fakeEngineSession.InterruptFor(ctx, sid, id, why)
}

// AN OLDER OFF FENCES ONLY WHAT WAS ADMITTED BEFORE IT. Two sessions each
// speaking a reply; an off commits and is held inside its fence on the
// first session; a newer on completes and a new reply is admitted on the
// second; the old off resumes. It used to take whatever it found in
// flight on reaching the second session — the reply admitted after the
// on — and fence it, leaving the mode on and the production stopped.
// What an off may fence is chosen before any fence is sent, by the
// revision each synthesis was admitted under.
func TestAnOlderOffDoesNotFenceAReplyAdmittedAfterANewerOn(t *testing.T) {
	a := newVoiceApp(t)
	voiceModeDoor(t, a, listenInteractive, speakOn)
	a.voiceReplySink = func(dashboard.VoiceReplyRef, string) {}
	entered := make(chan string, 1)
	release := make(chan struct{})
	var once, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	handles := map[string]*voiceHandle{}
	engines := map[string]*fakeEngineSession{}
	for _, id := range []string{"vs-order-a", "vs-order-b"} {
		f := &fakeEngineSession{}
		h := &voiceHandle{id: id, v: &blockedFence{fakeEngineSession: f, once: &once, entered: entered, release: release}, done: make(chan struct{})}
		a.voiceSessions.Store(id, h)
		handles[id], engines[id] = h, f
		if got := a.synthesizeReply(context.Background(), id, h.gen.Load(), "an old reply"); got != replyAdmitted {
			t.Fatalf("%s: %v", id, got)
		}
	}
	finished := make(chan error, 1)
	go func() {
		_, err := a.applyConfigChangeWith(map[string]any{"speech.mode": map[string]any{"listen": listenInteractive, "speak": speakOff}}, func(*Config) (bool, error) { return true, nil })
		finished <- err
	}()
	var first string
	select {
	case first = <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the off never reached its first fence")
	}
	// The newer on completes while the off waits; a reply is admitted
	// under it on the other session.
	voiceModeDoor(t, a, listenInteractive, speakOn)
	second := "vs-order-a"
	if first == second {
		second = "vs-order-b"
	}
	h := handles[second]
	if got := a.synthesizeReply(context.Background(), second, h.gen.Load(), "a reply admitted after the newer on"); got != replyAdmitted {
		t.Fatalf("a reply under the newer on was not admitted: %v", got)
	}
	newer := engines[second].producingNow()
	if newer == "" {
		t.Fatal("the newer on did not put a reply in production")
	}
	unblock()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the off never finished")
	}
	if _, speak, _ := a.VoiceMode(); speak != speakOn {
		t.Fatalf("the newer on was lost: %s", speak)
	}
	if now := engines[second].producingNow(); now != newer {
		t.Fatalf("the older off fenced %s, a reply admitted after the newer on: ops=%v", newer, engines[second].opsSeen())
	}
	if engines[first].producingNow() != "" {
		t.Fatalf("the older off did not fence the reply admitted before it on %s: ops=%v", first, engines[first].opsSeen())
	}
}

// awaitInflight waits for the synthesis to be RECORDED in flight, which
// is not the same moment as the engine entering.
//
// synthesizeReply calls SynthesizeForEnqueue and only then setInflight,
// so the fixture's entered signal fires before the record exists. The
// commit's fence takes its targets from that record (voiceModeCommitted
// ranges the sessions and compare-and-swaps loadInflight), so a test
// that commits on the engine's signal races the record and finds nothing
// to fence — which is how this failed twice on a gate box at load 45
// while passing alone every time.
//
// The sliver it was racing is not a hole in the product: the check after
// the acknowledgement catches an admission whose revision the speak-off
// watermark has passed. That is the guard for work enqueued but not yet
// recorded; the commit's fence is the guard for work already recorded.
// A test should wait for the state the code it tests actually reads.
func awaitInflight(t *testing.T, h *voiceHandle) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if h.loadInflight().id != "" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the synthesis was never recorded in flight")
}

// FROM THE MICROPHONE TO DREAM'S REQUEST, IN ONE TEST. Words the voice host
// records from a room — through the real engine-event path, under a
// meeting the operator set through the real door — must never be shown to
// DREAM, and words spoken TO the identity on the same path must be. The
// two ends were each held before — the host writes the note, DREAM is
// handed the note — and nothing held them joined: a change to how the host
// marks a room's words that kept both assertions true could still have
// made a room dream-visible, permanently, in a signed note.
func TestWordsFromARoomNeverReachADreamRequest(t *testing.T) {
	a := newVoiceApp(t)
	var wakes atomic.Int32
	stubWake(t, func() (string, error) { wakes.Add(1); return "", nil })
	page := &pageLog{}

	// Spoken TO the identity. On the real path these words are recorded by
	// the turn they open, and this rig stubs the wake (a real one needs a
	// composer and a live model), so the control is recorded as that turn
	// records it: the operator's message under the host's voice marker. It
	// is here so that "not shown" below cannot be satisfied by showing
	// nothing voice-marked at all.
	if err := a.engine.RecordConversationTurn(roleOperator, voiceMarker+"can you hear me clearly"); err != nil {
		t.Fatal(err)
	}

	// A meeting, through the REAL host path: the operator's door sets the
	// mode, the engine event arrives, and the host records the room's words.
	voiceModeDoor(t, a, listenMeeting, speakOff)
	room, _ := newTrackedSession(a, "vs-room", true)
	a.voiceEngineEvent(nil, finalRaw(room.id, 1, "we should cut the budget before friday"), page.enqueue)
	awaitWork(t, room)

	model := &scriptedLLM{replies: []string{aNoticing}}
	dream := cognitive.NewDream(a.store, model, nil, nil, dreamConfig(a.configSnapshot()))
	dream.SetConversation(a.store)
	if err := dream.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(model.shown) != 1 {
		t.Fatalf("DREAM made %d requests over a transcript holding words spoken to the identity, want 1", len(model.shown))
	}
	if strings.Contains(model.shown[0], "cut the budget") {
		t.Fatalf("WORDS FROM A ROOM REACHED DREAM:\n%s", model.shown[0])
	}
	if !strings.Contains(model.shown[0], "can you hear me clearly") {
		t.Fatalf("words spoken TO the identity on the same path were not shown:\n%s", model.shown[0])
	}
}
