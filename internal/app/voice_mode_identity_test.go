package app

import (
	"os"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/identity"
)

// voice_mode_identity_test.go — the identity's half of the voice mode:
// the port it changes the pair through, and the line it reads the pair
// in. The pair has ONE owner and ONE door; the adapter is the identity's
// way to that door and never a second writer.

// countingScreens attaches a dashboard that counts the pulls a
// broadcast makes, and a status push counter: a change nothing is told
// about is a change two screens can disagree about.
func countingScreens(t *testing.T, a *App) (pushes, configPulls *int) {
	t.Helper()
	p, c := 0, 0
	prev := pushVoiceStatus
	t.Cleanup(func() { pushVoiceStatus = prev })
	pushVoiceStatus = func(*App) { p++ }
	a.dashboard = dashboard.New("127.0.0.1", 0, &dashboard.WSHandler{
		GetConfig: func() *dashboard.ConfigState { c++; return &dashboard.ConfigState{} },
	})
	return &p, &c
}

// ONE VOCABULARY, TWO PACKAGES. The identity cannot import the
// substrate, so its verb mirrors the halves' value sets at the port
// boundary. The host owns them: if a half ever gains or loses a value
// here, the advertisement the identity is given must move with it, and
// this is the only place both can be seen at once.
func TestTheIdentityAdvertisesTheHostsVoiceVocabulary(t *testing.T) {
	props, _ := lookupWorkVerb(t).Params["properties"].(map[string]any)
	for _, tc := range []struct{ param, want string }{
		{"listen", listenValues},
		{"speak", speakValues},
	} {
		p, _ := props[tc.param].(map[string]any)
		vals, _ := p["enum"].([]string)
		if got := strings.Join(vals, ", "); got != tc.want {
			t.Errorf("the identity is offered %s = %q while the host takes %q", tc.param, got, tc.want)
		}
	}
}

func lookupWorkVerb(t *testing.T) identity.Verb {
	t.Helper()
	for _, v := range identity.Verbs() {
		if v.Name == "work" {
			return v
		}
	}
	t.Fatal("work is not offered")
	return identity.Verb{}
}

// THE ADAPTER IS NOT A SECOND WRITER. The pair reaches config through
// the one door the page uses — so it is validated, revised, persisted
// and broadcast exactly once, by the owner. Writing the field directly
// would leave the revision at zero and the file without it.
func TestTheIdentityChangesTheVoiceModeThroughTheDoor(t *testing.T) {
	srv := fakeEngine(t, "")
	defer srv.Close()
	a := speechApp(t, srv.URL)
	pushes, configPulls := countingScreens(t, a)
	port := voiceModeAdapter{a}

	if listen, speak, set := port.Mode(); listen != "interactive" || speak != "auto" || set {
		t.Fatalf("Mode() = (%s, %s, %v), want the host's own unset answer", listen, speak, set)
	}

	out, err := port.SetMode("meeting", "off")
	if err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	got := a.configSnapshot().Speech.Mode
	if got.Listen != "meeting" || got.Speak != "off" || got.Revision != 1 {
		t.Fatalf("the mode in config is %+v — the door was not the way in", got)
	}
	blob, rerr := os.ReadFile(a.cfg.SourcePath)
	if rerr != nil || !strings.Contains(string(blob), "meeting") {
		t.Fatalf("the change did not reach the file the door persists to: %v %s", rerr, blob)
	}
	for _, want := range []string{"meeting (listen: meeting, speak: off)", "not addressed to you", "your replies are text"} {
		if !strings.Contains(out, want) {
			t.Errorf("the sentence the identity reads omits %q: %q", want, out)
		}
	}
	if *pushes != 1 || *configPulls != 1 {
		t.Fatalf("the screens were told %d times about the microphone and %d times about the configuration, want 1 and 1", *pushes, *configPulls)
	}

	// A HALF THE IDENTITY DID NOT NAME ARRIVES EMPTY and is the door's to
	// keep: the speak in force (off) survives a change to the other half,
	// and nothing the engine read a moment earlier is written back.
	if _, err := port.SetMode("off", ""); err != nil {
		t.Fatal(err)
	}
	got = a.configSnapshot().Speech.Mode
	if got.Listen != "off" || got.Speak != "off" || got.Revision != 2 {
		t.Fatalf("the half not named was not kept by the door: %+v", got)
	}
	if listen, speak, set := port.Mode(); listen != "off" || speak != "off" || !set {
		t.Fatalf("Mode() = (%s, %s, %v) after the change", listen, speak, set)
	}
	// The word auto from the engine is also "not named": stored as
	// nothing at all only where nothing was in force (the page's own
	// per-reply rule), never written over an off.
	if _, err := port.SetMode("interactive", speakAuto); err != nil {
		t.Fatal(err)
	}
	if got := a.configSnapshot().Speech.Mode; got.Listen != "interactive" || got.Speak != "off" || got.Revision != 3 {
		t.Fatalf("auto from the engine overwrote the speak in force: %+v", got)
	}
}

