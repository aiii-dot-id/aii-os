package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost/pluginhosttest"
)

// speech_route_selection_test.go — the engine that hears or speaks is the
// one the operator chose for that half of speech (VDR-03).
//
// Settings → Speech lets the operator name a service or an engine for
// hearing and for speaking, and its readback honoured the choice. The
// paths that act on it did not: a session opened on the first engine
// running whatever was chosen, a typed reply went to whatever output
// session was open, and the microphone compared the choice with the first
// engine only — so a second engine that was chosen read as nothing set
// up. These assert the OWNER: which engine's lane a session's audio and
// control reach, and which session a typed reply is given to — never what
// a picker lists.

// runningEngine activates a speech engine the way an activation does — a
// live session lane under its plugin id, after any already running — and
// answers how to end that lane, as a crash or a deactivation would.
func runningEngine(t *testing.T, a *App, id string) (*pluginhost.ActivePlugin, func()) {
	t.Helper()
	c := pluginhosttest.SessionClientFrames(make(chan []byte), io.Discard, 32)
	v := pluginhost.NewVoiceSession(c)
	ctx, cancel := context.WithCancel(context.Background())
	ran := make(chan struct{})
	go func() { defer close(ran); _ = c.Run(ctx) }()
	stop := func() {
		cancel()
		select {
		case <-ran:
		case <-time.After(5 * time.Second):
			t.Errorf("the lane of %s did not end", id)
		}
	}
	t.Cleanup(stop)
	ap := pluginWithTestVoice(&pluginhost.ActivePlugin{ID: id, Title: id}, v)
	a.pluginMu.Lock()
	a.plugins = append(a.plugins, ap)
	a.pluginMu.Unlock()
	return ap, stop
}

