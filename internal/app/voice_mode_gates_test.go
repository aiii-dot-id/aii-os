package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// .
// .
// .
// .
// .
// .
// .
// .

func voiceModeDoor(t *testing.T, a *App, listen, speak string) {
	t.Helper()
	if _, err := a.applyConfigChangeWith(map[string]interface{}{
		"speech.mode": map[string]any{"listen": listen, "speak": speak},
	}, func(*Config) (bool, error) { return true, nil }); err != nil {
		t.Fatalf("the door refused (%s, %s): %v", listen, speak, err)
	}
}

// .
// .
// .
// .
// .
// .
func TestAPageCannotOpenAConversationWhileTheHostIsInAMeeting(t *testing.T) {
	a := newVoiceApp(t)

	// .
	if got := a.voiceOpenMode("vs-1", "conversation"); got != "conversation" {
		t.Fatalf("with no mode set the page's ask stands; the engine was told %q", got)
	}
	if v, ok := a.voiceModes.Load("vs-1"); !ok || v != true {
		t.Fatalf("the session's own record disagrees with what the engine was told: %v", v)
	}

	voiceModeDoor(t, a, "meeting", "off")
	if got := a.voiceOpenMode("vs-2", "conversation"); got != "meeting" {
		t.Fatalf("a page asked for a conversation while the host is in a meeting and the engine was told %q", got)
	}
	if v, ok := a.voiceModes.Load("vs-2"); !ok || v != false {
		t.Fatalf("the session would answer per utterance in a meeting: %v", v)
	}

	// .
	if got := a.voiceOpenMode("vs-3", "meeting"); got != "meeting" {
		t.Fatalf("meeting asked under meeting became %q", got)
	}

	// .
	// .
	// .
	voiceModeDoor(t, a, "off", "off")
	if got := a.voiceOpenMode("vs-4", "conversation"); got != "conversation" {
		t.Fatalf("listen=off refused a push-to-talk hold's lane: %q", got)
	}
	if v, ok := a.voiceModes.Load("vs-4"); !ok || v != true {
		t.Fatalf("a hold under listen=off must still be answered: %v", v)
	}
}

// .
// .
// .
// .
// .
func TestAWholeUtteranceUnderMeetingIsRecordedNotAnswered(t *testing.T) {
	srv := fakeEngine(t, "is anyone taking notes on this")
	defer srv.Close()
	a := speechApp(t, srv.URL)

	var mu sync.Mutex
	var woke []string
	prevWake := voiceWake
	voiceWake = func(_ *App, _ context.Context, role, text string) (string, error) {
		mu.Lock()
		woke = append(woke, role+": "+text)
		mu.Unlock()
		return "I hear you.", nil
	}
	t.Cleanup(func() { voiceWake = prevWake })

	// .
	// .
	if err := a.HearUtterance(context.Background(), make([]byte, 3200), 16000, 1, true); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	n := len(woke)
	mu.Unlock()
	if n != 1 {
		t.Fatalf("an utterance addressed to the identity in interactive mode woke it %d times", n)
	}
	before, err := a.store.RecentTurns(10)
	if err != nil {
		t.Fatal(err)
	}

	// .
	voiceModeDoor(t, a, "meeting", "off")
	if err := a.HearUtterance(context.Background(), make([]byte, 3200), 16000, 1, true); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := append([]string(nil), woke...)
	mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("a meeting utterance woke the identity: %v", got)
	}
	after, err := a.store.RecentTurns(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before)+1 {
		t.Fatalf("a meeting utterance was not recorded: %d turns, was %d", len(after), len(before))
	}
	if after[0].Role != roleOperator || !strings.Contains(after[0].Content, "taking notes") {
		t.Fatalf("what a meeting recorded is not the operator's own words: %+v", after[0])
	}
	// .
	if a.TurnActive() {
		t.Fatal("a meeting utterance kept the turn gate")
	}
}

