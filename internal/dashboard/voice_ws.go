package dashboard

import (
	"context"
	"encoding/binary"
	"log"

	"github.com/coder/websocket"
)

// voice_ws.go — one spoken utterance, arriving.
//
// THE BROWSER DECIDES WHERE AN UTTERANCE ENDS, and that decision is the
// entire protocol. It is holding the microphone, so it knows when the
// operator stopped talking; it sends the whole thing as ONE binary
// frame. There is no session to open, no chunk to assemble, no stream to
// keep alive and nothing to clean up if the socket dies mid-word — a
// half-sent utterance is simply a frame that never arrived.
//
// A superseded design carried this audio to a contained plugin over a
// second transport, with attachments, purposes, admission closures and
// an eviction policy. None of it worked, because a contained plugin
// cannot reach a socket. This file is what replaced all of it.

// voiceHeaderBytes is the fixed preamble on a voice frame:
//
//	[0]     version (1)
//	[1]     channels
//	[2:4]   reserved, zero
//	[4:8]   sample rate, uint32 little-endian
//
// THE BROWSER SENDS ITS NATIVE RATE AND DOES NOT RESAMPLE. An
// AudioContext gives you whatever the device decided — 44100 and 48000
// are both common and neither is asked for — so resampling in the page
// would put the most delicate arithmetic in the codebase in the place
// with the least ability to prove it right. A wrong rate is then a
// silently sped-up transcript. Sending the true rate makes the engine
// resample instead, which every one of them already does correctly.
const voiceHeaderBytes = 8

// voiceFrameVersion is the only version this host understands. A frame
// carrying anything else is refused rather than guessed at.
const voiceFrameVersion = 1

// maxVoiceFrameBytes bounds one utterance.
//
// GENEROUS, AND STILL A BOUND. At 48 kHz mono s16 this is about two
// minutes of continuous speech, which is longer than anyone talks in one
// breath and short enough that a page sending garbage cannot make the
// identity's process hold it. The websocket library's own read limit is
// raised to match at accept time; without that, this ceiling would never
// be reached because the connection would die first.
const maxVoiceFrameBytes = 12 << 20

// maxTextFrameBytes bounds one JSON control message. The connection's
// read limit had to rise to carry voice (above); this puts the text
// plane back on a bound of its own. Generous for chat — an operator
// pasting a whole document stays under it — and refused with a reason
// rather than a dropped connection when exceeded.
const maxTextFrameBytes = 256 << 10

// handleVoiceFrame turns one binary frame into words the identity heard.
func (s *Server) handleVoiceFrame(ctx context.Context, conn *websocket.Conn, data []byte) {
	h := s.currentHandler()
	if h.HearUtterance == nil {
		s.sendError(ctx, conn, "this identity has no speech endpoint configured — set speech.stt in Settings")
		return
	}
	if len(data) <= voiceHeaderBytes {
		s.sendError(ctx, conn, "voice frame carried no audio")
		return
	}
	if data[0] != voiceFrameVersion {
		s.sendError(ctx, conn, "voice frame version is not one this identity understands")
		return
	}
	// RESERVED MEANS ZERO, AND IS CHECKED SO THAT IT CAN LATER MEAN
	// SOMETHING. A future page that puts a flag here needs this host to
	// refuse it rather than ignore it — a field silently discarded for
	// one release is a field that can never be added, because the old
	// hosts in the field will accept it and do the wrong thing.
	if data[2] != 0 || data[3] != 0 {
		s.sendError(ctx, conn, "voice frame sets reserved header bytes this identity does not understand")
		return
	}
	channels := int(data[1])
	sampleRate := int(binary.LittleEndian.Uint32(data[4:8]))
	if channels <= 0 || channels > 2 || sampleRate < 8000 || sampleRate > 192000 {
		s.sendError(ctx, conn, "voice frame declares a format that is not audio")
		return
	}
	// A WHOLE NUMBER OF FRAMES, OR IT IS NOT THE AUDIO IT CLAIMS TO BE.
	// A trailing half-sample means the page truncated, or the channel
	// count is wrong; either way every sample after the error is
	// misaligned, and the engine would transcribe the noise rather than
	// report it.
	if (len(data)-voiceHeaderBytes)%(2*channels) != 0 {
		s.sendError(ctx, conn, "voice frame is not a whole number of samples for the channel count it declares")
		return
	}
	// ONE AT A TIME, AND THE OPERATOR IS TOLD. Taken AFTER validation so
	// a malformed frame cannot consume the slot, and BEFORE the goroutine
	// so the decision is made on the read loop, in frame order, where it
	// cannot race with the next frame arriving.
	if !s.admitVoice() {
		s.sendError(ctx, conn, "still hearing the last thing you said — wait for it to land before speaking again")
		return
	}

	// COPY, BECAUSE THE LIBRARY'S BUFFER IS NOT OURS AFTER THIS RETURNS
	// and the goroutine below outlives the return by design.
	pcm := make([]byte, len(data)-voiceHeaderBytes)
	copy(pcm, data[voiceHeaderBytes:])

	// OFF THE READ LOOP, ALWAYS. Transcription is a network round trip
	// and what follows it can be a whole LLM turn. Doing either here
	// stops this socket being read — and the next thing on it might be
	// the operator saying stop. This is the same rule the chat path
	// follows for the same reason, and the same rule an earlier voice
	// design got wrong by calling the sink synchronously from its
	// reader.
	//
	// LIFETIME IS THE SERVER'S, NOT THIS SOCKET'S. A reload drops the
	// socket; the words were still said, and the identity's answer is
	// broadcast to whatever is connected when it lands.
	turnCtx := s.serverTurnCtx()
	go func() {
		defer s.releaseVoice()
		if err := h.HearUtterance(turnCtx, pcm, sampleRate, channels); err != nil {
			// THE OPERATOR HEARS ABOUT IT. A microphone that silently
			// does nothing is indistinguishable from a broken one, and
			// the most likely causes here — a wrong model name, an
			// endpoint that is not running — are things only they can
			// fix, and only if told.
			log.Printf("VOICE: could not hear an utterance (%d bytes, %d Hz, %d ch): %v",
				len(pcm), sampleRate, channels, err)
			s.sendError(ctx, conn, "could not transcribe what you said: "+err.Error())
		}
	}()
}

// admitVoice takes the single in-flight slot, or reports that it is
// taken. See Server.voiceBusy for why there is only one.
func (s *Server) admitVoice() bool {
	s.voiceMu.Lock()
	defer s.voiceMu.Unlock()
	if s.voiceBusy {
		return false
	}
	s.voiceBusy = true
	return true
}

// releaseVoice returns the slot. It runs in the worker's defer, so a
// transcription that panics or errors still frees the microphone — a
// slot leaked here is an identity that has gone permanently deaf, and
// the operator would have no way to tell that from a broken endpoint.
func (s *Server) releaseVoice() {
	s.voiceMu.Lock()
	s.voiceBusy = false
	s.voiceMu.Unlock()
}
