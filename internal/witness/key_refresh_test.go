package witness

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// key_refresh_test.go — a witness key file kept beside the ledger is
// history, not a cache (headverify.go persistWitnessKey; P3 of the witness
// design, revision 5).

// THE KEY FILE IS REPLACED ONLY BY ONE THAT VERIFIES ALL IT DID. The same
// manifest again changes nothing; a re-signed manifest with the same
// window, and one whose window was widened at either end, replace it. A
// changed envelope under the same key id, a window narrowed at either end,
// a lower bound given to a window that had none, and a disjoint window are
// refused (ErrWitnessKeyKept), and the kept file stays byte for byte.
func TestAKeptWitnessKeyIsReplacedOnlyByOneThatVerifiesAllItDid(t *testing.T) {
	p := synthPlatform(t)
	platform := p.env
	fw := newFakeWitness(t)
	created := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	from := created.Add(-time.Hour)
	to := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	at := func(t0 time.Time) string { return t0.Format(time.RFC3339) }
	kept := buildManifestWindow(t, p, fw.witnessEnv, false, created, at(from), to)
	envelope := mustCanonical(t, fw.witnessEnv)
	// Another envelope under the same key id: the same key, another
	// creation second, and a manifest that vouches for exactly it.
	reissued := *fw.witnessEnv
	reissued.CreatedAt = created.Add(-time.Minute).Format(time.RFC3339)
	reissuedManifest := buildManifestWindow(t, p, &reissued, false, created, at(from), to)

	for _, tc := range []struct {
		name     string
		manifest []byte
		envelope []byte
		refused  bool
	}{
		{"the same manifest", kept, envelope, false},
		{"re-signed, the same window", buildManifestWindow(t, p, fw.witnessEnv, false, created.Add(time.Minute), at(from), to), envelope, false},
		{"widened below and above", buildManifestWindow(t, p, fw.witnessEnv, false, created, at(from.Add(-time.Hour)), to.Add(time.Hour)), envelope, false},
		{"no lower bound now", buildManifestWindow(t, p, fw.witnessEnv, false, created, "", to), envelope, false},
		{"another envelope under the same key id", reissuedManifest, mustCanonical(t, &reissued), true},
		{"ends earlier", buildManifestWindow(t, p, fw.witnessEnv, false, created, at(from), to.Add(-time.Hour)), envelope, true},
		{"begins later", buildManifestWindow(t, p, fw.witnessEnv, false, created, at(from.Add(time.Minute)), to), envelope, true},
		{"disjoint", buildManifestWindow(t, p, fw.witnessEnv, false, created, at(to.Add(time.Hour)), to.Add(48*time.Hour)), envelope, true},
		{"does not verify", buildManifestWindow(t, p, fw.witnessEnv, true, created, at(from), to.Add(time.Hour)), envelope, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := persistWitnessKey(dir, fw.witnessEnv.KeyID, kept, envelope, platform); err != nil {
				t.Fatalf("rig: the first key file: %v", err)
			}
			path := filepath.Join(WitnessKeysDir(dir), fw.witnessEnv.KeyID+".json")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			err = persistWitnessKey(dir, fw.witnessEnv.KeyID, tc.manifest, tc.envelope, platform)
			after, _ := os.ReadFile(path)
			switch {
			case tc.refused && !errors.Is(err, ErrWitnessKeyKept):
				t.Fatalf("A REFRESH THAT DOES NOT VERIFY ALL THE KEPT FILE DID WAS NOT REFUSED: %v", err)
			case tc.refused && !bytes.Equal(after, before):
				t.Fatal("THE KEPT KEY FILE WAS CHANGED BY A REFUSED REFRESH")
			case !tc.refused && err != nil:
				t.Fatalf("a refresh that verifies all the kept file did was refused: %v", err)
			case !tc.refused && !bytes.Contains(after, tc.manifest):
				t.Fatal("the refresh that may replace the kept file did not")
			}
			// Whatever stands verifies as boot reads it.
			if _, err := LoadWitnessKeys(dir, platform); err != nil {
				t.Fatalf("the key file that stands does not load: %v", err)
			}
		})
	}
}

