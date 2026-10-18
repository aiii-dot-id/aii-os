package aiiosexport_test

import (
	"context"
	"path/filepath"
	"testing"

	aiiosexport "github.com/aiii-dot-id/aii-os/export"
)

// writer is what a verifier outside the module brings to an engine: the real
// ledger signs and appends, the real store materializes.
type writer struct {
	lg *aiiosexport.Ledger
	st *aiiosexport.Store
	kp *aiiosexport.KeyPair
}

func (w *writer) Append(typ aiiosexport.EventType, ring int, payload any, _ string) (*aiiosexport.LedgerEvent, error) {
	evt, err := w.lg.Append(typ, ring, payload, w.kp)
	if err != nil {
		return nil, err
	}
	return evt, w.st.Materialize(evt)
}

func TestTheSeamBuildsWhatAnOutsideVerifierBuilds(t *testing.T) {
	dir := t.TempDir()
	reg := aiiosexport.NewRegistry(dir, nil, aiiosexport.Timeouts{})
	res, err := reg.Execute(context.Background(), "read", map[string]any{"file_path": filepath.Join(t.TempDir(), "outside.txt")})
	if err == nil && res.Error == "" {
		t.Fatalf("a read outside the sandbox was not refused: %+v", res)
	}

	kp, err := aiiosexport.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	ledgerPath := filepath.Join(dir, "ledger.jsonl")
	lg, err := aiiosexport.LedgerNew(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	// The ledger holds its lock file open until it is closed, and Windows
	// will not remove a file that is open: without this the test's own
	// directory could not be cleaned up there, and the test failed for
	// that and nothing else (found by the whole suite's run on Windows).
	defer lg.Close()
	st, err := aiiosexport.StoreNew(filepath.Join(dir, "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	w := &writer{lg: lg, st: st, kp: kp}
	if _, err := w.Append(aiiosexport.EventRing0Genesis, 0, map[string]any{"name": "seam"}, ""); err != nil {
		t.Fatalf("genesis through the seam: %v", err)
	}
	eng := aiiosexport.NewEngine(st, w, aiiosexport.NewRingManager(), nil, aiiosexport.EnginePorts{})
	if _, err := eng.ExecuteAction(context.Background(), "verb", "commit", map[string]any{"variant": "belief.promote", "id": "nothing"}); err == nil {
		t.Fatal("promoting a belief that does not exist was accepted")
	}
	if n, err := aiiosexport.VerifyChain(ledgerPath, kp.PublicKeyBytes(), nil); err != nil || n < 1 {
		t.Fatalf("the stranger's path does not verify the chain this test wrote: %d events, %v", n, err)
	}
}
