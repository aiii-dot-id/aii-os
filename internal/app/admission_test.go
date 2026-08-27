package app

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

type failingProjection struct {
	validated []byte
	exact     bool
}

func newAdmissionLedger(t *testing.T) (string, *crypto.KeyPair, *ledger.Ledger) {
	t.Helper()
	dir := t.TempDir()
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	lg, err := ledger.New(filepath.Join(dir, "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lg.Close(); err != nil {
			t.Errorf("close ledger: %v", err)
		}
	})
	return dir, kp, lg
}

func newAdmissionStore(t *testing.T, dir string) *store.Store {
	t.Helper()
	st, err := store.New(filepath.Join(dir, "identity.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return st
}

func (p *failingProjection) ValidateEvent(_ ledger.EventType, _ int, payload []byte) error {
	p.validated = append([]byte(nil), payload...)
	return nil
}

func (p *failingProjection) Materialize(evt *ledger.Event) error {
	p.exact = bytes.Equal(p.validated, evt.Payload)
	return errors.New("injected materialization failure")
}

func TestAdmissionDivergenceFreezesAndEntersSafe(t *testing.T) {
	_, kp, lg := newAdmissionLedger(t)
	lg.SetModelID("configured-model")
	projection := &failingProjection{}
	a := &App{ledger: lg}
	door := &ledgerAdapter{Ledger: lg, kp: kp, st: projection, onIntegrity: func(err error) { a.enterSafe(err.Error()) }}

	evt, err := door.Append(ledger.EventExperienceCreate, 3,
		map[string]interface{}{"id": "e1", "content": "observed"}, "")
	if err == nil || evt == nil || !projection.exact {
		t.Fatalf("divergence result: event=%v exact=%v err=%v", evt, projection.exact, err)
	}
	if evt.Author != kp.Fingerprint() || evt.SigKeyID != evt.Author {
		t.Fatalf("door split author from signing key: author=%q key=%q", evt.Author, evt.SigKeyID)
	}
	if _, safe := a.SafeMode(); !safe {
		t.Fatal("post-append projection failure did not enter SAFE")
	}
	if _, err := door.Append(ledger.EventExperienceCreate, 3,
		map[string]interface{}{"id": "e2", "content": "blocked"}, ""); err == nil {
		t.Fatal("admission remained open after divergence")
	}
	a.resetModeForTest()
}

func TestTailSignatureCorruptionEntersSafe(t *testing.T) {
	dir, kp, lg := newAdmissionLedger(t)
	path := lg.Path()
	st := newAdmissionStore(t, dir)
	a := &App{ledger: lg}
	door := &ledgerAdapter{Ledger: lg, kp: kp, st: st, onIntegrity: func(err error) { a.enterSafe(err.Error()) }}
	if _, err := door.Append(ledger.EventExperienceCreate, 3,
		map[string]interface{}{"id": "e1", "content": "observed"}, ""); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(raw, []byte(`"signature":"`), []byte(`"signature":"invalid`), 1)
	if bytes.Equal(raw, tampered) {
		t.Fatal("test did not alter the ledger signature")
	}
	if err := os.WriteFile(path, tampered, 0600); err != nil {
		t.Fatal(err)
	}

	_, err = door.Append(ledger.EventExperienceCreate, 3,
		map[string]interface{}{"id": "e2", "content": "blocked"}, "")
	if !errors.Is(err, ledger.ErrTailIntegrity) {
		t.Fatalf("corrupted tail must return the typed integrity error, got %v", err)
	}
	if _, safe := a.SafeMode(); !safe {
		t.Fatal("corrupted tail did not enter SAFE")
	}
	a.resetModeForTest()
}

func TestAdmissionSerializesPreflightThroughMaterialization(t *testing.T) {
	dir, kp, lg := newAdmissionLedger(t)
	st := newAdmissionStore(t, dir)
	door := &ledgerAdapter{Ledger: lg, kp: kp, st: st}
	payload := map[string]interface{}{"id": "same", "statement": "one goal"}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := door.Append(ledger.EventIntentionCreate, 3, payload, "")
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	succeeded := 0
	for err := range errs {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 || lg.LastSeq() != 1 {
		t.Fatalf("concurrent admission: successes=%d seq=%d", succeeded, lg.LastSeq())
	}
	if err := st.ReplayFromFile(lg.Path()); err != nil {
		t.Fatalf("admitted chain does not replay: %v", err)
	}
}

func TestAdmissionKeepsProducingModelAcrossSwitch(t *testing.T) {
	dir, kp, lg := newAdmissionLedger(t)
	st := newAdmissionStore(t, dir)
	door := &ledgerAdapter{Ledger: lg, kp: kp, st: st}
	lg.SetModelID("model-b")

	oldWork, err := door.Append(ledger.EventExperienceCreate, 3,
		map[string]interface{}{"id": "from-a", "content": "completed before switch"}, "model-a")
	if err != nil {
		t.Fatal(err)
	}
	newWork, err := door.Append(ledger.EventExperienceCreate, 3,
		map[string]interface{}{"id": "from-b", "content": "completed after switch"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if oldWork.ModelID != "model-a" || newWork.ModelID != "model-b" {
		t.Fatalf("model provenance crossed switch: old=%q new=%q", oldWork.ModelID, newWork.ModelID)
	}
}