// .
// .
// .
// .
// .
func TestASilentModeSpeaksNoReplyAndEnqueuesNothing(t *testing.T) {
	a := newVoiceApp(t)
	f := &fakeEngineSession{}
	const id = "vs-1"
	h := &voiceHandle{id: id, v: f, done: make(chan struct{})}
	a.voiceSessions.Store(id, h)
	var mu sync.Mutex
	var refs []dashboard.VoiceReplyRef
	var texts []string
	a.voiceReplySink = func(ref dashboard.VoiceReplyRef, text string) {
		mu.Lock()
		refs = append(refs, ref)
		texts = append(texts, text)
		mu.Unlock()
	}
	ctx := context.Background()

	// .
	if v := a.synthesizeReply(ctx, id, a.voiceGen(id), "the spoken answer"); v != replyAdmitted {
		t.Fatalf("an eligible reply under auto = %v", v)
	}
	if got := f.synthed(); len(got) != 1 {
		t.Fatalf("the engine was given %d replies under auto: %v", len(got), got)
	}

	voiceModeDoor(t, a, "meeting", "off")
	if v := a.synthesizeReply(ctx, id, a.voiceGen(id), "the quiet answer"); v != replyAdmitted {
		t.Fatalf("a reply delivered as text = %v; nothing more is owed it", v)
	}
	if got := f.synthed(); len(got) != 1 {
		t.Fatalf("the engine was handed a reply nobody would hear: %v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(refs) != 2 {
		t.Fatalf("the reply reached the page %d times: %+v", len(refs), refs)
	}
	if refs[1].Route != "plugin" || !refs[1].TextOnly || refs[1].SynthesisID != "" || refs[1].SessionID != id {
		t.Fatalf("a reply nobody speaks must carry the disposition no route speaks: %+v", refs[1])
	}
	if texts[1] != "the quiet answer" {
		t.Fatalf("the words still reach the operator: %q", texts[1])
	}
	if outcome, _ := h.replyOutcome.Load().(string); outcome != replyNotSpokenOutcome {
		t.Fatalf("the drain would not say why nothing was spoken: %q", outcome)
	}

	// .
	// .
	h.supersede()
	if v := a.synthesizeReply(ctx, id, 0, "a stale answer"); v != replySuperseded {
		t.Fatalf("a stale answer under speak=off = %v", v)
	}
	if len(refs) != 2 {
		t.Fatalf("the mode's gate delivered a superseded reply: %+v", refs)
	}
}

// .
// .
// .
// .
func TestADefinitelyRefusedReplyIsNotSpokenWhileTheModesSpeakIsOff(t *testing.T) {
	a := newVoiceApp(t)
	f := &fakeEngineSession{}
	const id = "vs-2"
	h := &voiceHandle{id: id, v: f, done: make(chan struct{})}
	a.voiceSessions.Store(id, h)
	var refs []dashboard.VoiceReplyRef
	a.voiceReplySink = func(ref dashboard.VoiceReplyRef, _ string) { refs = append(refs, ref) }
	minted := 0
	prevMint := voiceFallbackMint
	voiceFallbackMint = func(*App, string) string { minted++; return "cloud-1" }
	t.Cleanup(func() { voiceFallbackMint = prevMint })

	voiceModeDoor(t, a, "interactive", "on")
	b := &voiceBinding{session: id, gen: h.gen.Load()}
	if !a.speakFallback(context.Background(), b, "the refused answer") {
		t.Fatal("with speak on the configured voice takes a definitely refused reply over")
	}
	if minted != 1 || len(refs) != 1 || refs[0].Route != "cloud" || !refs[0].Fallback || refs[0].TextOnly {
		t.Fatalf("the takeover under speak=on: minted=%d refs=%+v", minted, refs)
	}

	voiceModeDoor(t, a, "meeting", "off")
	b2 := &voiceBinding{session: id, gen: h.gen.Load()}
	if !a.speakFallback(context.Background(), b2, "the other refused answer") {
		t.Fatal("the reply still reaches the operator as text")
	}
	if minted != 1 {
		t.Fatalf("a voice was bought while the mode's speak is off: %d mints", minted)
	}
	if len(refs) != 2 || refs[1].Route != "plugin" || !refs[1].TextOnly || refs[1].Fallback {
		t.Fatalf("the page was handed a reply its own voice would read aloud: %+v", refs[1])
	}
	if outcome, _ := h.replyOutcome.Load().(string); outcome != replyNotSpokenOutcome {
		t.Fatalf("outcome = %q", outcome)
	}
}

// .
// .
// .
// .
func TestTheCloudVoiceRefusesWhileTheModesSpeakIsOff(t *testing.T) {
	a := newVoiceApp(t)
	path := filepath.Join(filepath.Dir(a.cfg.SourcePath), "providers.json")
	if err := os.WriteFile(path, []byte(`{"providers":[{"name":"my-voice","url":"http://127.0.0.1:9","api_key":"vk-1234"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a.cfg.Speech.TTS = TTSConfig{Provider: "my-voice", Model: "m", Voice: "v"}

	// .
	if _, err := a.speakMint(dashboard.SpeakText{Text: "hello"}); err != nil {
		t.Fatalf("auto refused a reply: %v", err)
	}

	voiceModeDoor(t, a, "meeting", "off")
	_, err := a.speakMint(dashboard.SpeakText{Text: "hello again"})
	if !errors.Is(err, errSpeakOff) {
		t.Fatalf("the cloud voice spoke while the mode's speak is off: %v", err)
	}
	// .
	for _, want := range []string{"the voice mode's speak is off", "microphone control", "voice.mode"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the operator is not told %q: %q", want, err)
		}
	}
	// .
	// .
	// .
	if _, err := a.speakMint(dashboard.SpeakText{Sample: true, Provider: "my-voice", Voice: "v2"}); errors.Is(err, errSpeakOff) {
		t.Fatalf("an audition was refused as if it were a reply: %v", err)
	}

	voiceModeDoor(t, a, "meeting", "on")
	if _, err := a.speakMint(dashboard.SpeakText{Text: "hello once more"}); err != nil {
		t.Fatalf("speak=on refused a reply: %v", err)
	}
}
