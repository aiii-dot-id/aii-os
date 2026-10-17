package app

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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
	st, err := store.NewForTest(filepath.Join(dir, "identity.db"))
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
	door := &ledgerAdapter{Ledger: lg, kp: kp, st: projection, onIntegrity: a.integrityLost}

	evt, err := door.Append(ledger.EventExperienceCreate, 3,
		map[string]any{"id": "e1", "content": "observed"}, "")
	if err == nil || evt == nil || !projection.exact {
		t.Fatalf("divergence result: event=%v exact=%v err=%v", evt, projection.exact, err)
	}
	if evt.Sig == "" || evt.Content == "" {
		t.Fatalf("door returned a record without its proof or content hash: %+v", evt)
	}
	if reason, safe := a.SafeMode(); !safe || !strings.HasPrefix(reason, safePostures[safeDivergence].what+": ") {
		t.Fatalf("post-append projection failure did not enter SAFE as a divergence: %q", reason)
	}
	if _, err := door.Append(ledger.EventExperienceCreate, 3,
		map[string]any{"id": "e2", "content": "blocked"}, ""); err == nil {
		t.Fatal("admission remained open after divergence")
	}
	a.resetModeForTest()
}

// preflightProjection answers every admission with err.
type preflightProjection struct{ err error }

func (p preflightProjection) ValidateEvent(ledger.EventType, int, []byte) error { return p.err }
func (p preflightProjection) Materialize(*ledger.Event) error                   { return nil }

// readOnlyWrite is a real store fault: a write to a database opened read-only.
func readOnlyWrite(t *testing.T) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ro.db")
	rw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rw.Exec("CREATE TABLE t (x)"); err != nil {
		t.Fatal(err)
	}
	rw.Close()
	ro, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	_, err = ro.Exec("INSERT INTO t VALUES (1)")
	if !store.Fault(err) {
		t.Fatalf("inconclusive: a write to a read-only database is not a store fault: %v", err)
	}
	return err
}

// THE DOOR TELLS A FAULT FROM A REFUSAL. It said "refused before append"
// for every error before the append, so a store that could not run the
// rules read as a verdict on the act, to the identity and to CONSOLIDATE,
// which drops refused acts and consumes their evidence. A refusal keeps
// its words and is marked store.ErrRefused; a fault says what it is.
func TestTheDoorTellsAFaultFromARefusal(t *testing.T) {
	_, kp, lg := newAdmissionLedger(t)
	refusal := errors.New("ring 2 is not a legal authority for experience.create")
	fault := fmt.Errorf("preflight seq probe: %w", readOnlyWrite(t))
	for _, c := range []struct {
		cause   error
		refused bool
		words   string
	}{
		{refusal, true, "refused before append: " + refusal.Error()},
		{fault, false, "admission could not run; nothing was appended: " + fault.Error()},
	} {
		door := &ledgerAdapter{Ledger: lg, kp: kp, st: preflightProjection{c.cause}}
		evt, err := door.Append(ledger.EventExperienceCreate, 3, map[string]any{"id": "e1", "content": "observed"}, "")
		if evt != nil || err == nil {
			t.Fatalf("%v: the door admitted the event: %v %v", c.cause, evt, err)
		}
		if errors.Is(err, store.ErrRefused) != c.refused {
			t.Errorf("%v: marked refused = %t, want %t", c.cause, !c.refused, c.refused)
		}
		if err.Error() != c.words {
			t.Errorf("the door said %q\n  want %q", err.Error(), c.words)
		}
		if !errors.Is(err, c.cause) {
			t.Errorf("the cause is lost: %v", err)
		}
	}
	if _, err := os.Stat(lg.Path()); err == nil {
		if raw, _ := os.ReadFile(lg.Path()); len(bytes.TrimSpace(raw)) != 0 {
			t.Fatalf("an event refused or faulted before append was appended: %s", raw)
		}
	}
}

