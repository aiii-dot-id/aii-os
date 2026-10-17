package continuity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/escrow"
	"github.com/aiii-dot-id/aii-os/internal/fileperm"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// snapshot_encrypt_test.go — a snapshot is encrypted only when losing the
// machine would not lose it, and what is published is what was verified.

// hostSnapshotKey puts a snapshot key beside the identity key, as a normal
// boot does, and returns it with its recipient.
func hostSnapshotKey(t *testing.T, a *testApp) (key []byte, recipient string) {
	t.Helper()
	cfg := a.configSnapshot()
	a.continuity.EnsureSnapshotKey(cfg)
	key, err := os.ReadFile(a.continuity.snapshotKeyPath(cfg))
	if err != nil {
		t.Fatalf("the boot made no snapshot key: %v", err)
	}
	recipient, err = escrow.SnapshotRecipient(key)
	if err != nil {
		t.Fatal(err)
	}
	return key, recipient
}

// checkedEscrow writes the receipt `aii escrow check` would: signed by kp,
// naming recipient.
func checkedEscrow(t *testing.T, a *testApp, kp *crypto.KeyPair, recipient string) {
	t.Helper()
	r, err := escrow.SignReceipt(kp, recipient, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(r)
	if err := os.WriteFile(a.continuity.escrowReceiptPath(a.configSnapshot()), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func snapshotsIn(t *testing.T, dir string) (plain, sealed []string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		switch {
		case strings.HasPrefix(e.Name(), "."):
			t.Fatalf("debris in the snapshot set: %s", e.Name())
		case strings.HasSuffix(e.Name(), escrow.SnapshotSuffix):
			sealed = append(sealed, e.Name())
		case backupSeqRe.MatchString(e.Name()):
			plain = append(plain, e.Name())
		}
	}
	return plain, sealed
}

// ESCROW BEFORE ENCRYPT, ENFORCED — AND A FALLBACK, NOT A REFUSAL. Without
// proof that an escrow of THIS key was checked for THIS identity, the pass
// still backs up in plaintext. Missing escrow is passive status, while
// unusable keys or receipts still alert the operator.
func TestWithoutAVerifiedEscrowReceiptASnapshotIsPlaintextAndFaultsAreReported(t *testing.T) {
	stranger, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	for name, prepare := range map[string]func(t *testing.T, a *testApp){
		"no snapshot key at all": func(*testing.T, *testApp) {},
		"a key and no receipt":   func(t *testing.T, a *testApp) { hostSnapshotKey(t, a) },
		"a receipt for a key since rotated": func(t *testing.T, a *testApp) {
			_, old := hostSnapshotKey(t, a)
			checkedEscrow(t, a, a.door.kp, old)
			os.Remove(a.continuity.snapshotKeyPath(a.configSnapshot()))
			hostSnapshotKey(t, a) // a new key: the receipt names the old recipient
		},
		"a receipt signed by another identity": func(t *testing.T, a *testApp) {
			_, recipient := hostSnapshotKey(t, a)
			checkedEscrow(t, a, stranger, recipient)
		},
		"a receipt that does not read": func(t *testing.T, a *testApp) {
			hostSnapshotKey(t, a)
			os.WriteFile(a.continuity.escrowReceiptPath(a.configSnapshot()), []byte("{not json"), 0o600)
		},
		"a receipt path that cannot be read": func(t *testing.T, a *testApp) {
			hostSnapshotKey(t, a)
			// A directory fails ReadFile even when tests run as root;
			// chmod would not establish a portable permission failure.
			if err := os.Mkdir(a.continuity.escrowReceiptPath(a.configSnapshot()), 0o700); err != nil {
				t.Fatal(err)
			}
		},
		"an unusable snapshot key": func(t *testing.T, a *testApp) {
			hostSnapshotKey(t, a)
			if err := os.WriteFile(a.continuity.snapshotKeyPath(a.configSnapshot()), []byte("not a key"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			a, _, _ := maintApp(t)
			prepare(t, a)
			dir := a.backupsDir(a.configSnapshot())
			a.continuity.runMaintenance(t.Context())
			plain, sealed := snapshotsIn(t, dir)
			if len(sealed) != 0 {
				t.Fatalf("A SNAPSHOT WAS ENCRYPTED WITH NO VERIFIED ESCROW OF ITS KEY: %v", sealed)
			}
			if len(plain) != 1 {
				t.Fatalf("the fallback is a plaintext snapshot, not no snapshot: %v", plain)
			}
			wantAlert := name != "a key and no receipt"
			if got := operatorWasTold(t, a, "NOT encrypted"); got != wantAlert {
				t.Fatalf("unencrypted alert = %v, want %v", got, wantAlert)
			}
			st, ok, err := a.store.ContinuityStatus()
			if err != nil || !ok || st.Outcome != store.ContinuityOK || st.Encrypted || st.Unencrypted == "" {
				t.Fatalf("plaintext backup status: %+v, %v, %v", st, ok, err)
			}
			if name == "a receipt path that cannot be read" && st.Unencrypted != store.UnencryptedReceiptUnread {
				t.Fatalf("a receipt read error was not distinguished from absence: %+v", st)
			}
		})
	}
}

// Repeated passes and reopening the same test identity do not turn an
// unchecked escrow into a task. Status remains truthful without a notice,
// an error-level log, a receipt being fabricated, or either key changing.
func TestUncheckedEscrowStaysQuietAcrossBackupsAndRestart(t *testing.T) {
	a, _, keyPath := maintApp(t)
	key, _ := hostSnapshotKey(t, a)
	identityKey, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	log := logsink.CaptureForTest(t)
	now := time.Now()
	for pass, id := range []string{"exp_before_restart", "exp_after_restart", "exp_repeated"} {
		if pass == 1 {
			if err := a.store.Close(); err != nil {
				t.Fatal(err)
			}
			if err := a.ledger.Close(); err != nil {
				t.Fatal(err)
			}
			a.store, err = store.NewForTest(a.cfg.Identity.DBPath)
			if err != nil {
				t.Fatal(err)
			}
			a.ledger, err = ledger.New(a.cfg.Identity.LedgerPath)
			if err != nil {
				t.Fatal(err)
			}
			a.door.st, a.door.lg = a.store, a.ledger
			a.continuity = New(a.wires())
			a.continuity.EnsureSnapshotKey(a.configSnapshot())
		}
		// Distinct signed records give distinct snapshot names without sleeps.
		appendFixtureEvent(t, a, keyPath, id)
		made, err := a.continuity.takeOnDemand(t.Context(), false, now)
		if err != nil || made.Encrypted || made.Unencrypted != store.UnencryptedNoEscrow {
			t.Fatalf("pass %d: %+v, %v", pass, made, err)
		}
		if notices, err := a.store.UndeliveredFor("operator"); err != nil || len(notices) != 0 {
			t.Fatalf("pass %d generated unsolicited notices: %+v, %v", pass, notices, err)
		}
		view := a.continuity.continuityView()
		if view.Encrypting || view.Unencrypted != store.UnencryptedNoEscrow || view.LastPass == nil || view.LastPass.Outcome != store.ContinuityOK {
			t.Fatalf("pass %d hid plaintext backup status: %+v", pass, view)
		}
		read, err := a.continuity.continuityRead("", now)
		if err != nil || !strings.Contains(read, "encryption: off — no checked recovery copy of the snapshot key is recorded") {
			t.Fatalf("requested status: %q, %v", read, err)
		}
		if !strings.Contains(read, "escrow of your keys: none checked\n") {
			t.Fatalf("requested escrow status is not neutral: %q", read)
		}
	}
	if log.Contains("snapshot-unencrypted") || log.Contains("maintenance.error") {
		t.Fatalf("successful unencrypted backups generated warning logs: %s", log.String())
	}
	for path, want := range map[string][]byte{keyPath: identityKey, a.continuity.snapshotKeyPath(a.cfg): key} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != string(want) {
			t.Fatalf("a backup changed its key at %s: %v", path, err)
		}
	}
	if _, err := os.Stat(a.continuity.escrowReceiptPath(a.cfg)); !os.IsNotExist(err) {
		t.Fatalf("a backup fabricated an escrow receipt: %v", err)
	}
}

// WITH A VERIFIED RECEIPT the snapshot is one encrypted file, nothing of
// it is published in the clear, and it is the proved set: opened, it holds
// the database at the captured record and passes its own checksum list.
func TestWithAVerifiedEscrowReceiptTheSnapshotIsEncryptedAndIsTheProvedSet(t *testing.T) {
	a, _, _ := maintApp(t)
	live(t, a, 3)
	key, recipient := hostSnapshotKey(t, a)
	checkedEscrow(t, a, a.door.kp, recipient)
	dir := a.backupsDir(a.configSnapshot())
	captured := a.ledger.LastSeq()

	a.continuity.runMaintenance(t.Context())
	plain, sealed := snapshotsIn(t, dir)
	if len(plain) != 0 || len(sealed) != 1 {
		t.Fatalf("want one encrypted snapshot and nothing in the clear: plain %v sealed %v", plain, sealed)
	}
	if snapshotSeq(t, sealed[0]) != captured {
		t.Fatalf("named for record %d, captured at %d", snapshotSeq(t, sealed[0]), captured)
	}
	if operatorWasTold(t, a, "NOT encrypted") {
		t.Fatal("an encrypted snapshot raised the unencrypted alert")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, sealed[0]))
	if strings.Contains(string(raw), "a turn the record never holds") {
		t.Fatal("A CONVERSATION IS IN THE ENCRYPTED SNAPSHOT IN THE CLEAR")
	}
	out := filepath.Join(t.TempDir(), "opened")
	names, err := escrow.OpenSnapshot(filepath.Join(dir, sealed[0]), key, out)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ledger.jsonl", SnapshotDB, snapshotReceiptName, SnapshotSums} {
		if !strings.Contains(","+strings.Join(names, ",")+",", ","+want+",") {
			t.Fatalf("the encrypted snapshot lacks %s: %v", want, names)
		}
	}
	head, held, err := store.InspectCopy(t.Context(), filepath.Join(out, SnapshotDB))
	if err != nil || head.Seq != captured || held.Ephemeral["conversations"] != 3 {
		t.Fatalf("the database inside is not the captured one: head %d, %+v, %v", head.Seq, held, err)
	}
	// And the receipt gate is asked every pass, not once.
	os.Remove(a.continuity.escrowReceiptPath(a.configSnapshot()))
	nextSecond()
	a.continuity.runMaintenance(t.Context())
	if plain, sealed := snapshotsIn(t, dir); len(plain) != 1 || len(sealed) != 1 {
		t.Fatalf("with the receipt gone the next pass must fall back: plain %v sealed %v", plain, sealed)
	}
}

