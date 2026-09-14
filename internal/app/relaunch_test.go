package app

import "testing"

// .
// .
func TestRestartIsAskedOnceAndReleasesRun(t *testing.T) {
	a := New(defaultConfig())
	if a.restarting.Load() {
		t.Fatal("not restarting before it is asked")
	}
	if err := a.Restart(); err != nil {
		t.Fatal(err)
	}
	if err := a.Restart(); err != nil {
		t.Fatal("a second ask is the same ask")
	}
	select {
	case <-a.restartCh:
	default:
		t.Fatal("Run's select is not released")
	}
	if !a.restarting.Load() {
		t.Fatal("Run would not relaunch")
	}
}
