package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func TestSealRecoveryAdmissionRetriesTheSamePreparedEventOnce(t *testing.T) {
	dir, kp, lg := newAdmissionLedger(t)
	st := newAdmissionStore(t, dir)
	calls, recoveries, notified := 0, 0, 0
	var original []byte
	door := &ledgerAdapter{Ledger: lg, kp: kp, st: st}
	door.appendPrepared = func(kind ledger.EventType, ring int, p ledger.PreparedPayload, key *crypto.KeyPair) (*ledger.Event, error) {
		calls++
		if calls == 1 {
			original = bytes.Clone(p.Bytes())
			return nil, ledger.ErrSealRecoveryRequired
		}
		if calls == 2 && !bytes.Equal(original, p.Bytes()) {
			t.Fatal("recovery re-prepared the event")
		}
		return lg.AppendPrepared(kind, ring, p, key)
	}
	door.recoverSeal = func() error {
		recoveries++
		lg.SetModelID("later-model")
		return nil
	}
	door.onAppend = func(*ledger.Event) { notified++ }
	door.onIntegrity = func(_ safeTrigger, err error) { t.Fatalf("operational recovery entered SAFE: %v", err) }
	evt, err := door.Append(ledger.EventExperienceCreate, 3, map[string]string{"id": "first", "content": "one event"}, "original-model")
	if err != nil || evt == nil || evt.Seq != 1 || calls != 2 || recoveries != 1 || notified != 1 {
		t.Fatalf("admission result: evt=%v err=%v calls=%d recovery=%d notifications=%d", evt, err, calls, recoveries, notified)
	}
	if pin, err := st.PinAt(context.Background(), evt.Seq, evt.EntryHash()); err != nil {
		t.Fatalf("recovered event not materialized: %v", err)
	} else {
		pin.Release()
	}
	if _, err := door.Append(ledger.EventExperienceCreate, 3, map[string]string{"id": "second", "content": "normal again"}, ""); err != nil {
		t.Fatal(err)
	}
	if calls != 3 || recoveries != 1 || notified != 2 {
		t.Fatal("healthy admission repeated recovery")
	}
}

func TestSealRecoveryAdmissionClassifiesFailuresAndDoesNotLoop(t *testing.T) {
	ioErr := &os.PathError{Op: "reopen", Path: "fixture", Err: os.ErrPermission}
	for _, tc := range []struct {
		name                   string
		appendErr, recoverErr  error
		wantCalls, wantRecover int
		wantSafe               bool
	}{
		{"operational", ledger.ErrSealRecoveryRequired, ioErr, 1, 1, false},
		{"proof unavailable", ledger.ErrSealRecoveryRequired, ledger.ErrSealedWithoutWitness, 1, 1, false},
		{"integrity", ledger.ErrSealRecoveryRequired, ledger.ErrTailIntegrity, 1, 1, true},
		{"retry once", ledger.ErrSealRecoveryRequired, nil, 2, 1, false},
		{"uncertain append", ledger.ErrAppendUncertain, nil, 1, 0, true},
		{"ordinary I/O", ioErr, nil, 1, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, kp, lg := newAdmissionLedger(t)
			st := newAdmissionStore(t, dir)
			a := &App{ledger: lg}
			defer a.resetModeForTest()
			calls, recoveries, integrity := 0, 0, 0
			door := &ledgerAdapter{Ledger: lg, kp: kp, st: st}
			door.appendPrepared = func(ledger.EventType, int, ledger.PreparedPayload, *crypto.KeyPair) (*ledger.Event, error) {
				calls++
				return nil, tc.appendErr
			}
			door.recoverSeal = func() error { recoveries++; return tc.recoverErr }
			door.onIntegrity = func(_ safeTrigger, err error) {
				integrity++
				if !door.mu.TryLock() {
					t.Fatal("SAFE callback ran under admission lock")
				}
				door.mu.Unlock()
				a.enterSafe(err.Error())
			}
			_, err := door.Append(ledger.EventExperienceCreate, 3, map[string]string{"id": "blocked", "content": "not appended"}, "")
			wantErr := tc.appendErr
			if tc.recoverErr != nil {
				wantErr = tc.recoverErr
			}
			if !errors.Is(err, wantErr) || calls != tc.wantCalls || recoveries != tc.wantRecover || lg.LastSeq() != 0 {
				t.Fatalf("result=%v calls=%d recovery=%d seq=%d", err, calls, recoveries, lg.LastSeq())
			}
			_, safe := a.SafeMode()
			if safe != tc.wantSafe || (integrity == 1) != tc.wantSafe {
				t.Fatalf("false or missing SAFE: %v, calls=%d", safe, integrity)
			}
		})
	}
}

