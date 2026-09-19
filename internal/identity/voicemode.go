package identity

import (
	"context"
	"fmt"
	"slices"
	"strings"
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
// .
// .
// .

// .
type VoicePort interface {
	// .
	Mode() (listen, speak string, set bool)
	// .
	// .
	// .
	// .
	SetMode(listen, speak string) (string, error)
}

// .
// .
func (e *Engine) SetVoice(v VoicePort) { e.voice = v }

// .
// .
var (
	voiceListenValues = []string{"off", "interactive", "meeting"}
	voiceSpeakValues  = []string{"on", "off"}
)

// .
// .
// .
// .
var voiceModeSugar = []struct{ name, listen, speak string }{
	{"interactive", "interactive", "on"},
	{"earbuds", "off", "on"},
	{"meeting", "meeting", "off"},
	{"off", "off", "off"},
}

// .
func voiceModePair(name string) (listen, speak string, ok bool) {
	for _, m := range voiceModeSugar {
		if m.name == name {
			return m.listen, m.speak, true
		}
	}
	return "", "", false
}

func voiceModeNameValues() []string {
	out := make([]string, 0, len(voiceModeSugar))
	for _, m := range voiceModeSugar {
		out = append(out, m.name)
	}
	return out
}

// .
// .
// .
// .
func voiceArg(args map[string]interface{}, key string) (string, bool, error) {
	raw, present := args[key]
	if !present {
		return "", false, nil
	}
	s, isStr := raw.(string)
	if !isStr {
		return "", false, fmt.Errorf("%s must be a string, got %T", key, raw)
	}
	s = strings.ToLower(strings.TrimSpace(s))
	return s, s != "", nil
}

// .
// .
// .
// .
// .
func (e *Engine) verbVoiceMode(_ context.Context, args map[string]interface{}) (string, error) {
	if e.voice == nil {
		return "", fmt.Errorf("this host has no voice: nothing here hears or speaks, so there is no mode to change — only your operator can give this host one")
	}
	names := strings.Join(voiceModeNameValues(), "|")
	listenSet := strings.Join(voiceListenValues, "|")
	speakSet := strings.Join(voiceSpeakValues, "|")

	name, hasName, err := voiceArg(args, "mode")
	if err != nil {
		return "", err
	}
	listen, hasListen, err := voiceArg(args, "listen")
	if err != nil {
		return "", err
	}
	speak, hasSpeak, err := voiceArg(args, "speak")
	if err != nil {
		return "", err
	}

	switch {
	case hasName && (hasListen || hasSpeak):
		return "", fmt.Errorf("one or the other: mode= names a whole pair, listen= and speak= set the halves — send mode=%s alone, or listen=%s and/or speak=%s alone", names, listenSet, speakSet)
	case !hasName && !hasListen && !hasSpeak:
		return "", fmt.Errorf("the mode in force is in your working state; give mode=%s, or listen=%s and/or speak=%s to change it", names, listenSet, speakSet)
	case hasName:
		l, s, ok := voiceModePair(name)
		if !ok {
			return "", fmt.Errorf("mode %q is not one of %s", name, strings.Join(voiceModeNameValues(), ", "))
		}
		listen, speak = l, s
	default:
		if hasListen && !slices.Contains(voiceListenValues, listen) {
			return "", fmt.Errorf("listen %q is not one of %s", listen, strings.Join(voiceListenValues, ", "))
		}
		if hasSpeak && !slices.Contains(voiceSpeakValues, speak) {
			return "", fmt.Errorf("speak %q is not one of %s", speak, strings.Join(voiceSpeakValues, ", "))
		}
		// .
		// .
		// .
		// .
		// .
		// .
	}
	return e.voice.SetMode(listen, speak)
}
