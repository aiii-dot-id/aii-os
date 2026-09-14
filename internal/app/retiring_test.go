package app

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// .
// .
// .
// .
func TestRetiredPredecessorsHaveExactlyOneStopper(t *testing.T) {
	a := &App{}
	ap := &pluginhost.ActivePlugin{ID: "com.example.voice"}
	a.retire(ap)
	if !a.claimRetiring(ap) {
		t.Fatal("the first claim takes the predecessor")
	}
	if a.claimRetiring(ap) {
		t.Fatal("a second claim must find nothing — one stopper only")
	}
	// .
	b := &App{}
	b.retire(ap)
	b.pluginMu.Lock()
	retiring := b.retiring
	b.retiring = nil
	b.pluginMu.Unlock()
	if len(retiring) != 1 || retiring[0] != ap {
		t.Fatalf("Stop's take = %v", retiring)
	}
	if b.claimRetiring(ap) {
		t.Fatal("after Stop took it, the release goroutine must not stop it again")
	}
}
