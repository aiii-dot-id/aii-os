package app

import (
	"fmt"
	"log"
	"strings"

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
// .
// .
// .

const (
	listenOff         = "off"
	listenInteractive = "interactive"
	listenMeeting     = "meeting"

	speakOn  = "on"
	speakOff = "off"
	// .
	// .
	// .
	speakAuto = "auto"
)

const (
	listenValues = "off, interactive, meeting"
	speakValues  = "on, off"
)

func validListen(s string) bool {
	return s == listenOff || s == listenInteractive || s == listenMeeting
}

func validSpeak(s string) bool { return s == speakOn || s == speakOff }

// .
// .
func validateVoiceMode(m VoiceModeConfig) error {
	if m.Listen != "" && !validListen(m.Listen) {
		return fmt.Errorf("speech.mode.listen %q is not one of %s (or empty for the default)", m.Listen, listenValues)
	}
	if m.Speak != "" && !validSpeak(m.Speak) {
		return fmt.Errorf("speech.mode.speak %q is not one of %s (or empty for the default)", m.Speak, speakValues)
	}
	return nil
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
func voiceModeFromChange(v interface{}, current VoiceModeConfig) (VoiceModeConfig, error) {
	obj, ok := v.(map[string]interface{})
	if !ok {
		return VoiceModeConfig{}, fmt.Errorf("want an object with listen and/or speak")
	}
	out := VoiceModeConfig{Listen: current.Listen, Speak: current.Speak}
	for k, raw := range obj {
		s, ok := raw.(string)
		if !ok {
			return VoiceModeConfig{}, fmt.Errorf("%s: want a string", k)
		}
		s = strings.ToLower(strings.TrimSpace(s))
		switch k {
		case "listen":
			out.Listen = s
		case "speak":
			out.Speak = s
		default:
			return VoiceModeConfig{}, fmt.Errorf("unknown field %q (listen and speak are the fields)", k)
		}
	}
	if err := validateVoiceMode(out); err != nil {
		return VoiceModeConfig{}, err
	}
	return out, nil
}

// .
// .
func resolveVoiceMode(m VoiceModeConfig) (listen, speak string, set bool) {
	listen, speak = m.Listen, m.Speak
	if listen == "" {
		listen = listenInteractive
	}
	if speak == "" {
		speak = speakAuto
	}
	return listen, speak, m.Listen != "" || m.Speak != ""
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
func (a *App) publishVoiceMode(m VoiceModeConfig) {
	copy := m
	a.voiceModePub.Store(&copy)
}

func (a *App) publishedVoiceMode() VoiceModeConfig {
	if p := a.voiceModePub.Load(); p != nil {
		return *p
	}
	// .
	// .
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	if a.cfg == nil {
		return VoiceModeConfig{}
	}
	return a.cfg.Speech.Mode
}

// .
func (a *App) voiceMode() (listen, speak string, set bool) {
	return resolveVoiceMode(a.publishedVoiceMode())
}

// .
// .
// .
func (a *App) VoiceMode() (listen, speak string, revision uint64) {
	m := a.publishedVoiceMode()
	listen, speak, _ = resolveVoiceMode(m)
	return listen, speak, m.Revision
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
func (a *App) voiceModeCommitted(prev, next VoiceModeConfig) {
	_, was, _ := resolveVoiceMode(prev)
	_, now, _ := resolveVoiceMode(next)
	if now != speakOff {
		return
	}
	// .
	for {
		cur := a.voiceSpeakOffRev.Load()
		if next.Revision <= cur || a.voiceSpeakOffRev.CompareAndSwap(cur, next.Revision) {
			break
		}
	}
	if was == speakOff {
		return
	}
	const why = "the mode's speak turned off"
	// .
	// .
	// .
	// .
	// .
	// .
	type target struct {
		h  *voiceHandle
		id string
	}
	var targets []target
	a.voiceSessions.Range(func(_, v any) bool {
		h := v.(*voiceHandle)
		if cur := h.loadInflight(); cur.id != "" && cur.rev < next.Revision && h.inflight.CompareAndSwap(cur, inflightSynthesis{}) {
			targets = append(targets, target{h, cur.id})
		}
		a.hushFallback(h, why)
		return true
	})
	for _, t := range targets {
		if err := t.h.fenceBounded(t.id, why); err != nil {
			log.Printf("VOICE: speak turned off; the engine did NOT fence synthesis %s on %s (%v) — the page's own silence is the remaining guard", t.id, t.h.id, err)
		} else {
			log.Printf("VOICE: speak turned off; synthesis %s on %s was fenced", t.id, t.h.id)
			t.h.replyOutcome.Store(replyNotSpokenOutcome)
		}
	}
	a.spokenMu.Lock()
	var ids []string
	for id, r := range a.spoken {
		if !r.say.Sample {
			ids = append(ids, id)
		}
	}
	a.spokenMu.Unlock()
	for _, id := range ids {
		a.hushSpoken(id)
	}
}

// .
// .
// .
func voiceModeName(listen, speak string) string {
	spoken := speak != speakOff
	switch {
	case listen == listenInteractive && spoken:
		return "interactive"
	case listen == listenOff && spoken:
		return "earbuds"
	case listen == listenMeeting && !spoken:
		return "meeting"
	case listen == listenOff && !spoken:
		return "off"
	case listen == listenMeeting:
		return "meeting, spoken replies"
	default:
		return "interactive, text replies"
	}
}

// .
// .
// .
// .
func voiceModeSentence(listen, speak string) string {
	var hears string
	switch listen {
	case listenInteractive:
		hears = "the operator can speak to you"
	case listenMeeting:
		hears = "you are hearing a room; what you hear is recorded and not addressed to you"
	default:
		hears = "the operator types"
	}
	var says string
	switch speak {
	case speakOff:
		says = "your replies are text"
	case speakOn:
		says = "your replies are spoken"
	default:
		says = "your replies are spoken by the configured voice, when there is one"
	}
	return fmt.Sprintf("%s (listen: %s, speak: %s): %s; %s", voiceModeName(listen, speak), listen, speak, hears, says)
}

// .
func (a *App) voiceModeState() *dashboard.VoiceModeState {
	m := a.configSnapshot().Speech.Mode
	listen, speak, set := resolveVoiceMode(m)
	return &dashboard.VoiceModeState{Listen: listen, Speak: speak, Revision: m.Revision, Name: voiceModeName(listen, speak), Set: set}
}