// TWO REFUSALS ONLY THE HOST CAN SEE: SAFE, where nothing about this
// identity changes, and a listening mode on a host that offers no
// microphone — which would otherwise be set, reported as set, and never
// heard in.
func TestTheVoiceModeRefusesUnderSafeAndWithNoMicrophone(t *testing.T) {
	srv := fakeEngine(t, "")
	defer srv.Close()
	safe := speechApp(t, srv.URL)
	safe.enterSafe("test: the record is frozen")
	if _, err := (voiceModeAdapter{safe}).SetMode("meeting", "off"); err == nil || !strings.Contains(err.Error(), "SAFE") {
		t.Fatalf("SAFE did not hold the mode: %v", err)
	}
	if got := safe.configSnapshot().Speech.Mode; got != (VoiceModeConfig{}) {
		t.Fatalf("a refusal changed the mode: %+v", got)
	}

	// Nothing configured and no engine installed: no microphone.
	deaf := newVoiceApp(t)
	countingScreens(t, deaf)
	for _, listen := range []string{listenInteractive, listenMeeting} {
		_, err := (voiceModeAdapter{deaf}).SetMode(listen, speakOff)
		if err == nil || !strings.Contains(err.Error(), "no microphone is offered") || !strings.Contains(err.Error(), "Settings → Speech") {
			t.Fatalf("listen=%s on a host with no microphone: %v", listen, err)
		}
	}
	// HEARING NOTHING NEEDS NO MICROPHONE: earbuds and off are exactly
	// the modes an operator without one wants, and refusing them would
	// leave the identity unable to stop listening.
	for _, pair := range [][2]string{{listenOff, speakOn}, {listenOff, speakOff}} {
		if _, err := (voiceModeAdapter{deaf}).SetMode(pair[0], pair[1]); err != nil {
			t.Fatalf("listen=%s speak=%s was refused with no microphone: %v", pair[0], pair[1], err)
		}
	}

	// A microphone that is configured and cannot be reached says why,
	// rather than inviting the identity to listen into it.
	unreachable := speechApp(t, srv.URL)
	unreachable.cfg.Speech.STT.Provider = "not-in-providers"
	_, err := (voiceModeAdapter{unreachable}).SetMode(listenInteractive, speakOn)
	if err == nil || !strings.Contains(err.Error(), "not-in-providers") {
		t.Fatalf("an unreachable microphone refused without its reason: %v", err)
	}
}

// THE IDENTITY IS TOLD. The mode is state it holds across turns and may
// change, so it renders beside the standing state — and only where there
// is a voice at all, because a mode nothing can act on is machinery the
// identity would have to learn to discount.
func TestTheWorkingStateCarriesTheVoiceMode(t *testing.T) {
	a := newVoiceApp(t)
	ws, err := a.buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ws, "### Voice") {
		t.Fatalf("a host with no microphone and no reply voice named a voice mode:\n%s", ws)
	}

	// A REPLY VOICE IS A VOICE: the identity is heard even where nothing
	// hears it.
	a.cfg.Speech.TTS.Provider = "local-speech"
	ws, err = a.buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ws, "### Voice — interactive (listen: interactive, speak: auto)") {
		t.Fatalf("the unset mode did not render:\n%s", ws)
	}
	if !strings.Contains(ws, "work action=voice.mode") {
		t.Fatalf("the identity is not told how to change it:\n%s", ws)
	}
	a.cfg.Speech.TTS.Provider = ""

	// A microphone alone is a voice too.
	prev := activeVoicePlugin
	t.Cleanup(func() { activeVoicePlugin = prev })
	activeVoicePlugin = func(*App, string) string { return "id.test.voice" }

	for _, tc := range []struct{ listen, speak, want string }{
		{"", "", "### Voice — interactive (listen: interactive, speak: auto)"},
		{listenOff, speakOn, "### Voice — earbuds (listen: off, speak: on)"},
		{listenMeeting, speakOff, "### Voice — meeting (listen: meeting, speak: off)"},
		{listenOff, speakOff, "### Voice — off (listen: off, speak: off)"},
		{listenMeeting, speakOn, "### Voice — meeting, spoken replies (listen: meeting, speak: on)"},
		{listenInteractive, speakOff, "### Voice — interactive, text replies (listen: interactive, speak: off)"},
	} {
		voiceModeDoor(t, a, tc.listen, tc.speak) // through the door: the mode is published, never poked
		line := a.voiceWorkStateLine()
		if !strings.HasPrefix(line, tc.want) {
			t.Errorf("(%q, %q) rendered %q, want it to begin %q", tc.listen, tc.speak, line, tc.want)
		}
		if !strings.Contains(line, "work action=voice.mode") {
			t.Errorf("(%q, %q) does not say how to change it: %q", tc.listen, tc.speak, line)
		}
		// THE LINE HAS A BUDGET: it renders every turn, beside
		// everything else the identity must read.
		if n := len(line); n >= 300 {
			t.Errorf("(%q, %q) is %d bytes, over the 300-byte budget: %q", tc.listen, tc.speak, n, line)
		}
		ws, err := a.buildWorkState()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(ws, line) {
			t.Errorf("(%q, %q) did not reach the working state:\n%s", tc.listen, tc.speak, ws)
		}
	}

	// SAFE PAUSES EVERY MICROPHONE, and a mode nothing can act on is not
	// rendered: under SAFE the host offers no microphone, this identity
	// has no reply voice, and the line goes quiet rather than describing
	// a channel that is closed and a change that would be refused.
	a.enterSafe("test: the record is frozen")
	if line := a.voiceWorkStateLine(); line != "" {
		t.Fatalf("SAFE closed the channel and the line still described it: %q", line)
	}
}