// THE CIPHERTEXT IS VERIFIED BEFORE IT IS PUBLISHED. The proofs ran on the
// plaintext; what is kept is the ciphertext. One flipped bit between the
// two — a bad disk, a bad day — and the pass publishes nothing, says so,
// and leaves the older snapshot standing.
func TestACiphertextThatIsNotTheProvedSetIsNeverPublished(t *testing.T) {
	a, _, _ := maintApp(t)
	_, recipient := hostSnapshotKey(t, a)
	checkedEscrow(t, a, a.door.kp, recipient)
	dir := a.backupsDir(a.configSnapshot())
	a.continuity.runMaintenance(t.Context())
	_, before := snapshotsIn(t, dir)
	if len(before) != 1 {
		t.Fatalf("fixture: %v", before)
	}

	t.Cleanup(func() { a.continuity.SnapshotStep = nil })
	a.continuity.SnapshotStep = func(step string) {
		path, ok := strings.CutPrefix(step, "encrypted:")
		if !ok {
			return
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("hook: %v", err)
			return
		}
		raw[len(raw)/2] ^= 0x01
		os.WriteFile(path, raw, 0o600)
	}
	nextSecond()
	a.continuity.runMaintenance(t.Context())
	a.continuity.SnapshotStep = nil

	plain, after := snapshotsIn(t, dir)
	if len(plain) != 0 || len(after) != 1 || after[0] != before[0] {
		t.Fatalf("A CIPHERTEXT THAT DOES NOT VERIFY WAS PUBLISHED, or the good snapshot is gone: plain %v sealed %v", plain, after)
	}
	if !operatorWasTold(t, a, "does not verify") {
		t.Fatal("the operator was not told")
	}
	if !operatorWasTold(t, a, "[maintenance] backup:") {
		t.Fatal("ciphertext failure did not produce the genuine backup-failure notice")
	}
	if st, ok, err := a.store.ContinuityStatus(); err != nil || !ok || st.Outcome != store.ContinuityFailed {
		t.Fatalf("ciphertext failure was not recorded as failed: %+v, %v, %v", st, ok, err)
	}
}

