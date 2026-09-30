package app

import (
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/google/uuid"
)

func (s *pluginSubscriber) generationLocked(g uint64) bool {
	if g == 0 || g < s.generation {
		return false
	}
	if g != s.generation {
		s.generation = g
		s.stream = uuid.NewString()
		s.emitted = 0
		s.settled = 0
		s.loss = 0
		s.exhausted = false
	}
	if s.stream == "" {
		s.stream = uuid.NewString()
	}
	return true
}
func (s *pluginSubscriber) loseLocked() {
	if s.loss == pluginhost.MaxCalibrationCounter {
		s.exhausted = true
	} else {
		s.loss++
	}
}
func (s *pluginSubscriber) Snapshot(g uint64, session pluginhost.ActingSession) map[string]interface{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.generationLocked(g)
	out := map[string]interface{}{"version": 1, "available": false}
	if session.ID != "" {
		out["session_id"] = session.ID
	}
	reason := session.Reason
	if reason == "" && session.ID == "" {
		reason = "no_session"
	}
	if reason == "" {
		switch {
		case s.stopped:
			reason = "not_ready"
		case !current:
			reason = "generation_changed"
		case s.exhausted:
			reason = "counter_exhausted"
		}
	}
	if reason != "" {
		out["reason"] = reason
		return out
	}
	out["available"] = true
	out["stream_id"] = s.stream
	out["emitted"] = s.emitted
	out["settled"] = s.settled
	out["loss_count"] = s.loss
	return out
}
func (s *pluginSubscriber) Finish(g, seq uint64, accepted bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped || g != s.generation {
		return
	}
	if seq <= s.settled || seq > s.emitted {
		s.exhausted = true
		return
	}
	s.settled = seq
	if !accepted {
		s.loseLocked()
	}
}