// speechRouteApp is an application with the two speech pointers given, a page's
// microphone and two speakers on its plane, and a providers file naming
// the hearing service — so choosing it resolves without a network.
func speechRouteApp(t *testing.T, stt, tts string) *App {
	t.Helper()
	dir := t.TempDir()
	a := New(&Config{SourcePath: filepath.Join(dir, "config.json"), Speech: SpeechConfig{
		STT: STTConfig{Provider: stt, Model: "nova-3"}, TTS: TTSConfig{Provider: tts}}})
	t.Cleanup(a.bgCancel)
	if err := os.WriteFile(filepath.Join(dir, "providers.json"),
		[]byte(`{"providers":[{"name":"Deepgram","url":"http://127.0.0.1:1","api_key":"synthetic"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	plane := a.AudioPlane()
	for _, ep := range []*audio.Endpoint{
		{ID: "mic", Source: &liveSource{f: mono16k}},
		{ID: "spk", Sink: audio.NewCaptureSink(mono16k)},
		{ID: "spk-2", Sink: audio.NewCaptureSink(mono16k)},
		{ID: "spk-3", Sink: audio.NewCaptureSink(mono16k)},
	} {
		if err := plane.Register(ep); err != nil {
			t.Fatal(err)
		}
	}
	return a
}

// chooseSpeech changes the pointers in force, as a save through the door does.
func chooseSpeech(a *App, stt, tts string) {
	a.cfgMu.Lock()
	a.cfg.Speech.STT.Provider, a.cfg.Speech.TTS.Provider = stt, tts
	a.cfgMu.Unlock()
}

// opensReach replaces the engine's open with a record of WHICH engine's
// lane each open reached, and admits it: the session the application then
// holds is the real one, its control on the lane that was opened.
func opensReach(t *testing.T) func() []*pluginhost.VoiceSession {
	t.Helper()
	var mu sync.Mutex
	var reached []*pluginhost.VoiceSession
	prev := voiceEngineOpen
	t.Cleanup(func() { voiceEngineOpen = prev })
	voiceEngineOpen = func(v *pluginhost.VoiceSession, _ context.Context, _ string, _ *audio.Binding, _ map[string]any) error {
		mu.Lock()
		defer mu.Unlock()
		reached = append(reached, v)
		return nil
	}
	return func() []*pluginhost.VoiceSession {
		mu.Lock()
		defer mu.Unlock()
		return append([]*pluginhost.VoiceSession(nil), reached...)
	}
}

// typedRepliesReach records which session each typed reply was given to
// for its engine to speak, and admits it.
func typedRepliesReach(t *testing.T) func() []string {
	t.Helper()
	var mu sync.Mutex
	var given []string
	prev := voiceSynthesize
	t.Cleanup(func() { voiceSynthesize = prev })
	voiceSynthesize = func(_ *App, _ context.Context, sessionID string, _ uint64, _ string) replyVerdict {
		mu.Lock()
		defer mu.Unlock()
		given = append(given, sessionID)
		return replyAdmitted
	}
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), given...)
	}
}

func openHearing(a *App) (*voiceHandle, error) {
	s, err := a.OpenVoiceSession(context.Background(), "mic", "spk", "conversation", nil)
	if err != nil {
		return nil, err
	}
	return s.(*voiceHandle), nil
}

func openSpeaking(a *App, speaker string) (*voiceHandle, error) {
	s, err := a.OpenVoiceSession(context.Background(), "", speaker, voiceOutputMode, nil)
	if err != nil {
		return nil, err
	}
	return s.(*voiceHandle), nil
}

// lanesOf names the engines whose lanes were reached, for a failure that
// says where the audio went.
func lanesOf(reached []*pluginhost.VoiceSession, engines ...*pluginhost.ActivePlugin) []string {
	var out []string
	for _, v := range reached {
		name := "an unknown lane"
		for _, ap := range engines {
			if ap.Voice.Load() == v {
				name = ap.ID
			}
		}
		out = append(out, name)
	}
	return out
}

// A SECOND ENGINE THAT WAS CHOSEN IS THE ONE THAT HEARS AND SPEAKS. The
// microphone compared the choice with the first engine running only, and
// the service resolver then found an engine where it looks for a service,
// so the chosen engine read as `setup`; a session the page opened anyway
// went to the first engine.
func TestAChosenSecondEngineIsTheOneThatHearsAndSpeaks(t *testing.T) {
	a := speechRouteApp(t, "id.engine.second", "id.engine.second")
	first, _ := runningEngine(t, a, "id.engine.first")
	second, _ := runningEngine(t, a, "id.engine.second")
	reached := opensReach(t)
	given := typedRepliesReach(t)

	if state, reason, source := a.VoiceStatus(); state != "plugin" || source != second.ID {
		t.Fatalf("the chosen second engine must be the microphone offered: %q %q (%q)", state, source, reason)
	}
	h, err := openHearing(a)
	if err != nil {
		t.Fatalf("the session the page was offered did not open: %v", err)
	}
	if r := reached(); len(r) != 1 || r[0] != second.Voice.Load() {
		t.Fatalf("hearing opened on %v, want %s", lanesOf(r, first, second), second.ID)
	}
	if h.v != engineSession(second.Voice.Load()) {
		t.Fatal("the session's control is not on the lane it opened on")
	}
	h.Close(context.Background(), "abort") // the page went to earbuds

	out, err := openSpeaking(a, "spk-2")
	if err != nil {
		t.Fatalf("the output session did not open: %v", err)
	}
	if r := reached(); len(r) != 2 || r[1] != second.Voice.Load() {
		t.Fatalf("speaking opened on %v, want %s last", lanesOf(r, first, second), second.ID)
	}
	a.settleVoice(context.Background(), "an answer to typed words")
	if g := given(); len(g) != 1 || g[0] != out.id {
		t.Fatalf("the typed reply was given to %v, want the chosen engine's output session %s", g, out.id)
	}
}

// A SERVICE CHOSEN FOR HEARING KEEPS THE ENGINE OUT OF IT — and keeps the
// engine as the voice when speaking was left to it. Push-to-talk beside a
// chosen service opened a session on the engine because an engine was
// available; the host now refuses that open, and the refusal takes nothing
// from the engine's output session.
func TestAServiceChosenForHearingKeepsTheEngineOutOfIt(t *testing.T) {
	a := speechRouteApp(t, "Deepgram", "")
	engine, _ := runningEngine(t, a, "id.engine.only")
	reached := opensReach(t)
	given := typedRepliesReach(t)

	if state, _, source := a.VoiceStatus(); state != "cloud" || !strings.HasPrefix(source, "Deepgram") {
		t.Fatalf("the chosen service must be the microphone offered: %q (%q)", state, source)
	}
	// Speaking was left to the default, which is the engine: earbuds hold
	// its output session.
	out, err := openSpeaking(a, "spk-2")
	if err != nil || len(reached()) != 1 || reached()[0] != engine.Voice.Load() {
		t.Fatalf("speaking left to the default did not open on the engine: %v %v", err, lanesOf(reached(), engine))
	}
	// A hearing session is the engine's only when the engine was chosen to hear.
	if h, err := openHearing(a); err == nil {
		t.Fatalf("a hearing session opened on %v beside a chosen service (%s)", lanesOf(reached(), engine), h.id)
	}
	if n := len(reached()); n != 1 {
		t.Fatalf("a refused hearing open still reached an engine: %v", lanesOf(reached(), engine))
	}
	if out.closing.Load() {
		t.Fatal("a refused hearing open took the engine's output session away")
	}
	// What the service heard is answered in the engine's voice, through
	// that output session: hearing and speaking are chosen apart.
	a.settleVoice(context.Background(), "an answer to what the service heard")
	if g := given(); len(g) != 1 || g[0] != out.id {
		t.Fatalf("the reply was given to %v, want the engine's output session %s", g, out.id)
	}
}

// A SERVICE CHOSEN FOR SPEAKING KEEPS TYPED REPLIES OFF THE ENGINE. In
// earbuds the page opened the engine's output lane whatever speaking was
// set to, and the typed reply was then bound to it — so the configured
// service never spoke. The host now opens no output session for a service,
// and a session still open from before the choice is not given replies.
func TestAServiceChosenForSpeakingKeepsTypedRepliesOffTheEngine(t *testing.T) {
	a := speechRouteApp(t, "", "")
	engine, _ := runningEngine(t, a, "id.engine.only")
	reached := opensReach(t)
	given := typedRepliesReach(t)

	stale, err := openSpeaking(a, "spk-2") // opened while the engine was chosen to speak
	if err != nil {
		t.Fatal(err)
	}
	chooseSpeech(a, "", "ElevenLabs")
	if v := a.replyVoice(); v != "ElevenLabs" {
		t.Fatalf("the page must be told the service speaks: %q", v)
	}
	if h, err := openSpeaking(a, "spk-3"); err == nil {
		t.Fatalf("an output session opened on %v while a service is chosen to speak (%s)", lanesOf(reached(), engine), h.id)
	}
	if n := len(reached()); n != 1 {
		t.Fatalf("a refused output open still reached an engine: %v", lanesOf(reached(), engine))
	}
	a.settleVoice(context.Background(), "typed in earbuds")
	if g := given(); len(g) != 0 {
		t.Fatalf("a typed reply was given to the engine's output session %v (stale %s) while a service is chosen to speak", g, stale.id)
	}
	if a.voiceReplyShown.Load() {
		t.Fatal("the reply was claimed by the voice path; the turn's own path must show it, for the service to speak")
	}
	// Hearing is still the engine's by default: the choices are apart.
	if state, _, source := a.VoiceStatus(); state != "plugin" || source != engine.ID {
		t.Fatalf("hearing left to the default is the engine's: %q (%q)", state, source)
	}
	if _, err := openHearing(a); err != nil || len(reached()) != 2 || reached()[1] != engine.Voice.Load() {
		t.Fatalf("hearing left to the default did not open on the engine: %v %v", err, lanesOf(reached(), engine))
	}
}

// EACH HALF OPENS ON THE ENGINE CHOSEN FOR IT, and one half takes nothing
// from the other.
func TestEachHalfOfSpeechOpensOnTheEngineChosenForIt(t *testing.T) {
	a := speechRouteApp(t, "id.engine.first", "id.engine.second")
	first, _ := runningEngine(t, a, "id.engine.first")
	second, _ := runningEngine(t, a, "id.engine.second")
	reached := opensReach(t)
	given := typedRepliesReach(t)

	out, err := openSpeaking(a, "spk-2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := openHearing(a); err != nil {
		t.Fatal(err)
	}
	if r := reached(); len(r) != 2 || r[0] != second.Voice.Load() || r[1] != first.Voice.Load() {
		t.Fatalf("opens reached %v, want speaking on %s then hearing on %s", lanesOf(r, first, second), second.ID, first.ID)
	}
	if out.closing.Load() {
		t.Fatalf("hearing on %s took the output session of %s away", first.ID, second.ID)
	}
	a.settleVoice(context.Background(), "typed beside a conversation")
	if g := given(); len(g) != 1 || g[0] != out.id {
		t.Fatalf("the typed reply was given to %v, want %s's output session %s", g, second.ID, out.id)
	}
}

// A CHOSEN ENGINE THAT STOPS IS NOT REPLACED BY ANOTHER. Between the status
// that offered its conversation and the page's open, the chosen engine's
// lane ended: the open is refused, naming it, and reaches no other engine.
// With nothing chosen the engine running is the default, as before.
func TestAChosenEngineThatStopsIsNotReplacedByAnother(t *testing.T) {
	a := speechRouteApp(t, "id.engine.second", "id.engine.second")
	first, _ := runningEngine(t, a, "id.engine.first")
	second, stopSecond := runningEngine(t, a, "id.engine.second")
	reached := opensReach(t)

	if state, _, source := a.VoiceStatus(); state != "plugin" || source != second.ID {
		t.Fatalf("fixture: the chosen engine is not the microphone offered: %q (%q)", state, source)
	}
	stopSecond()
	if second.Voice.Load().Alive() {
		t.Fatal("fixture: the chosen engine's lane did not end")
	}
	if _, err := openHearing(a); err == nil || !strings.Contains(err.Error(), second.ID) {
		t.Fatalf("hearing on a chosen engine that stopped must be refused, naming it: %v", err)
	}
	if _, err := openSpeaking(a, "spk-2"); err == nil || !strings.Contains(err.Error(), second.ID) {
		t.Fatalf("speaking on a chosen engine that stopped must be refused, naming it: %v", err)
	}
	if r := reached(); len(r) != 0 {
		t.Fatalf("a session opened on %v in place of the chosen engine", lanesOf(r, first, second))
	}
	if state, _, source := a.VoiceStatus(); state == "plugin" || source == first.ID {
		t.Fatalf("the microphone offered another engine's conversation in place of the chosen one: %q (%q)", state, source)
	}

	chooseSpeech(a, "", "")
	if state, _, source := a.VoiceStatus(); state != "plugin" || source != first.ID {
		t.Fatalf("with nothing chosen the engine running is the default: %q (%q)", state, source)
	}
	if _, err := openHearing(a); err != nil || len(reached()) != 1 || reached()[0] != first.Voice.Load() {
		t.Fatalf("with nothing chosen hearing opens on the engine running: %v %v", err, lanesOf(reached(), first, second))
	}
}
