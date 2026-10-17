package app

import (
	"bytes"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestMalformedWitnessEnvelopeCacheRepairsWithoutChangingIdentity(t *testing.T) {
	f := newSealedFixture(t)
	f.bootAndAnchor(t)
	st, err := store.NewForTest(f.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	original, err := st.LoadWitnessEnvelope()
	if err != nil || len(original) == 0 {
		st.Close()
		t.Fatalf("fixture envelope: %v", err)
	}
	if err := st.ReplaceWitnessEnvelope([]byte(`{}`)); err != nil {
		st.Close()
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	// Valid JSON but no usable envelope: a realistic wrong-row shape,
	// accepted by SQLite's json_valid constraint, unlike arbitrary bytes.
	a := New(f.cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	if reason, safe := a.SafeMode(); safe {
		t.Fatalf("cache damage made intact history SAFE: %s", reason)
	}
	appendFixtureEvent(t, a, f.keyPath, "life_after_bad_envelope_cache")
	if err := a.anchorer.CheckAndAnchor(); err != nil {
		t.Fatalf("original witness relationship did not recover: %v", err)
	}
	got, err := a.store.LoadWitnessEnvelope()
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("cache was not restored byte-for-byte: %v", err)
	}
	if _, identities := f.witness.Holds(); identities != 1 {
		t.Fatalf("repair enrolled %d identities", identities)
	}
	stopApp(t, a)
	next := New(f.cfg)
	if err := startLiveForTest(next); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApp(t, next) })
	if reason, safe := next.SafeMode(); safe {
		t.Fatalf("restart after repair is SAFE: %s", reason)
	}
	appendFixtureEvent(t, next, f.keyPath, "life_after_envelope_repair_restart")
}
