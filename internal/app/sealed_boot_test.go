package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/witness"
	"github.com/aiii-dot-id/aii-os/internal/witness/witnesstest"
)

func TestRuntimeWitnessRefusalPreservesHealthyRestart(t *testing.T) {
	f := newSealedFixture(t)
	// The same fake platform that the fixture explicitly trusts by file
	// cannot make itself trusted merely by becoming the download response.
	f.cfg.Witness.PlatformPubkeyPath = ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(f.platform.Env)
	}))
	t.Cleanup(server.Close)
	f.cfg.Genesis.ServerURL = server.URL
	app := New(f.cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApp(t, app) })
	seq, hash := app.ledger.LastSeq(), app.ledger.LastHash()
	if err := app.anchorer.CheckAndAnchor(); !errors.Is(err, witness.ErrManifestUntrusted) {
		t.Fatalf("untrusted manifest must refuse before creating history: %v", err)
	}
	if app.ledger.LastSeq() != seq || app.ledger.LastHash() != hash {
		t.Fatal("a rejected manifest changed the identity's history")
	}
	if reason, safe := app.SafeMode(); safe {
		t.Fatalf("a rejected server signer is not a broken identity: %s", reason)
	}
	appendFixtureEvent(t, app, f.keyPath, "life_after_refused_manifest")
	last := app.ledger.LastSeq()
	stopApp(t, app)
	// Reopen through the real boot verifier, not an alternate test reader.
	restarted := New(f.cfg)
	if err := startLiveForTest(restarted); err != nil {
		t.Fatalf("restart after manifest refusal: %v", err)
	}
	t.Cleanup(func() { stopApp(t, restarted) })
	if reason, safe := restarted.SafeMode(); safe {
		t.Fatalf("runtime left a healthy identity unable to boot normally: %s", reason)
	}
	if restarted.ledger.LastSeq() != last {
		t.Fatalf("restart lost accepted work: seq=%d want %d", restarted.ledger.LastSeq(), last)
	}
	appendFixtureEvent(t, restarted, f.keyPath, "life_after_restart")
}

// sealed_boot_test.go — the whole thing, end to end: an identity anchors
// with a witness the platform vouches for, the key is persisted beside
// the ledger, the head seals the record into a segment, the next boot
// verifies the segment under the persisted key and runs; without the
// key the same boot is SAFE; and a maintenance snapshot of a sealed
// ledger is whole, restores, and verifies on its own.

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

