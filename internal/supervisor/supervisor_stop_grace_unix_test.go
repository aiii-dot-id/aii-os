//go:build !windows

package supervisor

import (
	"testing"
	"time"
)

// Unix only: the stop's two graces are the end of input and SIGTERM, and on
// Windows a stop collapses to the kill by design (TestCloseEscalatesToKill
// says the same), so there is no second grace there to give.

// A STOP GIVES THE CHILD THE GRACE IT WAS GIVEN. The stop was a fixed five
// seconds, three after the end of input and two after SIGTERM, whatever the
// child: an engine that flushes what it holds on the way out was killed
// mid-write on a slow machine. The spec's StopGrace (the operator's
// plugins.runtime.stop_ms) is split the same way. Longer: a child that never
// reads its input, and so never sees its end, is asked to stop with SIGTERM
// only after three fifths of ten seconds, not after three. Shorter: a child
// that ignores both is killed after its one second, not after five.
func TestAStopGivesTheChildTheGraceItWasGiven(t *testing.T) {
	t.Run("longer than the default", func(t *testing.T) {
		spec := childSpec("deaf") // never reads stdin; dies on SIGTERM
		spec.StopGrace = 10 * time.Second
		s := mustStart(t, spec, nil)
		began := time.Now()
		if err := s.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if took := time.Since(began); took < 6*time.Second-200*time.Millisecond || took > 10*time.Second {
			t.Fatalf("a child given 10s was asked to stop with SIGTERM after %s: want three fifths of its grace, 6s, not the default's %s", took, closeGraceEOF)
		}
	})
	t.Run("shorter than the default", func(t *testing.T) {
		spec := childSpec("ignore-term")
		spec.StopGrace = time.Second
		s := mustStart(t, spec, nil)
		began := time.Now()
		if err := s.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if took := time.Since(began); took < time.Second-100*time.Millisecond || took >= closeGraceEOF {
			t.Fatalf("a child given 1s was held %s before the kill: want its own second, not the default's %s", took, DefaultStopGrace)
		}
	})
}

func TestBehindAWallThatSIGTERMEndsTheWholeStopIsTheWaitForTheEndOfInput(t *testing.T) {
	for _, c := range []struct {
		name        string
		termIsKill  bool
		least, most time.Duration
	}{
		{"a wall that SIGTERM ends", true, 2*time.Second - 100*time.Millisecond, 3 * time.Second},
		{"no such wall", false, 1200*time.Millisecond - 100*time.Millisecond, 2*time.Second - 300*time.Millisecond},
	} {
		t.Run(c.name, func(t *testing.T) {
			spec := childSpec("deaf") // never reads stdin; dies on SIGTERM
			spec.StopGrace = 2 * time.Second
			spec.ArgvContainment.TermIsKill = c.termIsKill
			s := mustStart(t, spec, nil)
			began := time.Now()
			if err := s.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			if took := time.Since(began); took < c.least || took > c.most {
				t.Fatalf("%s: a child given 2s was stopped after %s, want %s to %s", c.name, took, c.least, c.most)
			}
		})
	}
}
