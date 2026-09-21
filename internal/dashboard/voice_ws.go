package dashboard

import (
	"context"
	"encoding/binary"
	"github.com/aiii-dot-id/aii-os/internal/logsink"

	"github.com/coder/websocket"
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
const voiceHeaderBytes = 8

// .
// .
const voiceFrameVersion = 1

// .
// .
// .
// .
// .
// .
// .
// .
const maxVoiceFrameBytes = 12 << 20

// .
// .
// .
// .
// .
const maxTextFrameBytes = 256 << 10

// .
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
	if data[3] != 0 {
		s.sendError(ctx, conn, "voice frame sets a reserved header byte this identity does not understand")
		return
	}
	if data[2] > 1 {
		s.sendError(ctx, conn, "voice frame declares a mode this identity does not understand")
		return
	}
	answer := data[2] == 1
	channels := int(data[1])
	sampleRate := int(binary.LittleEndian.Uint32(data[4:8]))
	if channels <= 0 || channels > 2 || sampleRate < 8000 || sampleRate > 192000 {
		s.sendError(ctx, conn, "voice frame declares a format that is not audio")
		return
	}
	// .
	// .
	// .
	// .
	// .
	if (len(data)-voiceHeaderBytes)%(2*channels) != 0 {
		s.sendError(ctx, conn, "voice frame is not a whole number of samples for the channel count it declares")
		return
	}
	// .
	// .
	// .
	// .
	if !s.admitVoice() {
		s.sendError(ctx, conn, "still hearing the last thing you said — wait for it to land before speaking again")
		return
	}

	// .
	// .
	pcm := make([]byte, len(data)-voiceHeaderBytes)
	copy(pcm, data[voiceHeaderBytes:])

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
	turnCtx := s.serverTurnCtx()
	go func() {
		defer s.releaseVoice()
		if err := h.HearUtterance(turnCtx, pcm, sampleRate, channels, answer); err != nil {
			// .
			// .
			// .
			// .
			// .
			logsink.Warn("voice.error", "could not hear an utterance (%d bytes, %d Hz, %d ch): %v",
				len(pcm), sampleRate, channels, err)
			s.sendError(ctx, conn, "could not transcribe what you said: "+err.Error())
		}
	}()
}

// .
// .
func (s *Server) admitVoice() bool {
	s.voiceMu.Lock()
	defer s.voiceMu.Unlock()
	if s.voiceBusy {
		return false
	}
	s.voiceBusy = true
	return true
}

// .
// .
// .
// .
func (s *Server) releaseVoice() {
	s.voiceMu.Lock()
	s.voiceBusy = false
	s.voiceMu.Unlock()
}
