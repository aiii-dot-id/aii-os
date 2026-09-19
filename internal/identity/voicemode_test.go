package identity

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// .
// .
// .

type fakeVoicePort struct {
	listen, speak       string
	set                 bool
	gotListen, gotSpeak string
	calls               int
	err                 error
}

func (p *fakeVoicePort) Mode() (string, string, bool) { return p.listen, p.speak, p.set }

func (p *fakeVoicePort) SetMode(listen, speak string) (string, error) {
	p.calls++
	p.gotListen, p.gotSpeak = listen, speak
	if p.err != nil {
		return "", p.err
	}
	// .
	// .
	if listen != "" {
		p.listen = listen
	}
	if speak != "" {
		p.speak = speak
	}
	p.set = true
	return fmt.Sprintf("the mode is now listen=%s speak=%s", p.listen, p.speak), nil
}

func voiceEngine(t *testing.T) (*Engine, *fakeVoicePort) {
	t.Helper()
	e, _, _, _, _ := setupEngine(t)
	port := &fakeVoicePort{listen: "interactive", speak: "auto"}
	e.SetVoice(port)
	return e, port
}

func setVoiceMode(t *testing.T, e *Engine, args map[string]interface{}) (string, error) {
	t.Helper()
	call := map[string]interface{}{"action": "voice.mode"}
	for k, v := range args {
		call[k] = v
	}
	return e.ExecuteAction(ctxBG(), "verb", "work", call)
}

// .
// .
func TestEachVoiceModeNameIsAPair(t *testing.T) {
	for _, tc := range []struct{ name, listen, speak string }{
		{"interactive", "interactive", "on"},
		{"earbuds", "off", "on"},
		{"meeting", "meeting", "off"},
		{"off", "off", "off"},
		{" Meeting ", "meeting", "off"},
	} {
		e, port := voiceEngine(t)
		out, err := setVoiceMode(t, e, map[string]interface{}{"mode": tc.name})
		if err != nil {
			t.Fatalf("mode=%q: %v", tc.name, err)
		}
		if port.gotListen != tc.listen || port.gotSpeak != tc.speak {
			t.Errorf("mode=%q reached the host as (%s, %s), want (%s, %s)", tc.name, port.gotListen, port.gotSpeak, tc.listen, tc.speak)
		}
		// .
		// .
		if want := fmt.Sprintf("the mode is now listen=%s speak=%s", tc.listen, tc.speak); out != want {
			t.Errorf("mode=%q answered %q, want the port's sentence %q", tc.name, out, want)
		}
	}
}

// .
// .
// .
func TestOneHalfOfTheVoiceModeKeepsTheOther(t *testing.T) {
	e, port := voiceEngine(t)
	port.listen, port.speak, port.set = "meeting", "auto", true

	if _, err := setVoiceMode(t, e, map[string]interface{}{"speak": "off"}); err != nil {
		t.Fatal(err)
	}
	// .
	// .
	// .
	// .
	if port.gotListen != "" || port.gotSpeak != "off" {
		t.Fatalf("speak= alone sent (%q, %q), want (\"\", off) — the half not named is the door's to keep", port.gotListen, port.gotSpeak)
	}
	if port.listen != "meeting" || port.speak != "off" {
		t.Fatalf("the pair in force after speak= alone is (%s, %s), want (meeting, off)", port.listen, port.speak)
	}

	port.listen, port.speak = "meeting", "auto"
	if _, err := setVoiceMode(t, e, map[string]interface{}{"listen": "off"}); err != nil {
		t.Fatal(err)
	}
	if port.gotListen != "off" || port.gotSpeak != "" {
		t.Fatalf("listen= alone sent (%q, %q), want (off, \"\") — the half not named is the door's to keep", port.gotListen, port.gotSpeak)
	}
	if port.listen != "off" || port.speak != "auto" {
		t.Fatalf("the pair in force after listen= alone is (%s, %s), want (off, auto) — unchanged", port.listen, port.speak)
	}

	// .
	// .
	// .
	if _, err := setVoiceMode(t, e, map[string]interface{}{"listen": "meeting", "speak": "on"}); err != nil {
		t.Fatalf("a pair with no name must not be refused: %v", err)
	}
	if port.gotListen != "meeting" || port.gotSpeak != "on" {
		t.Fatalf("the unnamed pair arrived as (%s, %s)", port.gotListen, port.gotSpeak)
	}
}

// .
// .
func TestVoiceModeRefusalsNameTheRemedy(t *testing.T) {
	// .
	bare, _, _, _, _ := setupEngine(t)
	if _, err := setVoiceMode(t, bare, map[string]interface{}{"mode": "meeting"}); err == nil || !strings.Contains(err.Error(), "this host has no voice") {
		t.Fatalf("an unwired host must refuse honestly: %v", err)
	}

	for _, tc := range []struct {
		name string
		args map[string]interface{}
		want string
	}{
		{"nothing at all", map[string]interface{}{}, "in your working state"},
		{"an empty name", map[string]interface{}{"mode": "  "}, "in your working state"},
		{"a name and a half", map[string]interface{}{"mode": "meeting", "speak": "on"}, "one or the other"},
		{"an unknown name", map[string]interface{}{"mode": "earmuffs"}, "interactive, earbuds, meeting, off"},
		{"an unknown listen", map[string]interface{}{"listen": "eavesdrop"}, "off, interactive, meeting"},
		{"an unknown speak", map[string]interface{}{"speak": "loud"}, "on, off"},
		{"a listen that is not a string", map[string]interface{}{"listen": 3}, "must be a string"},
	} {
		e, port := voiceEngine(t)
		out, err := setVoiceMode(t, e, tc.args)
		if err == nil {
			t.Errorf("%s was accepted: %q", tc.name, out)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s refused with %q, which does not name %q", tc.name, err, tc.want)
		}
		if port.calls != 0 {
			t.Errorf("%s reached the host anyway", tc.name)
		}
	}
}

// .
// .
// .
// .
func TestTheWorkFacadeAdvertisesTheVoiceMode(t *testing.T) {
	work := lookupVerb("work")
	if work == nil {
		t.Fatal("work is not in the registry")
	}
	props, _ := work.Params["properties"].(map[string]interface{})
	action, _ := props["action"].(map[string]interface{})
	actions, _ := action["enum"].([]string)
	if !slices.Contains(actions, "voice.mode") {
		t.Fatalf("the work action enum must advertise voice.mode: %v", actions)
	}
	if !strings.Contains(work.Description, "voice.mode") {
		t.Error("the work charter prose must name voice.mode among the absorbed actions")
	}
	for param, want := range map[string][]string{
		"mode":   voiceModeNameValues(),
		"listen": voiceListenValues,
		"speak":  voiceSpeakValues,
	} {
		p, ok := props[param].(map[string]interface{})
		if !ok {
			t.Errorf("work must advertise %s", param)
			continue
		}
		desc, _ := p["description"].(string)
		if !strings.HasPrefix(desc, "(voice.mode)") {
			t.Errorf("%s must name the mode that reads it: %q", param, desc)
		}
		vals, _ := p["enum"].([]string)
		if strings.Join(vals, "|") != strings.Join(want, "|") {
			t.Errorf("%s advertises %v, want %v — the advertisement and the verb must be one set", param, vals, want)
		}
	}
}
