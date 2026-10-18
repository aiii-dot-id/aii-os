package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestOwnedCredentialPathReadsThePublishedConfigUnderItsOwner(t *testing.T) {
	cfg := &Config{}
	cfg.Identity.LedgerPath = filepath.Join(t.TempDir(), "before", "ledger.jsonl")
	a := New(cfg)
	// Check ownership at the actual read step, not whether the scheduler
	// happened to run the reader before publication. Removing RLock now
	// fails deterministically even without the race detector.
	visited := false
	a.credentialPathRead = func() {
		visited = true
		if a.cfgMu.TryLock() {
			a.cfgMu.Unlock()
			t.Error("credential config read does not hold its owner's lock")
		}
	}
	if _, err := a.ownedCredentialPath("file", "fixture.json"); err != nil {
		t.Fatal(err)
	}
	if !visited {
		t.Fatal("credential config read step was not reached")
	}
	a.credentialPathRead = nil
	wantLedger := filepath.Join(t.TempDir(), "after", "ledger.jsonl")
	type result struct {
		path string
		err  error
	}
	ready, done := make(chan struct{}), make(chan result, 1)
	a.cfgMu.Lock()
	go func() {
		close(ready)
		path, err := a.ownedCredentialPath("file", "fixture.json")
		done <- result{path, err}
	}()
	<-ready
	// This is reload's actual publication shape: even an unchanged field
	// is written by whole-value assignment. The reader must wait for it.
	fresh := *a.cfg
	fresh.Identity.LedgerPath = wantLedger
	*a.cfg = fresh
	a.cfgMu.Unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	select {
	case got := <-done:
		want := filepath.Join(identityCredentials(wantLedger), "fixture.json")
		if got.err != nil || got.path != want {
			t.Fatalf("credential path did not use published config: %q want %q, %v", got.path, want, got.err)
		}
	case <-ctx.Done():
		t.Fatal("credential path remained blocked after config publication")
	}
}
