package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/witness"
	"github.com/aiii-dot-id/aii-os/internal/witness/witnesstest"
)

// .
// .
// .
// .
// .
// .

type sealedFixture struct {
	dir, keyPath, ledgerPath, dbPath string
	platform                         *witnesstest.Platform
	witness                          *witnesstest.Witness
	cfg                              *Config
}

func newSealedFixture(t *testing.T) *sealedFixture {
	t.Helper()
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Sealed")
	buildPriorProjection(t, ledgerPath, dbPath)
	platform := witnesstest.NewPlatform(t)
	fw := witnesstest.NewWitness(t, platform)
	cfg := safebootConfig(t, dir, "Sealed", keyPath, ledgerPath, dbPath)
	cfg.Witness = WitnessConfig{URL: fw.URL(), IntervalEvents: 1, PlatformPubkeyPath: platform.WriteEnv(t, dir)}
	return &sealedFixture{dir: dir, keyPath: keyPath, ledgerPath: ledgerPath, dbPath: dbPath, platform: platform, witness: fw, cfg: cfg}
}

// .
// .
func (f *sealedFixture) bootAndAnchor(t *testing.T) uint64 {
	t.Helper()
	app := New(f.cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("boot: %v", err)
	}
	if reason, safe := app.SafeMode(); safe {
		app.Stop()
		t.Fatalf("fixture boot is SAFE: %s", reason)
	}
	if app.anchorer == nil {
		app.Stop()
		t.Fatal("no anchorer with a witness configured")
	}
	if err := app.anchorer.CheckAndAnchor(); err != nil {
		app.Stop()
		t.Fatalf("anchor: %v", err)
	}
	last, sealed := app.ledger.LastSeq(), app.ledger.SealedSeq()
	app.Stop()
	if sealed != last {
		t.Fatalf("sealed through %d, last record %d — the head should have sealed everything through itself", sealed, last)
	}
	return last
}

func TestASealedRecordBootsUnderThePersistedWitnessKey(t *testing.T) {
	f := newSealedFixture(t)
	head := f.bootAndAnchor(t)

	keys, err := witness.LoadWitnessKeys(f.dir, f.platform.Env)
	if err != nil {
		t.Fatal(err)
	}
	if keys[f.witness.KeyID] == nil {
		t.Fatal("THE WITNESS KEY WAS NOT PERSISTED BESIDE THE LEDGER")
	}
	segment := filepath.Join(f.dir, "segment-1-"+itoa(head)+".jsonl.gz")
	if _, err := os.Stat(segment); err != nil {
		t.Fatalf("no segment through the head: %v", err)
	}

	// .
	// .
	// .
	app := New(f.cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("second boot: %v", err)
	}
	defer app.Stop()
	if reason, safe := app.SafeMode(); safe {
		t.Fatalf("A SEALED RECORD BOOTED SAFE: %s", reason)
	}
	if app.ledger.LastSeq() != head || app.ledger.SealedSeq() != head {
		t.Fatalf("reopened at %d (sealed %d), want %d", app.ledger.LastSeq(), app.ledger.SealedSeq(), head)
	}
	ordinal, raw, err := app.store.LastWitnessReceipt()
	if err != nil {
		t.Fatal(err)
	}
	if ordinal != int64(head)-1 || len(raw) == 0 {
		t.Fatalf("the projection rebuilt from the containers holds receipt ordinal %d (%d bytes), want %d", ordinal, len(raw), head-1)
	}
	appendFixtureEvent(t, app, f.keyPath, "exp_after_seal")
	if app.ledger.LastSeq() != head+1 {
		t.Fatalf("append after the seal landed at %d", app.ledger.LastSeq())
	}
}

func TestASealedRecordWithoutItsWitnessKeyBootsSafe(t *testing.T) {
	f := newSealedFixture(t)
	f.bootAndAnchor(t)
	if err := os.RemoveAll(witness.WitnessKeysDir(f.dir)); err != nil {
		t.Fatal(err)
	}
	app := New(f.cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("boot must come up SAFE, not die: %v", err)
	}
	defer app.Stop()
	reason, safe := app.SafeMode()
	if !safe || !strings.Contains(reason, "witness key") {
		t.Fatalf("a sealed segment with no key to verify its head must boot SAFE naming the key, got safe=%v %q", safe, reason)
	}
}

func TestASnapshotOfASealedRecordIsWholeAndVerifiesOnItsOwn(t *testing.T) {
	f := newSealedFixture(t)
	head := f.bootAndAnchor(t)

	app := New(f.cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	defer app.Stop()
	cfg := app.configSnapshot()
	app.runMaintenance()
	snaps := backupFiles(t, app.backupsDir(cfg))
	if len(snaps) != 1 {
		t.Fatalf("%d snapshots, want 1 (%v)", len(snaps), snaps)
	}
	snap := filepath.Join(app.backupsDir(cfg), snaps[0])
	for _, name := range []string{"segment-1-" + itoa(head) + ".jsonl.gz", "ledger.jsonl", "SHA256SUMS", filepath.Join("witness-keys", f.witness.KeyID+".json")} {
		if _, err := os.Stat(filepath.Join(snap, name)); err != nil {
			t.Fatalf("the snapshot lacks %s", name)
		}
	}
	// .
	// .
	heads, err := witness.LoadHeadVerifier(snap, f.platform.Env)
	if err != nil {
		t.Fatal(err)
	}
	n, _, err := genesis.VerifySelfContainedWith(filepath.Join(snap, "ledger.jsonl"), heads)
	if err != nil || uint64(n) != head || heads.Verified() != 1 {
		t.Fatalf("snapshot verification: n=%d verified=%d err=%v", n, heads.Verified(), err)
	}
	// .
	st, err := store.New(filepath.Join(t.TempDir(), "restore.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.ReplayFromFile(filepath.Join(snap, "ledger.jsonl")); err != nil {
		t.Fatalf("THE SNAPSHOT DOES NOT RESTORE: %v", err)
	}
	ordinal, _, err := st.LastWitnessReceipt()
	if err != nil {
		t.Fatal(err)
	}
	if ordinal != int64(head)-1 {
		t.Fatalf("restored projection holds receipt ordinal %d, want %d", ordinal, head-1)
	}
}

func itoa(n uint64) string {
	var b [20]byte
	i := len(b)
	for {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
		if n == 0 {
			break
		}
	}
	return string(b[i:])
}