// ONE ORDERED SET. A plaintext directory and an encrypted file stand side
// by side while the old ones age out; pruning orders them by record and
// then by stamp, whichever kind each is, and keeps the newest.
func TestPruningTreatsPlaintextAndEncryptedSnapshotsAsOneSet(t *testing.T) {
	dir := t.TempDir()
	mkPlain := func(name string) {
		os.MkdirAll(filepath.Join(dir, name), 0o700)
		os.WriteFile(filepath.Join(dir, name, SnapshotSums), []byte("x  y\n"), 0o600)
	}
	mkPlain("ledger-20260917T040000Z-seq7")
	mkPlain("ledger-20260918T040000Z-seq7")
	os.WriteFile(filepath.Join(dir, "ledger-20260919T040000Z-seq7"+escrow.SnapshotSuffix), []byte("c"), 0o600)
	os.WriteFile(filepath.Join(dir, "ledger-20260916T040000Z-seq7"+escrow.SnapshotSuffix), []byte("c"), 0o600)
	os.MkdirAll(filepath.Join(dir, "ledger-20260920T040000Z-seq7"), 0o700) // a directory with no checksum list: debris

	if removed := pruneBackups(dir, 2); removed != 2 {
		t.Fatalf("pruned %d, want 2", removed)
	}
	var left []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		left = append(left, e.Name())
	}
	want := "ledger-20260918T040000Z-seq7,ledger-20260919T040000Z-seq7" + escrow.SnapshotSuffix
	if strings.Join(left, ",") != want {
		t.Fatalf("kept %v, want the two newest by stamp whatever their kind: %s", left, want)
	}
}

