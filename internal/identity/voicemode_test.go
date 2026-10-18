package identity

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// voicemode_test.go — work action=voice.mode: the identity's one way to
// change the host's voice mode. The names are labels on pairs, a half
// named alone keeps the other, and every refusal names the remedy.

type fakeVoicePort struct {
	listen, speak       string // the pair in force
	set                 bool
	gotListen, gotSpeak string // what SetMode was handed
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
	// As the host's door: a half handed over empty was not named, and
	// the value in force is kept for it inside the change.
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
	e.voice = port
	return e, port
}

func setVoiceMode(t *testing.T, e *Engine, args map[string]any) (string, error) {
	t.Helper()
	call := map[string]any{"action": "voice.mode"}
	for k, v := range args {
		call[k] = v
	}
	return e.ExecuteAction(ctxBG(), "verb", "work", call)
}

// Each name is a PAIR, and the pair is what reaches the host. A broken
// sugar row, or routing that never reaches the verb, fails here.
func TestEachVoiceModeNameIsAPair(t *testing.T) {
	for _, tc := range []struct{ name, listen, speak string }{
		{"interactive", "interactive", "on"},
		{"earbuds", "off", "on"},
		{"meeting", "meeting", "off"},
		{"off", "off", "off"},
		{" Meeting ", "meeting", "off"}, // the identity's spelling is forgiven
	} {
		e, port := voiceEngine(t)
		out, err := setVoiceMode(t, e, map[string]any{"mode": tc.name})
		if err != nil {
			t.Fatalf("mode=%q: %v", tc.name, err)
		}
		if port.gotListen != tc.listen || port.gotSpeak != tc.speak {
			t.Errorf("mode=%q reached the host as (%s, %s), want (%s, %s)", tc.name, port.gotListen, port.gotSpeak, tc.listen, tc.speak)
		}
		// The verb answers with the PORT's sentence — the host says what
		// is now true, not the engine's guess at it.
		if want := fmt.Sprintf("the mode is now listen=%s speak=%s", tc.listen, tc.speak); out != want {
			t.Errorf("mode=%q answered %q, want the port's sentence %q", tc.name, out, want)
		}
	}
}

// A HALF NAMED ALONE KEEPS THE OTHER. Changing how the identity is heard
// must not silently change whether it is heard — including when the half
// kept was never chosen (the host's auto rides back out untouched).
func TestOneHalfOfTheVoiceModeKeepsTheOther(t *testing.T) {
	e, port := voiceEngine(t)
	port.listen, port.speak, port.set = "meeting", "auto", true

	if _, err := setVoiceMode(t, e, map[string]any{"speak": "off"}); err != nil {
		t.Fatal(err)
	}
	// THE HALF NOT NAMED REACHES THE PORT EMPTY: the host's door keeps
	// the value in force for it inside its own transaction. Filled here
	// from a read of the mode, it was a copy of a moment ago, and put
	// back a speak the operator had turned off in between.
	if port.gotListen != "" || port.gotSpeak != "off" {
		t.Fatalf("speak= alone sent (%q, %q), want (\"\", off) — the half not named is the door's to keep", port.gotListen, port.gotSpeak)
	}
	if port.listen != "meeting" || port.speak != "off" {
		t.Fatalf("the pair in force after speak= alone is (%s, %s), want (meeting, off)", port.listen, port.speak)
	}

	port.listen, port.speak = "meeting", "auto"
	if _, err := setVoiceMode(t, e, map[string]any{"listen": "off"}); err != nil {
		t.Fatal(err)
	}
	if port.gotListen != "off" || port.gotSpeak != "" {
		t.Fatalf("listen= alone sent (%q, %q), want (off, \"\") — the half not named is the door's to keep", port.gotListen, port.gotSpeak)
	}
	if port.listen != "off" || port.speak != "auto" {
		t.Fatalf("the pair in force after listen= alone is (%s, %s), want (off, auto) — unchanged", port.listen, port.speak)
	}

	// A PAIR NO NAME COVERS IS LEGITIMATE: record the room and answer
	// aloud is a thing an operator may want, and the halves reach the
	// host exactly as sent.
	if _, err := setVoiceMode(t, e, map[string]any{"listen": "meeting", "speak": "on"}); err != nil {
		t.Fatalf("a pair with no name must not be refused: %v", err)
	}
	if port.gotListen != "meeting" || port.gotSpeak != "on" {
		t.Fatalf("the unnamed pair arrived as (%s, %s)", port.gotListen, port.gotSpeak)
	}
}

// Every refusal is typed by its own words and names the remedy, and none
// of them reaches the host.
func TestVoiceModeRefusalsNameTheRemedy(t *testing.T) {
	// No port at all: nothing here hears or speaks.
	bare, _, _, _, _ := setupEngine(t)
	if _, err := setVoiceMode(t, bare, map[string]any{"mode": "meeting"}); err == nil || !strings.Contains(err.Error(), "this host has no voice") {
		t.Fatalf("an unwired host must refuse honestly: %v", err)
	}

	for _, tc := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"nothing at all", map[string]any{}, "in your working state"},
		{"an empty name", map[string]any{"mode": "  "}, "in your working state"},
		{"a name and a half", map[string]any{"mode": "meeting", "speak": "on"}, "one or the other"},
		{"an unknown name", map[string]any{"mode": "earmuffs"}, "interactive, earbuds, meeting, off"},
		{"an unknown listen", map[string]any{"listen": "eavesdrop"}, "off, interactive, meeting"},
		{"an unknown speak", map[string]any{"speak": "loud"}, "on, off"},
		{"a listen that is not a string", map[string]any{"listen": 3}, "must be a string"},
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

// The surface the model is offered carries the action and its three
// arguments — an action that is routed and not advertised is a
// capability the identity cannot discover (the scattered-surface
// disease this registry exists to prevent).
func TestTheWorkFacadeAdvertisesTheVoiceMode(t *testing.T) {
	work := lookupVerb("work")
	if work == nil {
		t.Fatal("work is not in the registry")
	}
	props, _ := work.Params["properties"].(map[string]any)
	action, _ := props["action"].(map[string]any)
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
		p, ok := props[param].(map[string]any)
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
