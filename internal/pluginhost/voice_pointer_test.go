package pluginhost

func pluginWithTestVoice(ap *ActivePlugin, v *VoiceSession) *ActivePlugin {
	ap.Voice.Store(v)
	return ap
}