// THE KEY IS MADE ONCE, at a normal boot, owner-only; an existing key is
// never touched; and SAFE makes no files.
func TestTheSnapshotKeyIsMadeOnceAndNeverInSafe(t *testing.T) {
	a, _, _ := maintApp(t)
	cfg := a.configSnapshot()
	path := a.continuity.snapshotKeyPath(cfg)
	if filepath.Dir(path) != filepath.Dir(cfg.Identity.KeyPath) {
		t.Fatalf("the snapshot key is not beside the identity key: %s", path)
	}
	a.continuity.EnsureSnapshotKey(cfg)
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := fileperm.IsRestrictedToOwner(path); err != nil || !ok {
		t.Fatalf("the snapshot key is readable by others: %v", err)
	}
	a.continuity.EnsureSnapshotKey(cfg)
	if again, _ := os.ReadFile(path); string(again) != string(first) {
		t.Fatal("AN EXISTING SNAPSHOT KEY WAS REPLACED: every snapshot encrypted to it is now unreadable")
	}

	safe, _, _ := maintApp(t)
	safe.enterSafe("test: no new files in SAFE")
	safe.continuity.EnsureSnapshotKey(safe.configSnapshot())
	if _, err := os.Stat(safe.continuity.snapshotKeyPath(safe.configSnapshot())); !os.IsNotExist(err) {
		t.Fatal("SAFE made a snapshot key")
	}
}

