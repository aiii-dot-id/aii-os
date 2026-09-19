package supervisor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
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
func TestAStartNobodyIsWaitingForEndsWhenItsContextDoes(t *testing.T) {
	spec := Spec{
		PluginID: "com.example.slow-start", Argv: []string{fakechildBin, "session"},
		SessionMode: true,
		// .
		// .
		ReadyMark: "a-mark-this-child-never-prints", ReadyTimeout: 10 * time.Minute,
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(150 * time.Millisecond); cancel() }()

	started := time.Now()
	sup, err := StartContext(ctx, spec, nil)
	took := time.Since(started)
	if sup != nil {
		defer sup.Close()
	}
	if took > 30*time.Second {
		t.Fatalf("the start served the whole allowance anyway: %s", took)
	}
	var cancelled *StartCancelledError
	if !errors.As(err, &cancelled) {
		t.Fatalf("a start nobody waits for must refuse as cancelled, got: %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("the cancellation must be visible through the error: %v", err)
	}
	if cancelled.PluginID != spec.PluginID {
		t.Fatalf("the refusal names the plugin: %+v", cancelled)
	}
	// .
	// .
	var exit *ChildExitError
	if errors.As(err, &exit) {
		t.Fatalf("a cancellation was reported as the child's exit: %v", exit)
	}
	if sup != nil {
		t.Fatal("a cancelled start must hand back no running supervisor")
	}
}

// .
func TestAStartUnderAFinishedContextSpawnsNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sup, err := StartContext(ctx, Spec{PluginID: "com.example.late", Argv: []string{fakechildBin, "session"},
		SessionMode: true, ReadyMark: "never", ReadyTimeout: time.Minute}, nil)
	if sup != nil {
		sup.Close()
	}
	if err == nil {
		t.Fatal("a start under a finished context succeeded")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("and it must say why: %v", err)
	}
}

// .
// .
func TestStartWithoutAContextStillHonoursItsAllowance(t *testing.T) {
	started := time.Now()
	sup, err := Start(Spec{PluginID: "com.example.plain", Argv: []string{fakechildBin, "session"},
		SessionMode: true, ReadyMark: "never", ReadyTimeout: 40 * time.Millisecond}, nil)
	if sup != nil {
		defer sup.Close()
	}
	var exit *ChildExitError
	if !errors.As(err, &exit) || exit.Phase != "start" {
		t.Fatalf("the allowance must still refuse an unready child: %v", err)
	}
	if time.Since(started) > 30*time.Second {
		t.Fatal("the allowance was not enforced")
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestAStartContextThatEndsAfterReadinessKillsNothingAndRestartsStillWork(t *testing.T) {
	_, lg := newCapture()
	ctx, cancel := context.WithCancel(context.Background())
	s, err := StartContext(ctx, childSpec("crash-after-respond", lg), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cancel()
	time.Sleep(200 * time.Millisecond)
	if state, why := s.State(); state != StateRunning {
		t.Fatalf("the child did not outlive the attempt that started it: %v (%v)", state, why)
	}
	resp, err := s.Invoke(context.Background(), []byte(testReq))
	if err != nil || !strings.Contains(string(resp), "answered") {
		t.Fatalf("it must still answer: %v %s", err, resp)
	}
	// .
	// .
	waitFor(t, "a restart under a finished start context", func() bool {
		got, ierr := s.Invoke(context.Background(), []byte(testReq))
		return ierr == nil && strings.Contains(string(got), "answered") && s.Restarts() >= 1
	})
}