func TestTailSignatureCorruptionEntersSafe(t *testing.T) {
	dir, kp, lg := newAdmissionLedger(t)
	path := lg.Path()
	st := newAdmissionStore(t, dir)
	a := &App{ledger: lg}
	door := &ledgerAdapter{Ledger: lg, kp: kp, st: st, onIntegrity: a.integrityLost}
	if _, err := door.Append(ledger.EventExperienceCreate, 3,
		map[string]any{"id": "e1", "content": "observed"}, ""); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(raw, []byte(`"sig":"`), []byte(`"sig":"invalid`), 1)
	if bytes.Equal(raw, tampered) {
		t.Fatal("test did not alter the ledger signature")
	}
	if err := os.WriteFile(path, tampered, 0600); err != nil {
		t.Fatal(err)
	}

	_, err = door.Append(ledger.EventExperienceCreate, 3,
		map[string]any{"id": "e2", "content": "blocked"}, "")
	if !errors.Is(err, ledger.ErrTailIntegrity) {
		t.Fatalf("corrupted tail must return the typed integrity error, got %v", err)
	}
	if reason, safe := a.SafeMode(); !safe || !strings.HasPrefix(reason, safePostures[safeRecordAtRuntime].what+": ") {
		t.Fatalf("corrupted tail did not enter SAFE as a record failure: %q", reason)
	}
	a.resetModeForTest()
}

func TestAdmissionSerializesPreflightThroughMaterialization(t *testing.T) {
	dir, kp, lg := newAdmissionLedger(t)
	st := newAdmissionStore(t, dir)
	door := &ledgerAdapter{Ledger: lg, kp: kp, st: st}
	payload := map[string]any{"id": "same", "statement": "one goal"}

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
		map[string]any{"id": "from-a", "content": "completed before switch"}, "model-a")
	if err != nil {
		t.Fatal(err)
	}
	newWork, err := door.Append(ledger.EventExperienceCreate, 3,
		map[string]any{"id": "from-b", "content": "completed after switch"}, "")
	if err != nil {
		t.Fatal(err)
	}

	stamp := func(evt *ledger.Event) string {
		var p struct {
			ModelID string `json:"model_id"`
		}
		if err := json.Unmarshal(evt.Payload, &p); err != nil {
			t.Fatal(err)
		}
		return p.ModelID
	}
	if stamp(oldWork) != "model-a" || stamp(newWork) != "model-b" {
		t.Fatalf("model provenance crossed switch: old=%q new=%q", stamp(oldWork), stamp(newWork))
	}
}

// THE DOOR'S LOCK HOLDS ADMISSION OUT OF THE CAPTURE. The record is
// captured first and the database pinned second, and between the two an
// admitted event would leave the mirror one record past the boundary —
// the pin would refuse, rightly, and the identity would lose its
// snapshot on exactly the days it is busiest. So an event offered in
// that window waits at the door: it lands after the capture, and the
// capture holds.
func TestAnEventOfferedDuringTheCaptureWaitsAtTheDoor(t *testing.T) {
	a, _, _ := maintApp(t)
	landed := make(chan error, 1)
	var landedInsideTheCapture bool

	prev := snapshotStep
	t.Cleanup(func() { snapshotStep = prev })
	snapshotStep = func(step string) {
		if step != "tail-copied" {
			return
		}
		go func() {
			_, err := a.door.Append(ledger.EventExperienceCreate, 3, map[string]any{
				"id": "exp_offered_during_the_capture", "content": "x", "category": "observation", "provenance": "self",
			}, "")
			landed <- err
		}()
		// Long enough for an unguarded append to land many times over;
		// under the door's lock it cannot land however long this waits.
		select {
		case err := <-landed:
			landedInsideTheCapture = true
			landed <- err
		case <-time.After(300 * time.Millisecond):
		}
	}
	before := a.ledger.LastSeq()
	at, pin, err := a.door.capture(t.Context(), filepath.Join(t.TempDir(), "ledger.jsonl"))
	snapshotStep = prev
	if err != nil {
		t.Fatalf("THE CAPTURE WAS REFUSED because an event was admitted inside it: %v", err)
	}
	defer pin.Release()
	if landedInsideTheCapture {
		t.Fatal("an event was admitted between the record's capture and the database's pin")
	}
	if at.LastSeq != before || pin.Head().Hash != at.LastHash {
		t.Fatalf("captured at %d with the mirror at %d, want both at %d", at.LastSeq, pin.Head().Seq, before)
	}
	if err := <-landed; err != nil {
		t.Fatalf("the event that waited at the door was then refused: %v", err)
	}
	if a.ledger.LastSeq() != before+1 {
		t.Fatalf("the event that waited never landed: ledger at %d", a.ledger.LastSeq())
	}
}
