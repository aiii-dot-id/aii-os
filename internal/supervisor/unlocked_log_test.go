package supervisor

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// lockProbe sits in front of the test's capture and, before each line
// goes through, takes and releases the supervisor's lock. A line written
// while s.mu is held can never get it, because its writer holds the lock
// and is waiting on this write, so after a bound the line is recorded as
// written under the lock and let through.
type lockProbe struct {
	slog.Handler
	s atomic.Pointer[Supervisor]

	mu    sync.Mutex
	lines []probedLine
}

type probedLine struct {
	level  slog.Level
	msg    string
	locked bool
}

// probeLock puts a probe over a fresh capture for one test. Lines are
// probed from the moment the test names the supervisor with p.s.Store.
func probeLock(t *testing.T) (*lockProbe, *logsink.Capture) {
	t.Helper()
	capture := logsink.CaptureForTest(t)
	prev := slog.Default()
	p := &lockProbe{Handler: prev.Handler()}
	slog.SetDefault(slog.New(p))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return p, capture
}

func (p *lockProbe) Handle(ctx context.Context, r slog.Record) error {
	locked := false
	if s := p.s.Load(); s != nil {
		taken := make(chan struct{})
		go func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			close(taken)
		}()
		select {
		case <-taken:
		case <-time.After(time.Second):
			locked = true
		}
	}
	p.mu.Lock()
	p.lines = append(p.lines, probedLine{level: r.Level, msg: r.Message, locked: locked})
	p.mu.Unlock()
	return p.Handler.Handle(ctx, r)
}

// levelOf fails the test for any line written under the lock, and
// returns the level of the one line carrying want.
func (p *lockProbe) levelOf(t *testing.T, want string) slog.Level {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	var found []probedLine
	for _, l := range p.lines {
		if l.locked {
			t.Errorf("written under the supervisor lock: %s", l.msg)
		}
		if strings.Contains(l.msg, want) {
			found = append(found, l)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want one line carrying %q, got %d", want, len(found))
	}
	return found[0].level
}

// Each of the three deactivations, driven to its own site: the ceiling at
// the reap, a refused respawn, and the ceiling at a respawn that failed.
func TestDeactivationIsLoggedOutsideTheLock(t *testing.T) {
	for _, tc := range []struct {
		name string
		// arm runs with the child up and the probe live; it makes the
		// next crash end in the case's deactivation.
		arm func(t *testing.T, s *Supervisor)
		// phase is the exit the ceiling counted last; empty for a refusal.
		phase string
	}{
		{"ceiling at the reap", func(t *testing.T, s *Supervisor) {
			crashAndRevive(t, s)
		}, "run"},
		{"refused respawn", func(t *testing.T, s *Supervisor) {
			s.mu.Lock()
			s.spec.VerifyArtifact = func() error { return errors.New("digest mismatch") }
			s.mu.Unlock()
		}, ""},
		{"ceiling at a failed respawn", func(t *testing.T, s *Supervisor) {
			s.mu.Lock()
			s.spec.Argv = []string{fakechildBin, "failstart"}
			s.mu.Unlock()
		}, "start"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, capture := probeLock(t)
			spec := childSpec("crash-after-respond")
			spec.Backoff.MaxRestarts = 1
			s := mustStart(t, spec, nil)
			p.s.Store(s)

			tc.arm(t, s)
			_, _ = s.Invoke(context.Background(), []byte(testReq)) // answers, then crashes
			waitFor(t, "the deactivation line", func() bool {
				return strings.Contains(capture.String(), "plugins.refusal: plugin org.example.crash-after-respond: DEACTIVATED: ")
			})
			if lv := p.levelOf(t, "DEACTIVATED"); lv != slog.LevelWarn {
				t.Fatalf("a deactivation is a warning, got %v", lv)
			}
			// The case reached the site it names.
			_, reason := s.State()
			var refused *SpawnRefusedError
			var ceiling *RestartCeilingError
			var exited *ChildExitError
			if tc.phase == "" && !errors.As(reason, &refused) {
				t.Fatalf("want a refused respawn, got %v", reason)
			}
			if tc.phase != "" && (!errors.As(reason, &ceiling) || !errors.As(ceiling.Last, &exited) || exited.Phase != tc.phase) {
				t.Fatalf("want the ceiling after a %s exit, got %v", tc.phase, reason)
			}
		})
	}
}

// crashAndRevive spends the one restart the ceiling allows.
func crashAndRevive(t *testing.T, s *Supervisor) {
	t.Helper()
	pid := s.Pid()
	if _, err := s.Invoke(context.Background(), []byte(testReq)); err != nil {
		t.Fatalf("the child answers before it crashes: %v", err)
	}
	waitFor(t, "the revived child", func() bool {
		st, _ := s.State()
		return st == StateRunning && s.Pid() != 0 && s.Pid() != pid
	})
}
