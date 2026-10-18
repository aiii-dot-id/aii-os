package witness

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
)

func TestMalformedEnvelopeCacheRecoversOnlyTheRecordedIdentity(t *testing.T) {
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	key := AsIdentityKey(kp)
	born := time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC)
	enrolledAt := born.Add(time.Hour)
	original, did := enrolled(t, key, enrolledAt)
	for _, damaged := range []string{"not JSON", `{}`, `{"kind":"aiii.server_key.public","key_type":"identity","keys":[]}`} {
		t.Run(damaged, func(t *testing.T) {
			st := &memEnvelopeStore{saved: []byte(damaged), enrolledIDs: []string{did}, born: born, witness: enrolledAt.Add(time.Second)}
			got, env, err := EnsureIdentityEnvelope(key, st, t.TempDir())
			if err != nil {
				t.Fatalf("record-backed recovery stopped at the damaged cache: %v", err)
			}
			if !bytes.Equal(got, original) || !bytes.Equal(st.saved, original) || st.replaces != 1 || st.saves != 0 {
				t.Fatalf("did not replace only the cache with the original envelope: saves=%d replaces=%d", st.saves, st.replaces)
			}
			if gotID, err := DeriveIdentityID(got, env); err != nil || gotID != did {
				t.Fatalf("recovered another identity: %s %v", gotID, err)
			}
			if _, _, err := EnsureIdentityEnvelope(key, st, t.TempDir()); err != nil || st.replaces != 1 {
				t.Fatalf("recovered cache was not stable: %v", err)
			}
		})
	}
}

func TestMalformedEnvelopeWithoutRecordedEnrollmentNeverRemints(t *testing.T) {
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	st := &memEnvelopeStore{saved: []byte("not JSON")}
	if _, _, err := EnsureIdentityEnvelope(AsIdentityKey(kp), st, t.TempDir()); err == nil {
		t.Fatal("a damaged pre-anchor envelope was silently replaced")
	}
	if string(st.saved) != "not JSON" || st.saves != 0 || st.replaces != 0 {
		t.Fatal("recovery without a recorded DID changed the cache")
	}
}

func TestMalformedEnvelopeRemainsUnchangedWithoutAProvedReplacement(t *testing.T) {
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	key := AsIdentityKey(kp)
	_, did := enrolled(t, key, time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC))
	st := &memEnvelopeStore{saved: []byte(`{}`), enrolledIDs: []string{did}}
	if _, _, err := EnsureIdentityEnvelope(key, st, t.TempDir()); !errors.Is(err, ErrEnvelopeUnrecoverable) {
		t.Fatalf("missing recovery evidence must refuse, not mint: %v", err)
	}
	if string(st.saved) != "{}" || st.saves != 0 || st.replaces != 0 {
		t.Fatal("unproved recovery changed the old cache")
	}
}

type refusingEnvelopeReplace struct {
	memEnvelopeStore
	fault error
}

func (s *refusingEnvelopeReplace) ReplaceWitnessEnvelope([]byte) error { return s.fault }

func TestMalformedEnvelopeRepairReportsPersistenceFailure(t *testing.T) {
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	key := AsIdentityKey(kp)
	born := time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC)
	_, did := enrolled(t, key, born.Add(time.Hour))
	fault := errors.New("fixture write unavailable")
	st := &refusingEnvelopeReplace{memEnvelopeStore: memEnvelopeStore{saved: []byte(`{}`), enrolledIDs: []string{did}, born: born, witness: born.Add(time.Hour + time.Second)}, fault: fault}
	got, env, err := EnsureIdentityEnvelope(key, st, t.TempDir())
	if !errors.Is(err, fault) || got != nil || env != nil {
		t.Fatalf("failed persistence was acknowledged: %v", err)
	}
	if string(st.saved) != "{}" || st.saves != 0 {
		t.Fatal("failed persistence altered the cache or minted a new identity")
	}
}
