package supervisor

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestNextSessionIsOneChildSnapshotAndTerminalWake(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		in1, out1, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer in1.Close()
		defer out1.Close()
		in2, out2, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer in2.Close()
		defer out2.Close()
		old := &child{frames: make(chan []byte), stdin: out1, audioIn: out1, audioOut: in1, exited: make(chan struct{})}
		s := &Supervisor{spec: Spec{PluginID: "p"}, state: StateRunning, child: old}
		first, ok := s.SessionConnection()
		if !ok {
			t.Fatal("no first child")
		}
		got := make(chan SessionConnection, 1)
		go func() {
			c, e := s.NextSession(ctx, first.Exited)
			if e == nil {
				got <- c
			}
		}()
		synctest.Wait()
		select {
		case <-got:
			t.Fatal("same child was handed out twice")
		default:
		}
		next := &child{frames: make(chan []byte), stdin: out2, audioIn: out2, audioOut: in2, exited: make(chan struct{})}
		s.mu.Lock()
		s.child = next
		s.publishStateLocked()
		s.mu.Unlock()
		synctest.Wait()
		var c SessionConnection
		select {
		case c = <-got:
		default:
			t.Fatal("readiness did not wake the session owner")
		}
		if c.Frames != next.frames || c.Stdin != out2 || c.AudioIn != out2 || c.AudioOut != in2 || c.Exited != next.exited {
			t.Fatal("mixed child snapshot")
		}
		ended := make(chan error, 1)
		go func() { _, e := s.NextSession(ctx, c.Exited); ended <- e }()
		synctest.Wait()
		why := errors.New("restart refused")
		s.mu.Lock()
		s.state = StateStopped
		s.stopReason = why
		s.publishStateLocked()
		s.mu.Unlock()
		synctest.Wait()
		select {
		case err := <-ended:
			if !errors.Is(err, why) {
				t.Fatal("terminal cause lost")
			}
		default:
			t.Fatal("terminal state did not wake the session owner")
		}
		select {
		case <-s.Terminated():
		default:
			t.Fatal("terminal signal lost")
		}
	})
}

func TestFailedRespawnUsesTheWindowNotLifetime(t *testing.T) {
	now := time.Now()
	s := &Supervisor{spec: Spec{PluginID: "p", Backoff: Backoff{MaxRestarts: 2, Window: time.Minute}}, state: StateStarting, restarts: 20, restartTimes: []time.Time{now.Add(-2 * time.Minute)}}
	s.mu.Lock()
	if !s.countRestartLocked(now, errors.New("failed respawn")) {
		t.Fatal("lifetime count incorrectly exhausted this window")
	}
	if !s.countRestartLocked(now, errors.New("second failure")) {
		t.Fatal("second window attempt refused")
	}
	if s.countRestartLocked(now, errors.New("third failure")) {
		t.Fatal("window ceiling ignored")
	}
	s.mu.Unlock()
	times, err := s.Failure()
	var ceiling *RestartCeilingError
	if !errors.As(err, &ceiling) || len(times) != 2 || ceiling.Restarts != 2 || !ceiling.RetryAfter().Equal(now.Add(time.Minute)) {
		t.Fatalf("window evidence: %v %v", err, times)
	}
}

func TestActualFailedRespawnDoesNotUseLifetimeCeiling(t *testing.T) {
	spec := childSpec("failstart")
	spec.Backoff = Backoff{MaxRestarts: 2, Window: time.Minute, Initial: time.Hour, Max: time.Hour}
	s := &Supervisor{spec: spec, state: StateRestarting, restarts: 20, restartTimes: []time.Time{time.Now().Add(-2 * time.Minute)}}
	t.Cleanup(func() { _ = s.Close() })
	s.restart(20)
	state, err := s.State()
	if state != StateRestarting || s.Restarts() != 21 {
		t.Fatalf("a failed respawn after the window expired was made terminal by lifetime history: %s %v", state, err)
	}
}

func TestRespawnRefusalPublishesTerminalCause(t *testing.T) {
	var refuse atomic.Bool
	why := errors.New("fixture artifact unavailable")
	spec := childSpec("respond")
	spec.VerifyArtifact = func() error {
		if refuse.Load() {
			return why
		}
		return nil
	}
	s := mustStart(t, spec, nil)
	refuse.Store(true)
	if killed, err := s.KillChild(); err != nil || !killed {
		t.Fatalf("no live child was killed: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	select {
	case <-s.Terminated():
	case <-ctx.Done():
		t.Fatal("terminal respawn refusal was not published")
	}
	times, err := s.Failure()
	var refusal *SpawnRefusedError
	if !errors.As(err, &refusal) || !errors.Is(err, why) || len(times) != 1 {
		t.Fatalf("terminal evidence lost: %v %v", err, times)
	}
}
