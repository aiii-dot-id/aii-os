package app

// .
// .
// .
// .
// .
// .
// .
// .

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

// .
// .
// .
// .
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

// .
// .
// .
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
	a.voiceEngineEvent(nil, observationRaw(h.id, 21, map[string]any{"refers_to": 7, "speaker": "Sam", "speaker_id": "sam", "decision": "known"}), page.enqueue)
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

// .
// .
// .
// .
// .
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

// .
// .
// .
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

// .
// .
// .
// .
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
	// .
	// .
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

// .
// .
// .
// .
func TestSpeakOffCommittedDuringTheEnqueueFencesTheSynthesis(t *testing.T) {
	a := newVoiceApp(t)
	voiceModeDoor(t, a, listenInteractive, speakOn)
	f := &fakeEngineSession{}
	h := &voiceHandle{id: "vs-enqueue", v: f, done: make(chan struct{})}
	a.voiceSessions.Store(h.id, h)
	var refs []dashboard.VoiceReplyRef
	var mu sync.Mutex
	a.voiceReplySink = func(ref dashboard.VoiceReplyRef, _ string) { mu.Lock(); refs = append(refs, ref); mu.Unlock() }
	// .
	// .
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

// .
// .
// .
// .
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
	// .
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

// .
// .
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

// .
// .
// .
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
	// .
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
	// .
	sid, err := a.speakMint(dashboard.SpeakText{Sample: true, Provider: "my-voice", Model: "m", Voice: "v2"})
	if err != nil {
		t.Fatalf("an audition was refused: %v", err)
	}
	a.spokenMu.Lock()
	s := a.spoken[sid]
	a.spokenMu.Unlock()
	s.mu.Lock()
	s.started, s.done = true, true
	s.mu.Unlock()
	if err := a.speakPlay(context.Background(), sid, &strings.Builder{}); errors.Is(err, errSpeakOff) {
		t.Fatalf("an audition was refused at play as if it were a reply: %v", err)
	}
}

// .
// .
// .
// .
// .
// .
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
	// .
	// .
	f.onSynthesize = nil
	if v := a.synthesizeReply(context.Background(), h.id, h.gen.Load(), "the next reply, under on"); v != replyAdmitted || f.producingNow() == "" {
		t.Fatalf("a reply admitted after the on must be spoken: verdict %v, producing %q", v, f.producingNow())
	}
}

// .
// .
// .
type passWaitContext struct {
	context.Context
	once    sync.Once
	entered chan struct{}
}

func (c *passWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

// .
// .
// .
// .
// .
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
	a.releaseTurn()
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

// .
// .
// .
// .
// .
// .
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

// .
// .
// .
// .
// .
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

	// .
	r := minted("hushed before it began")
	if reserved() == 0 {
		t.Fatal("the fixture reply holds nothing against the month")
	}
	voiceModeDoor(t, a, listenInteractive, speakOff)
	voiceModeDoor(t, a, listenInteractive, speakOn)
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

	// .
	// .
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
	// .
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

// .
// .
// .
func TestASilentTurnDoesNotInterruptTheReplyBeingSpoken(t *testing.T) {
	a := newVoiceApp(t)
	voiceModeDoor(t, a, listenInteractive, speakOn)
	f := &fakeEngineSession{}
	h := &voiceHandle{id: "vs-out", v: f, b: &audio.Binding{OutputHandle: "out-1", Contained: true}, done: make(chan struct{})}
	a.voiceSessions.Store(h.id, h)
	a.voiceReplySink = func(dashboard.VoiceReplyRef, string) {}
	if a.voiceOutputSession() != h {
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
	// .
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

// .
// .
// .
// .
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
	// .
	m, _ := newTrackedSession(a, "vs-mic", true)
	a.voiceEngineEvent(nil, finalRaw(m.id, 1, "words the operator spoke"), page.enqueue)
	awaitWork(t, m)
	if n := wakes.Load(); n != 1 {
		t.Fatalf("words spoken into a microphone woke %d turn(s), want 1", n)
	}
}

// .
// .
// .
// .
func TestTheDoorKeepsTheHalfAChangeDoesNotName(t *testing.T) {
	a := newVoiceApp(t)
	voiceModeDoor(t, a, listenInteractive, speakOff)
	_, _, rev := a.VoiceMode()
	if _, err := a.applyConfigChangeWith(map[string]interface{}{
		"speech.mode": map[string]any{"listen": listenMeeting},
	}, func(*Config) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	listen, speak, rev2 := a.VoiceMode()
	if listen != listenMeeting || speak != speakOff || rev2 != rev+1 {
		t.Fatalf("a listen-only change gave (%s, %s, rev %d); want (meeting, off, rev %d)", listen, speak, rev2, rev+1)
	}
	// .
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

// .
// .
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

// .
// .
// .
// .
// .
// .
// .
// .
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
		_, err := a.applyConfigChangeWith(map[string]interface{}{"speech.mode": map[string]any{"listen": listenInteractive, "speak": speakOff}}, func(*Config) (bool, error) { return true, nil })
		finished <- err
	}()
	var first string
	select {
	case first = <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the off never reached its first fence")
	}
	// .
	// .
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

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
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

// .
// .
// .
// .
// .
// .
// .
// .
func TestWordsFromARoomNeverReachADreamRequest(t *testing.T) {
	a := newVoiceApp(t)
	var wakes atomic.Int32
	stubWake(t, func() (string, error) { wakes.Add(1); return "", nil })
	page := &pageLog{}

	// .
	// .
	// .
	// .
	// .
	// .
	if err := a.engine.RecordConversationTurn(roleOperator, voiceMarker+"can you hear me clearly"); err != nil {
		t.Fatal(err)
	}

	// .
	// .
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
