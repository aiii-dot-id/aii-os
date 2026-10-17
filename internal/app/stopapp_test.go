package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

const teardownBound = 2 * time.Minute

// stopApp ends a test's App and fails the test when its teardown fails: a
// store or ledger close that did not complete, a plugin that would not
// deactivate, a teardown still running at the bound. App.Stop logs that
// and returns nothing, so a teardown regression passed every test that
// tore an App down. A test that breaks teardown on purpose calls
// Shutdown itself and asserts what it expects.
func stopApp(t testing.TB, a *App) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), teardownBound)
	defer cancel()
	began := time.Now()
	if err := a.Shutdown(ctx); err != nil {
		t.Errorf("teardown: %v", err)
	}
	// A teardown that is slow and still ends passes; it is said, so a
	// regression that slows every teardown is seen before it is a hang.
	if took := time.Since(began); took > 10*time.Second {
		t.Logf("the teardown took %v", took)
	}
}

type asideStop struct {
	done chan struct{}
	err  error
}

func stopAppAside(a *App) *asideStop {
	s := &asideStop{done: make(chan struct{})}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), teardownBound)
		defer cancel()
		s.err = a.Shutdown(ctx)
		close(s.done)
	}()
	return s
}

// check fails t when the teardown ended in error; it is called from the
// test's own goroutine, once done has closed.
func (s *asideStop) check(t testing.TB) {
	t.Helper()
	<-s.done
	if s.err != nil {
		t.Errorf("teardown: %v", s.err)
	}
}

// THE HELPER SEES A TEARDOWN THAT FAILED. A ledger closed under its App
// fails the App's final close; App.Stop logs that and returns nothing,
// stopApp fails the test that owns the App.
func TestStopAppReportsATeardownThatFailed(t *testing.T) {
	dir := t.TempDir()
	lg, err := ledger.New(filepath.Join(dir, "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}
	a := New(&Config{SourcePath: filepath.Join(dir, "config.json")})
	a.ledger = lg
	rec := &teardownRecorder{TB: t}
	stopApp(rec, a)
	if len(rec.errs) != 1 || !strings.Contains(rec.errs[0], "teardown:") {
		t.Fatalf("a failed teardown passed: %q", rec.errs)
	}
}

// teardownRecorder keeps what stopApp reports instead of failing the test
// that runs it.
type teardownRecorder struct {
	testing.TB
	errs []string
}

func (r *teardownRecorder) Helper() {}
func (r *teardownRecorder) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}