func TestSealRecoveryCaptureRetriesAndPinsTheSameBoundary(t *testing.T) {
	dir, kp, lg := newAdmissionLedger(t)
	st := newAdmissionStore(t, dir)
	door := &ledgerAdapter{Ledger: lg, kp: kp, st: st}
	evt, err := door.Append(ledger.EventExperienceCreate, 3, map[string]string{"id": "captured", "content": "kept"}, "")
	if err != nil {
		t.Fatal(err)
	}
	calls, recoveries := 0, 0
	door.captureRecord = func(dst string) (ledger.Capture, error) {
		calls++
		if calls == 1 {
			return ledger.Capture{}, ledger.ErrSealRecoveryRequired
		}
		return lg.Capture(dst)
	}
	door.recoverSeal = func() error { recoveries++; return nil }
	c, pin, err := door.capture(context.Background(), filepath.Join(t.TempDir(), "tail"))
	if err != nil {
		t.Fatal(err)
	}
	defer pin.Release()
	if calls != 2 || recoveries != 1 || c.LastSeq != evt.Seq || c.LastHash != evt.EntryHash() {
		t.Fatalf("capture=%+v calls=%d recovery=%d", c, calls, recoveries)
	}
}

func TestSealRecoveryCaptureIntegrityReachesSafeOutsideTheDoor(t *testing.T) {
	dir, kp, lg := newAdmissionLedger(t)
	st := newAdmissionStore(t, dir)
	a := &App{ledger: lg}
	defer a.resetModeForTest()
	door := &ledgerAdapter{Ledger: lg, kp: kp, st: st}
	door.captureRecord = func(string) (ledger.Capture, error) { return ledger.Capture{}, ledger.ErrSealRecoveryRequired }
	door.recoverSeal = func() error { return ledger.ErrTailIntegrity }
	door.onIntegrity = func(_ safeTrigger, err error) {
		if !door.mu.TryLock() {
			t.Fatal("capture called SAFE under admission lock")
		}
		door.mu.Unlock()
		a.enterSafe(err.Error())
	}
	_, pin, err := door.capture(context.Background(), filepath.Join(t.TempDir(), "tail"))
	if !errors.Is(err, ledger.ErrTailIntegrity) || pin != nil {
		t.Fatalf("capture=%v pin=%v", err, pin)
	}
	if _, safe := a.SafeMode(); !safe {
		t.Fatal("corrupt recovery did not reach SAFE")
	}
}

func TestStartupWiresFreshOfflineSealRecovery(t *testing.T) {
	f := newSealedFixture(t)
	f.bootAndAnchor(t)
	a := New(f.cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	if a.door.recoverSeal == nil {
		t.Fatal("startup omitted recovery")
	}
	if err := a.door.recoverSeal(); err != nil {
		t.Fatalf("healthy startup recovery callback: %v", err)
	}
	if _, safe := a.SafeMode(); safe {
		t.Fatal("healthy identity became SAFE")
	}
	appendFixtureEvent(t, a, f.keyPath, "after_recovery_wiring_check")
}
