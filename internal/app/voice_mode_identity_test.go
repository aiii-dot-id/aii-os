package app

import (
	"os"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/identity"
)

// .
// .
// .
// .

// .
// .
// .
func countingScreens(t *testing.T, a *App) (pushes, configPulls *int) {
	t.Helper()
	p, c := 0, 0
	prev := pushVoiceStatus
	t.Cleanup(func() { pushVoiceStatus = prev })
	pushVoiceStatus = func(*App) { p++ }
	a.dashboard = dashboard.New("127.0.0.1", 0, &dashboard.WSHandler{
		GetConfig: func() (*dashboard.ConfigState, error) { c++; return &dashboard.ConfigState{}, nil },
	})
	return &p, &c
}

// .
// .
// .
// .
// .
func TestTheIdentityAdvertisesTheHostsVoiceVocabulary(t *testing.T) {
	props, _ := lookupWorkVerb(t).Params["properties"].(map[string]interface{})
	for _, tc := range []struct{ param, want string }{
		{"listen", listenValues},
		{"speak", speakValues},
	} {
		p, _ := props[tc.param].(map[string]interface{})
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

// .
// .
// .
// .
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

	// .
	// .
	// .
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
	// .
	// .
	// .
	if _, err := port.SetMode("interactive", speakAuto); err != nil {
		t.Fatal(err)
	}
	if got := a.configSnapshot().Speech.Mode; got.Listen != "interactive" || got.Speak != "off" || got.Revision != 3 {
		t.Fatalf("auto from the engine overwrote the speak in force: %+v", got)
	}
}

// .
// .
// .
// .
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

	// .
	deaf := newVoiceApp(t)
	countingScreens(t, deaf)
	for _, listen := range []string{listenInteractive, listenMeeting} {
		_, err := (voiceModeAdapter{deaf}).SetMode(listen, speakOff)
		if err == nil || !strings.Contains(err.Error(), "no microphone is offered") || !strings.Contains(err.Error(), "Settings → Speech") {
			t.Fatalf("listen=%s on a host with no microphone: %v", listen, err)
		}
	}
	// .
	// .
	// .
	for _, pair := range [][2]string{{listenOff, speakOn}, {listenOff, speakOff}} {
		if _, err := (voiceModeAdapter{deaf}).SetMode(pair[0], pair[1]); err != nil {
			t.Fatalf("listen=%s speak=%s was refused with no microphone: %v", pair[0], pair[1], err)
		}
	}

	// .
	// .
	unreachable := speechApp(t, srv.URL)
	unreachable.cfg.Speech.STT.Provider = "not-in-providers"
	_, err := (voiceModeAdapter{unreachable}).SetMode(listenInteractive, speakOn)
	if err == nil || !strings.Contains(err.Error(), "not-in-providers") {
		t.Fatalf("an unreachable microphone refused without its reason: %v", err)
	}
}

// .
// .
// .
// .
func TestTheWorkingStateCarriesTheVoiceMode(t *testing.T) {
	a := newVoiceApp(t)
	ws, err := a.buildWorkState()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ws, "### Voice") {
		t.Fatalf("a host with no microphone and no reply voice named a voice mode:\n%s", ws)
	}

	// .
	// .
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

	// .
	prev := activeVoicePlugin
	t.Cleanup(func() { activeVoicePlugin = prev })
	activeVoicePlugin = func(*App) string { return "id.test.voice" }

	for _, tc := range []struct{ listen, speak, want string }{
		{"", "", "### Voice — interactive (listen: interactive, speak: auto)"},
		{listenOff, speakOn, "### Voice — earbuds (listen: off, speak: on)"},
		{listenMeeting, speakOff, "### Voice — meeting (listen: meeting, speak: off)"},
		{listenOff, speakOff, "### Voice — off (listen: off, speak: off)"},
		{listenMeeting, speakOn, "### Voice — meeting, spoken replies (listen: meeting, speak: on)"},
		{listenInteractive, speakOff, "### Voice — interactive, text replies (listen: interactive, speak: off)"},
	} {
		voiceModeDoor(t, a, tc.listen, tc.speak)
		line := a.voiceWorkStateLine()
		if !strings.HasPrefix(line, tc.want) {
			t.Errorf("(%q, %q) rendered %q, want it to begin %q", tc.listen, tc.speak, line, tc.want)
		}
		if !strings.Contains(line, "work action=voice.mode") {
			t.Errorf("(%q, %q) does not say how to change it: %q", tc.listen, tc.speak, line)
		}
		// .
		// .
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

	// .
	// .
	// .
	// .
	a.enterSafe("test: the record is frozen")
	if line := a.voiceWorkStateLine(); line != "" {
		t.Fatalf("SAFE closed the channel and the line still described it: %q", line)
	}
}
