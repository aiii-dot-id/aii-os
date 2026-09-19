package app

import (
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/identity"
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
type voiceModeAdapter struct{ a *App }

var _ identity.VoicePort = voiceModeAdapter{}

func (v voiceModeAdapter) Mode() (listen, speak string, set bool) { return v.a.voiceMode() }

func (v voiceModeAdapter) SetMode(listen, speak string) (string, error) {
	if why, inSafe := v.a.SafeMode(); inSafe {
		return "", fmt.Errorf("the mode cannot change while this identity is in SAFE (%s): your operator clears SAFE, and the mode is yours again", why)
	}
	if listen == listenInteractive || listen == listenMeeting {
		switch st, why, _ := v.a.VoiceStatus(); st {
		case "setup":
			return "", fmt.Errorf("listen=%s needs a microphone and no microphone is offered: set a speech service or install a voice engine (Settings → Speech)", listen)
		case "unreachable":
			return "", fmt.Errorf("listen=%s needs a microphone and the one configured cannot be reached (%s): fix or replace the speech service (Settings → Speech)", listen, why)
		}
	}
	// .
	// .
	// .
	// .
	// .
	// .
	change := map[string]interface{}{}
	if listen != "" {
		change["listen"] = listen
	}
	if speak != "" && speak != speakAuto {
		change["speak"] = speak
	}
	if _, err := v.a.applyConfigChange(map[string]interface{}{"speech.mode": change}); err != nil {
		return "", err
	}
	// .
	// .
	// .
	pushVoiceStatus(v.a)
	v.a.pluginsChanged()
	nowListen, nowSpeak, _ := v.a.voiceMode()
	return voiceModeSentence(nowListen, nowSpeak), nil
}