// A WINDOW WITH NO BEGINNING IS NOT GIVEN ONE. An absent not_before is
// unbounded below; a refresh that bounds it narrows it.
func TestAKeptWindowWithNoBeginningIsNotGivenOne(t *testing.T) {
	p := synthPlatform(t)
	fw := newFakeWitness(t)
	created := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	to := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	dir := t.TempDir()
	if err := persistWitnessKey(dir, fw.witnessEnv.KeyID, buildManifestWindow(t, p, fw.witnessEnv, false, created, "", to), mustCanonical(t, fw.witnessEnv), p.env); err != nil {
		t.Fatal(err)
	}
	bounded := buildManifestWindow(t, p, fw.witnessEnv, false, created, created.Add(-24*time.Hour).Format(time.RFC3339), to.Add(time.Hour))
	if err := persistWitnessKey(dir, fw.witnessEnv.KeyID, bounded, mustCanonical(t, fw.witnessEnv), p.env); !errors.Is(err, ErrWitnessKeyKept) {
		t.Fatalf("A WINDOW WITH NO BEGINNING WAS GIVEN ONE: %v", err)
	}
}

// A WITNESS THAT SERVES A NARROWED MANIFEST IS NOT TAKEN: the pass is
// refused, the kept file stands, the operator's seam is told once, and
// nothing is latched.
func TestAWitnessServingANarrowedManifestIsRefusedAndTold(t *testing.T) {
	p := synthPlatform(t)
	fw := newFakeWitness(t)
	created := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	to := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	fw.manifest = buildManifestWindow(t, p, fw.witnessEnv, false, created, "", to)
	lg, kp := testLedger(t, 3)
	dir := filepath.Dir(lg.Path())
	a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), &recordStore{lg: lg}, &recordStore{lg: lg}, testMinter{lg, kp}, 1, p.writeEnv(t))
	var told []error
	a.SetOnWitnessKeyKept(func(err error) { told = append(told, err) })
	var alarms []*ConflictError
	a.SetOnIntegrityConflict(func(ce *ConflictError) { alarms = append(alarms, ce) })
	if err := a.CheckAndAnchor(); err != nil {
		t.Fatalf("rig: the first anchor: %v", err)
	}
	path := filepath.Join(WitnessKeysDir(dir), fw.witnessEnv.KeyID+".json")
	kept, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fw.mu.Lock()
	fw.manifest = buildManifestWindow(t, p, fw.witnessEnv, false, created, "", to.Add(-time.Hour))
	fw.mu.Unlock()
	for pass := 1; pass <= 2; pass++ {
		appendRecords(t, lg, kp, 1)
		if err := a.CheckAndAnchor(); !errors.Is(err, ErrWitnessKeyKept) {
			t.Fatalf("pass %d: A NARROWED MANIFEST WAS NOT REFUSED: %v", pass, err)
		}
	}
	if now, _ := os.ReadFile(path); !bytes.Equal(now, kept) {
		t.Fatal("THE KEPT KEY FILE WAS CHANGED")
	}
	if len(told) != 1 || len(alarms) != 0 || a.IntegrityConflict() != nil {
		t.Fatalf("told %d times (want 1), %d alarms, latched %v", len(told), len(alarms), a.IntegrityConflict())
	}
}

// THE KEYS DIRECTORY'S FIRST CREATION IS ONE THE NEXT KEY FILE CAN STAND
// IN: made, and made again after it was removed. Owner-only where the file
// system says so in the mode (0700); on Windows the mode does not carry it
// (Go reports the attribute bits, and privacy there is the folder's DACL),
// so there the directory is held to being one.
func TestTheWitnessKeysDirectoryIsMadeWhenFirstNeeded(t *testing.T) {
	p := synthPlatform(t)
	fw := newFakeWitness(t)
	dir := t.TempDir()
	keep := func() {
		t.Helper()
		if err := persistWitnessKey(dir, fw.witnessEnv.KeyID, buildManifest(t, p, fw.witnessEnv, false), mustCanonical(t, fw.witnessEnv), nil); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(WitnessKeysDir(dir))
		if err != nil || !info.IsDir() {
			t.Fatalf("THE KEYS DIRECTORY WAS NOT MADE: %v %v", info, err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
			t.Fatalf("the keys directory is %o, want 0700", info.Mode().Perm())
		}
		if _, err := os.Stat(filepath.Join(WitnessKeysDir(dir), fw.witnessEnv.KeyID+".json")); err != nil {
			t.Fatalf("the key file does not stand in the keys directory: %v", err)
		}
	}
	keep()
	if err := os.RemoveAll(WitnessKeysDir(dir)); err != nil {
		t.Fatal(err)
	}
	keep()
}
