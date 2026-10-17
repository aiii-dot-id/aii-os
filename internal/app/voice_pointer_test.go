package app

import "github.com/aiii-dot-id/aii-os/internal/pluginhost"

func pluginWithTestVoice(ap *pluginhost.ActivePlugin, v *pluginhost.VoiceSession) *pluginhost.ActivePlugin {
	ap.Voice.Store(v)
	return ap
}
