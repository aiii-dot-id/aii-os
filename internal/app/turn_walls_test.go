package app

import (
	"testing"
	"time"
)

// A CONTINUED LEG ENDS AT THE OPERATOR'S WALL. Set at the door to one
// second, the next leg whose model never answers is cut there; the
// forty-five minutes the code carried would have held it.
func TestAContinuedLegEndsAtTheOperatorsWall(t *testing.T) {
	a, _ := dispatchApp(t)
	t.Cleanup(func() { stopApp(t, a) })
	// This test measures the wall and its cut-short notice, independently of
	// the separately tested bounded timeout recovery.
	off := false
	a.cfgMu.Lock()
	a.cfg.Agency.TurnContinuation = &off
	a.cfgMu.Unlock()
	if _, err := a.applyConfigChange(map[string]any{"agency.continuation_wall_seconds": float64(1)}); err != nil {
		t.Fatalf("the door refused a wall of one second: %v", err)
	}
	asked := hangingModel(t, a) // never answers; the client waits ten minutes
	done := make(chan struct{})
	go func() {
		a.continueCappedTurn("ws_wall", 1, capDeclared, "")
		close(done)
	}()
	select {
	case <-asked:
	case <-time.After(20 * time.Second):
		t.Fatal("rig: the leg never asked the model")
	}
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("the continued leg ran past the operator's wall of one second")
	}
	// Cut during its first request, before a word: the operator is told
	// it was cut short, and by what (row 9.51).
	if got, want := outboxReply(t, a, "continuation_1_"), "The next part of ws_wall was cut short: it reached the wall of a continued turn (agency.continuation_wall_seconds, 1 s)."; got != want {
		t.Errorf("the operator is told %q, want %q", got, want)
	}
}
