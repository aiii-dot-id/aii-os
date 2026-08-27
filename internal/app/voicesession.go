package app

import "sync/atomic"

// voicesession.go — which of two things the operator opened.
//
// THIS FILE USED TO BE A TRANSPORT. It held a loopback audio stream, a
// bounded work queue, an ordered worker, a playback sink and a barge-in
// path, because a contained plugin was going to carry PCM in and out of
// it. That plane is gone (docs/VOICE_FIRST_PRINCIPLES.md): the speech
// model is an endpoint like every other model the identity uses, and
// audio crosses the dashboard socket that already spans the only link
// it needs to cross. What is left is the one thing that was never about
// transport.
//
// CONVERSATION WAKES; A MEETING DOES NOT. In a meeting, waking per
// utterance would make a room full of people a spend storm, so speech
// is recorded and read on the next real turn. But an operator who
// deliberately opened a conversation and then spoke into silence has a
// broken product, whatever the log says — the identity has to answer.
//
// THE MODE IS SESSION INTENT AND CHANGES NO AUTHORITY. What arrives is
// a PARTICIPANT turn either way, because the operator role means
// "arrived through an authenticated operator channel" and a microphone
// is not one at any confidence. It decides whether the identity answers
// now, not whose word this is.

// voiceMode is what the operator opened.
type voiceMode int32

const (
	// voiceMeeting records what it hears and reads it on the next turn.
	voiceMeeting voiceMode = iota
	// voiceConversation answers.
	voiceConversation
)

// voiceSession holds the mode and nothing else. It has no authority of
// its own, no lifecycle, and no registry: every decision it carries is
// one the operator already made.
type voiceSession struct{ mode atomic.Int32 }

// setMode chooses what speech does. Meeting is the zero value because it
// is the one that cannot surprise an operator with spend.
func (v *voiceSession) setMode(m voiceMode) { v.mode.Store(int32(m)) }

func (v *voiceSession) currentMode() voiceMode { return voiceMode(v.mode.Load()) }

// voiceSession returns the app's session, creating it on first use.
func (a *App) voiceSession() *voiceSession {
	a.voiceOnce.Do(func() { a.voice = &voiceSession{} })
	return a.voice
}
