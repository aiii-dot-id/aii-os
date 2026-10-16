package app

import "github.com/aiii-dot-id/aii-os/internal/dashboard"

func (a *App) wireVoiceHooks(h *dashboard.WSHandler) {

	h.HearUtterance = a.HearUtterance
	h.VoiceConfigured = a.VoiceConfigured
	h.VoiceStatus = a.VoiceStatus
	h.VoiceMode = a.VoiceMode
	h.AudioPlane = a.AudioPlane
	h.VoiceEngine = a.VoiceEngine
	h.VoiceSessionOpen = a.OpenVoiceSession
	h.VoiceSpeaker = a.voiceSpeakerCarried
	h.SpeakerPolicy = a.speakerPolicyState
	h.SpeakMint = a.speakMint
	h.SpeakPlay = a.speakPlay
	h.SpeakAhead = a.speakAhead
	h.ReplyVoice = a.replyVoice
	h.SetSpeechService = a.setSpeechService
	h.SpeechLists = a.speechLists
}
