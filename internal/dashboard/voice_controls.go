package dashboard

import (
	"context"
	"errors"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

var ErrNoVoiceHolder = errors.New("no page holds that voice session")

func (s *Server) VoiceControlsFailed() uint64 { return s.voiceControlsFailed.Load() }

func (s *Server) VoiceControlsUnheld() uint64 { return s.voiceControlsUnheld.Load() }

type voiceControls struct {
	mu    sync.Mutex
	queue []VoiceEvent
	ready chan struct{}
}

func newVoiceControls() *voiceControls { return &voiceControls{ready: make(chan struct{}, 1)} }

func (q *voiceControls) push(ev VoiceEvent) {
	q.mu.Lock()
	q.queue = append(q.queue, ev)
	q.mu.Unlock()
	select {
	case q.ready <- struct{}{}:
	default:
	}
}

func (q *voiceControls) take() []VoiceEvent {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := q.queue
	q.queue = nil
	return out
}

func (s *Server) DeliverVoiceControl(ev VoiceEvent) error {
	s.wsMu.Lock()
	clients := make([]*wsClient, 0, len(s.wsConns))
	for _, cl := range s.wsConns {
		clients = append(clients, cl)
	}
	s.wsMu.Unlock()
	for _, cl := range clients {
		cl.vmu.Lock()
		cv := cl.voice
		cl.vmu.Unlock()
		if cv != nil && cv.controls != nil && cv.session != nil && cv.session.ID() == ev.SessionID && !voiceSessionOver(cv.session) {
			cv.controls.push(ev)
			return nil
		}
	}
	s.voiceControlsUnheld.Add(1)
	return ErrNoVoiceHolder
}

func (s *Server) sendVoiceControls(conn carrier, cl *wsClient, cv *connVoice) {
	for _, ev := range cv.controls.take() {
		if err := s.sendMsg(context.Background(), conn, ServerMessage{Type: "voice_event", VoiceEvent: &ev}); err != nil {
			s.voiceControlsFailed.Add(1)
			logsink.Error("voice.error", "a playback control (%s) for session %s did not reach the page at %s, which holds its audio: %v",
				ev.Type, ev.SessionID, cl.addr, err)
		}
	}
}