// bootAndAnchor boots the identity, runs one anchor pass and stops it,
// leaving a sealed record on disk.
func (f *sealedFixture) bootAndAnchor(t *testing.T) uint64 {
	t.Helper()
	app := New(f.cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("boot: %v", err)
	}
	if reason, safe := app.SafeMode(); safe {
		stopApp(t, app)
		t.Fatalf("fixture boot is SAFE: %s", reason)
	}
	if app.anchorer == nil {
		stopApp(t, app)
		t.Fatal("no anchorer with a witness configured")
	}
	if err := app.anchorer.CheckAndAnchor(); err != nil {
		stopApp(t, app)
		t.Fatalf("anchor: %v", err)
	}
	last, sealed := app.ledger.LastSeq(), app.ledger.SealedSeq()
	stopApp(t, app)
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

	// The next boot verifies the segment under the persisted key and
	// runs; the projection it rebuilds carries the receipt; the ledger
	// keeps appending onto the empty tail.
	app := New(f.cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatalf("second boot: %v", err)
	}
	defer stopApp(t, app)
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
	defer stopApp(t, app)
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
	defer stopApp(t, app)
	cfg := app.configSnapshot()
	maintenancePass(t, app)
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
	// On its own: the keys in the snapshot, the pinned root replaced by
	// the platform that signed them, no running identity.
	heads, err := witness.LoadHeadVerifier(snap, f.platform.Env)
	if err != nil {
		t.Fatal(err)
	}
	n, _, err := genesis.VerifySelfContainedWith(filepath.Join(snap, "ledger.jsonl"), heads)
	if err != nil || uint64(n) != head || heads.Verified() != 1 {
		t.Fatalf("snapshot verification: n=%d verified=%d err=%v", n, heads.Verified(), err)
	}
	// And it restores: replay reads the segment beside the tail copy.
	st, err := store.NewForTest(filepath.Join(t.TempDir(), "restore.db"))
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

// refusingMinter is a system.witnessed mint that does not land.
type refusingMinter struct{}

func (refusingMinter) MintWitnessed(witness.WitnessReceipt, string) (*ledger.Event, error) {
	return nil, errors.New("the mint did not land")
}

func TestBootMintsTheWitnessReceiptKeptBesideTheLedger(t *testing.T) {
	f := newSealedFixture(t)
	app := New(f.cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	// One anchor pass over this identity's own record and store whose
	// mint does not land.
	unminted := witness.NewAnchorer(witness.New(f.witness.URL(), ""), app.ledger, witness.AsIdentityKey(app.keyPair),
		app.store, app.store, refusingMinter{}, 1, f.cfg.Witness.PlatformPubkeyPath)
	if err := unminted.CheckAndAnchor(); err == nil {
		stopApp(t, app)
		t.Fatal("fixture: the mint landed")
	}
	attested := app.ledger.LastSeq()
	stopApp(t, app)
	if ordinal, ids := f.witness.Holds(); ordinal != int64(attested) || ids != 1 {
		t.Fatalf("fixture: the witness holds record %d under %d identities, want %d under one", ordinal, ids, attested)
	}
	if _, err := os.Stat(witness.PendingReceiptPath(f.dir)); err != nil {
		t.Fatalf("the answered receipt is not beside the ledger: %v", err)
	}

	restarted := New(f.cfg)
	if err := startLiveForTest(restarted); err != nil {
		t.Fatalf("boot: %v", err)
	}
	if reason, safe := restarted.SafeMode(); safe {
		stopApp(t, restarted)
		t.Fatalf("boot is SAFE: %s", reason)
	}
	ordinal, _, err := restarted.store.LastWitnessReceipt()
	if err != nil || ordinal != int64(attested) {
		stopApp(t, restarted)
		t.Fatalf("BOOT DID NOT MINT THE RECEIPT KEPT BESIDE THE LEDGER: the record's last receipt attests %d, want %d (%v)", ordinal, attested, err)
	}
	if _, err := os.Stat(witness.PendingReceiptPath(f.dir)); !errors.Is(err, os.ErrNotExist) {
		stopApp(t, restarted)
		t.Fatalf("the receipt the record carries is still beside it: %v", err)
	}
	appendFixtureEvent(t, restarted, f.keyPath, "exp_after_the_settled_receipt")
	if err := restarted.anchorer.CheckAndAnchor(); err != nil {
		stopApp(t, restarted)
		t.Fatalf("the next anchor: %v", err)
	}
	last, sealed := restarted.ledger.LastSeq(), restarted.ledger.SealedSeq()
	stopApp(t, restarted)
	if sealed != last {
		t.Fatalf("sealed through %d, last record %d — the next head should have sealed everything through itself", sealed, last)
	}
	if _, ids := f.witness.Holds(); ids != 1 {
		t.Fatalf("the witness holds %d identities for one ledger", ids)
	}

	again := New(f.cfg)
	if err := startLiveForTest(again); err != nil {
		t.Fatalf("the boot after: %v", err)
	}
	defer stopApp(t, again)
	if reason, safe := again.SafeMode(); safe {
		t.Fatalf("THE RECORD DOES NOT BOOT WITH THE SETTLED RECEIPT IN IT: %s", reason)
	}
	if again.ledger.LastSeq() != last {
		t.Fatalf("reopened at %d, want %d", again.ledger.LastSeq(), last)
	}
}

func TestRecordsLandingWhileTheWitnessAnswersBootLive(t *testing.T) {
	f := newSealedFixture(t)
	app := New(f.cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	ids := []string{"exp_while_the_witness_answers_1", "exp_while_the_witness_answers_2"}
	landed := make(chan error, len(ids))
	f.witness.Slowly(func() {
		for _, id := range ids {
			_, err := app.door.Append(ledger.EventExperienceCreate, 3, map[string]any{
				"id": id, "content": "observed " + id, "category": "observation", "provenance": "self",
			}, "")
			landed <- err
		}
	})
	bookmarked := app.ledger.LastSeq()
	err := app.anchorer.CheckAndAnchor()
	f.witness.Slowly(nil)
	last, sealed := app.ledger.LastSeq(), app.ledger.SealedSeq()
	stopApp(t, app)
	if err != nil {
		t.Fatalf("anchor: %v", err)
	}
	for range ids {
		if err := <-landed; err != nil {
			t.Fatal(err)
		}
	}
	if ordinal, _ := f.witness.Holds(); ordinal != int64(bookmarked) || last != bookmarked+3 {
		t.Fatalf("fixture: the witness holds record %d, the record ends at %d; want %d, then two records and the head", ordinal, last, bookmarked)
	}

	restarted := New(f.cfg)
	if err := startLiveForTest(restarted); err != nil {
		t.Fatalf("boot: %v", err)
	}
	if reason, safe := restarted.SafeMode(); safe {
		stopApp(t, restarted)
		t.Fatalf("THE RECORD BOOTS SAFE AFTER RECORDS LANDED WHILE THE WITNESS ANSWERED: %s", reason)
	}
	if sealed == last {
		stopApp(t, restarted)
		t.Fatalf("A SEGMENT CLOSED UNDER THE LATE HEAD (record %d)", last)
	}
	appendFixtureEvent(t, restarted, f.keyPath, "exp_after_the_late_head")
	if err := restarted.anchorer.CheckAndAnchor(); err != nil {
		stopApp(t, restarted)
		t.Fatalf("the next anchor: %v", err)
	}
	last, sealed = restarted.ledger.LastSeq(), restarted.ledger.SealedSeq()
	stopApp(t, restarted)
	if sealed != last {
		t.Fatalf("sealed through %d, last record %d — the head on time should have sealed over the late one", sealed, last)
	}

	again := New(f.cfg)
	if err := startLiveForTest(again); err != nil {
		t.Fatalf("the boot after: %v", err)
	}
	defer stopApp(t, again)
	if reason, safe := again.SafeMode(); safe {
		t.Fatalf("THE SEGMENT SEALED OVER A LATE HEAD BOOTS SAFE: %s", reason)
	}
	if again.ledger.LastSeq() != last || again.ledger.SealedSeq() != last {
		t.Fatalf("reopened at %d (sealed %d), want %d", again.ledger.LastSeq(), again.ledger.SealedSeq(), last)
	}
}

func TestBootMintsAKeptReceiptLateAfterTheRecordMovedOn(t *testing.T) {
	f := newSealedFixture(t)
	app := New(f.cfg)
	if err := startLiveForTest(app); err != nil {
		t.Fatal(err)
	}
	unminted := witness.NewAnchorer(witness.New(f.witness.URL(), ""), app.ledger, witness.AsIdentityKey(app.keyPair),
		app.store, app.store, refusingMinter{}, 1, f.cfg.Witness.PlatformPubkeyPath)
	if err := unminted.CheckAndAnchor(); err == nil {
		stopApp(t, app)
		t.Fatal("fixture: the mint landed")
	}
	attested := app.ledger.LastSeq()
	appendFixtureEvent(t, app, f.keyPath, "exp_after_the_unminted_receipt_1")
	appendFixtureEvent(t, app, f.keyPath, "exp_after_the_unminted_receipt_2")
	stopApp(t, app)

	restarted := New(f.cfg)
	if err := startLiveForTest(restarted); err != nil {
		t.Fatalf("boot: %v", err)
	}
	if reason, safe := restarted.SafeMode(); safe {
		stopApp(t, restarted)
		t.Fatalf("boot is SAFE: %s", reason)
	}
	ordinal, _, err := restarted.store.LastWitnessReceipt()
	if ends := restarted.ledger.LastSeq(); err != nil || ordinal != int64(attested) || ends != attested+3 {
		stopApp(t, restarted)
		t.Fatalf("BOOT DID NOT MINT THE KEPT RECEIPT LATE: the record's last receipt attests %d (want %d), the record ends at %d (want %d) (%v)",
			ordinal, attested, ends, attested+3, err)
	}
	if _, err := os.Stat(witness.PendingReceiptPath(f.dir)); !errors.Is(err, os.ErrNotExist) {
		stopApp(t, restarted)
		t.Fatalf("the receipt the record carries is still beside it: %v", err)
	}
	appendFixtureEvent(t, restarted, f.keyPath, "exp_after_the_late_receipt")
	if err := restarted.anchorer.CheckAndAnchor(); err != nil {
		stopApp(t, restarted)
		t.Fatalf("the next anchor: %v", err)
	}
	last, sealed := restarted.ledger.LastSeq(), restarted.ledger.SealedSeq()
	stopApp(t, restarted)
	if sealed != last {
		t.Fatalf("sealed through %d, last record %d — the head on time should have sealed over the late one", sealed, last)
	}
	if _, ids := f.witness.Holds(); ids != 1 {
		t.Fatalf("the witness holds %d identities for one ledger", ids)
	}

	again := New(f.cfg)
	if err := startLiveForTest(again); err != nil {
		t.Fatalf("the boot after: %v", err)
	}
	defer stopApp(t, again)
	if reason, safe := again.SafeMode(); safe {
		t.Fatalf("THE RECORD DOES NOT BOOT WITH THE LATE HEAD IN IT: %s", reason)
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