// SNAPSHOTS THAT WERE ENCRYPTED AND ARE NOT ANY MORE ARE SAID, ONCE. A pass
// with no escrow receipt publishes in the clear and raises nothing, which
// is right for an identity that never had a checked escrow. It was the same
// quiet for one whose receipt was lost: months of encrypted snapshots, then
// every one in the clear, into a folder a sync job may ship, with no
// notice. A kept encrypted snapshot is the evidence that this identity was
// encrypting, and the operator is told once for it.
func TestSnapshotsThatStopBeingEncryptedAreSaidOnce(t *testing.T) {
	a, _, _ := maintApp(t)
	_, recipient := hostSnapshotKey(t, a)
	checkedEscrow(t, a, a.door.kp, recipient)
	dir := a.backupsDir(a.configSnapshot())
	a.continuity.runMaintenance(t.Context())
	if _, sealed := snapshotsIn(t, dir); len(sealed) != 1 {
		t.Fatalf("rig: the first pass was to encrypt: %v", sealed)
	}
	const said = "were encrypted and are not any more"
	if operatorWasTold(t, a, said) {
		t.Fatal("the operator was told snapshots stopped being encrypted while they were")
	}

	// The receipt is lost, as on a machine it never travelled to.
	if err := os.Remove(a.continuity.escrowReceiptPath(a.configSnapshot())); err != nil {
		t.Fatal(err)
	}
	told := func() int {
		msgs, err := a.store.UndeliveredFor("operator")
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, m := range msgs {
			if strings.Contains(m.Content, said) {
				n++
			}
		}
		return n
	}
	for pass := 1; pass <= 2; pass++ {
		a.continuity.runMaintenance(t.Context())
		if plain, _ := snapshotsIn(t, dir); len(plain) == 0 {
			t.Fatalf("pass %d: rig: the pass without a receipt was to publish in the clear", pass)
		}
		if n := told(); n != 1 {
			t.Fatalf("pass %d: THE OPERATOR WAS TOLD %d TIMES THAT SNAPSHOTS STOPPED BEING ENCRYPTED, want once", pass, n)
		}
	}
}

// ONE LOSS, ONE NOTICE, WHEN THE NEWEST ENCRYPTED SNAPSHOT LEAVES. The
// notice was keyed by the newest encrypted snapshot kept; when that one left
// the folder while an older encrypted one stayed, the older one became the
// key and the same loss was told a second time. Encrypting again and losing
// it again is another loss, and is told.
func TestALossOfEncryptionIsToldOnceWhenTheNewestEncryptedSnapshotLeaves(t *testing.T) {
	a, _, _ := maintApp(t)
	_, recipient := hostSnapshotKey(t, a)
	checkedEscrow(t, a, a.door.kp, recipient)
	dir := a.backupsDir(a.configSnapshot())
	pass := func() {
		t.Helper()
		time.Sleep(1100 * time.Millisecond) // a snapshot's name carries its second
		a.continuity.runMaintenance(t.Context())
	}
	pass()
	pass()
	if _, sealed := snapshotsIn(t, dir); len(sealed) != 2 {
		t.Fatalf("rig: two encrypted snapshots were to be kept: %v", sealed)
	}
	const said = "were encrypted and are not any more"
	told := func() int {
		msgs, err := a.store.UndeliveredFor("operator")
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, m := range msgs {
			if strings.Contains(m.Content, said) {
				n++
			}
		}
		return n
	}
	receipt := a.continuity.escrowReceiptPath(a.configSnapshot())
	if err := os.Remove(receipt); err != nil {
		t.Fatal(err)
	}
	pass()
	if n := told(); n != 1 {
		t.Fatalf("rig: the loss is told once: %d", n)
	}
	// The newest encrypted snapshot leaves; the older one stays.
	_, sealed := snapshotsIn(t, dir)
	sort.Strings(sealed)
	if err := os.Remove(filepath.Join(dir, sealed[len(sealed)-1])); err != nil {
		t.Fatal(err)
	}
	pass()
	if n := told(); n != 1 {
		t.Fatalf("THE SAME LOSS WAS TOLD %d TIMES once the newest encrypted snapshot left the folder, want once", n)
	}
	// Encrypting again, and losing it again: another loss, told.
	checkedEscrow(t, a, a.door.kp, recipient)
	pass()
	if err := os.Remove(receipt); err != nil {
		t.Fatal(err)
	}
	pass()
	if n := told(); n != 2 {
		t.Fatalf("a second loss after encrypting again was told %d time(s) in all, want 2", n)
	}
}
