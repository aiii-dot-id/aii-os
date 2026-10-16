package supervisor

import (
	"context"
	"io"
	"time"
)

type SessionConnection struct {
	Frames   <-chan []byte
	Stdin    io.Writer
	AudioIn  io.WriteCloser
	AudioOut io.ReadCloser
	Exited   <-chan struct{}
}

func (s *Supervisor) sessionConnectionLocked() (SessionConnection, bool) {
	c := s.child
	if s.state != StateRunning || c == nil || c.stdoutEOF.Load() {
		return SessionConnection{}, false
	}
	select {
	case <-c.exited:
		return SessionConnection{}, false
	default:
	}
	conn := SessionConnection{Frames: c.frames, Stdin: c.stdin, Exited: c.exited}
	if c.audioIn != nil {
		conn.AudioIn, conn.AudioOut = c.audioIn, c.audioOut
	}
	return conn, true
}

func (s *Supervisor) SessionConnection() (SessionConnection, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionConnectionLocked()
}

func (s *Supervisor) NextSession(ctx context.Context, previous <-chan struct{}) (SessionConnection, error) {
	for {
		if err := ctx.Err(); err != nil {
			return SessionConnection{}, err
		}
		s.mu.Lock()
		if conn, ok := s.sessionConnectionLocked(); ok && conn.Exited != previous {
			s.mu.Unlock()
			return conn, nil
		}
		if s.state == StateStopped {
			err := &UnavailableError{PluginID: s.spec.PluginID, State: s.state, Reason: s.stopReason}
			s.mu.Unlock()
			return SessionConnection{}, err
		}
		if s.changed == nil {
			s.changed = make(chan struct{})
		}
		changed := s.changed
		s.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return SessionConnection{}, ctx.Err()
		}
	}
}

func (s *Supervisor) publishStateLocked() {
	if s.changed != nil {
		close(s.changed)
	}
	s.changed = make(chan struct{})
	if s.terminated == nil {
		s.terminated = make(chan struct{})
	}
	if s.state == StateStopped {
		select {
		case <-s.terminated:
		default:
			close(s.terminated)
		}
	}
}

func (s *Supervisor) Terminated() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminated == nil {
		s.publishStateLocked()
	}
	return s.terminated
}

func (s *Supervisor) Failure() ([]time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Time(nil), s.restartTimes...), s.stopReason
}

func (s *Supervisor) countRestartLocked(now time.Time, last error) bool {
	s.restartTimes = restartsWithin(s.restartTimes, now.Add(-s.spec.Backoff.window()))
	if len(s.restartTimes) >= s.spec.Backoff.maxRestarts() {
		s.state = StateStopped
		s.stopReason = &RestartCeilingError{PluginID: s.spec.PluginID, Restarts: len(s.restartTimes), Last: last, RetryAt: s.restartTimes[0].Add(s.spec.Backoff.window())}
		s.publishStateLocked()
		return false
	}
	s.restarts++
	s.restartTimes = append(s.restartTimes, now)
	return true
}
