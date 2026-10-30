package witness

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
)

// keptBeside writes canonical beside a ledger in a new directory, as
// enrolment keeps it, and returns the directory.
func keptBeside(t *testing.T, key IdentityKey, canonical []byte) string {
	t.Helper()
	dir := t.TempDir()
	if err := writeEnvelopeFile(dir, key, canonical); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestALostTableTakesTheEnvelopeFromTheFileWithNoSearch(t *testing.T) {
	kp, _ := crypto.GenerateKeyPair()
	key := AsIdentityKey(kp)
	original, did := enrolled(t, key, time.Date(2026, 8, 20, 13, 32, 14, 0, time.UTC))
	dir := keptBeside(t, key, original)
	lost := &memEnvelopeStore{enrolledIDs: []string{did}} // no genesis time: nothing to search

	got, env, err := EnsureIdentityEnvelope(key, lost, dir)
	if err != nil {
		t.Fatalf("THE ENVELOPE BESIDE THE LEDGER WAS NOT TAKEN; the table's loss was searched for: %v", err)
	}
	if id, _ := DeriveIdentityID(got, env); !bytes.Equal(got, original) || id != did {
		t.Fatalf("the envelope taken is not the one beside the ledger (it names %s, the record %s)", id, did)
	}
	if !bytes.Equal(lost.saved, original) || lost.saves != 1 {
		t.Fatalf("the store was not given the envelope beside the ledger as its cache (saves=%d)", lost.saves)
	}

	// Control: with the file gone too, the same store has nothing but the
	// record to go on, which gives no window here.
	if err := os.Remove(EnvelopePath(dir)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := EnsureIdentityEnvelope(key, &memEnvelopeStore{enrolledIDs: []string{did}}, dir); !errors.Is(err, ErrEnvelopeUnrecoverable) {
		t.Fatalf("rig: without the file the record alone must not give the envelope here: %v", err)
	}
}

func TestAFileTheRecordContradictsIsRewrittenFromTheRecord(t *testing.T) {
	kp, _ := crypto.GenerateKeyPair()
	key := AsIdentityKey(kp)
	born := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	recorded, did := enrolled(t, key, born.Add(26*time.Hour))
	beside, _ := enrolled(t, key, born.Add(9*24*time.Hour))
	dir := keptBeside(t, key, beside)
	store := &memEnvelopeStore{saved: recorded, enrolledIDs: []string{did}, born: born, witness: born.Add(26*time.Hour + time.Minute)}

	if kept, err := KeepEnvelopeBeside(Unguarded{}, key, store, dir); kept || err != nil {
		t.Fatalf("the start's step touched a file that stands: kept=%v %v", kept, err)
	}
	got, _, err := EnsureIdentityEnvelope(key, store, dir)
	if err != nil || !bytes.Equal(got, recorded) {
		t.Fatalf("THE ENVELOPE PRESENTED IS NOT THE ONE THE RECORD NAMES: %v", err)
	}
	if file, _ := os.ReadFile(EnvelopePath(dir)); !bytes.Equal(file, recorded) {
		t.Fatal("A FILE THE RECORD CONTRADICTS WAS NOT REWRITTEN FROM THE RECORD")
	}
	if !bytes.Equal(store.saved, recorded) {
		t.Fatal("the row was moved off the record's envelope")
	}

	// With the row lost as well, the record still decides: rebuilt from it.
	if err := os.WriteFile(EnvelopePath(dir), beside, 0o600); err != nil {
		t.Fatal(err)
	}
	lost := &memEnvelopeStore{enrolledIDs: []string{did}, born: born, witness: born.Add(26*time.Hour + time.Minute)}
	if got, _, err := EnsureIdentityEnvelope(key, lost, dir); err != nil || !bytes.Equal(got, recorded) {
		t.Fatalf("with the row lost, the file the record contradicts was taken over the record: %v", err)
	}
	if file, _ := os.ReadFile(EnvelopePath(dir)); !bytes.Equal(file, recorded) {
		t.Fatal("the file was not rewritten with the envelope rebuilt from the record")
	}
}

// AN ENROLLED IDENTITY'S ENVELOPE IS KEPT BESIDE THE LEDGER AT THE FIRST
// START THAT KEEPS IT, from the store, checked against the record: once,
// and not again. One the record does not name is not kept, and is said.
func TestTheFirstStartKeepsAnEnrolledIdentitysEnvelopeBesideTheLedger(t *testing.T) {
	kp, _ := crypto.GenerateKeyPair()
	key := AsIdentityKey(kp)
	born := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	original, did := enrolled(t, key, born.Add(time.Hour))
	dir := t.TempDir()
	store := &memEnvelopeStore{saved: original, enrolledIDs: []string{did}, born: born, witness: born.Add(2 * time.Hour)}

	kept, err := KeepEnvelopeBeside(Unguarded{}, key, store, dir)
	if err != nil || !kept {
		t.Fatalf("THE ENROLLED IDENTITY'S ENVELOPE WAS NOT KEPT BESIDE THE LEDGER: kept=%v %v", kept, err)
	}
	if got, err := os.ReadFile(EnvelopePath(dir)); err != nil || !bytes.Equal(got, original) {
		t.Fatalf("the file is not the store's envelope, byte for byte: %v", err)
	}
	if kept, err := KeepEnvelopeBeside(Unguarded{}, key, store, dir); kept || err != nil {
		t.Fatalf("the second start kept it again: kept=%v %v", kept, err)
	}

	t.Run("a row the record does not name", func(t *testing.T) {
		stranger, _ := enrolled(t, key, born.Add(9*24*time.Hour))
		dir := t.TempDir()
		kept, err := KeepEnvelopeBeside(Unguarded{}, key, &memEnvelopeStore{saved: stranger, enrolledIDs: []string{did}, born: born}, dir)
		if kept || !errors.Is(err, ErrEnvelopeRowDisagrees) {
			t.Fatalf("a row that names another identity than the record was not refused: kept=%v %v", kept, err)
		}
		if _, err := os.Stat(EnvelopePath(dir)); !os.IsNotExist(err) {
			t.Fatal("A ROW THE RECORD DOES NOT NAME WAS KEPT BESIDE THE LEDGER")
		}
	})
	t.Run("another key's row", func(t *testing.T) {
		other, _ := crypto.GenerateKeyPair()
		foreign, foreignDID := enrolled(t, AsIdentityKey(other), born.Add(time.Hour))
		dir := t.TempDir()
		kept, err := KeepEnvelopeBeside(Unguarded{}, key, &memEnvelopeStore{saved: foreign, enrolledIDs: []string{foreignDID}, born: born}, dir)
		if kept || !errors.Is(err, ErrEnvelopeRowDisagrees) {
			t.Fatalf("another key's envelope was not refused: kept=%v %v", kept, err)
		}
		if _, err := os.Stat(EnvelopePath(dir)); !os.IsNotExist(err) {
			t.Fatal("ANOTHER KEY'S ENVELOPE WAS KEPT BESIDE THE LEDGER")
		}
	})
	t.Run("a record that names none", func(t *testing.T) {
		dir := t.TempDir()
		if kept, err := KeepEnvelopeBeside(Unguarded{}, key, &memEnvelopeStore{saved: original}, dir); kept || err != nil {
			t.Fatalf("an envelope was kept for an identity the record names no enrolment of: kept=%v %v", kept, err)
		}
	})
}

// A FILE THAT DOES NOT READ, OR IS NOT THIS KEY'S, IS NEVER TAKEN FOR
// NONE. Taken for none while the record names no enrolment, the next step
// mints, and a minted envelope is a newcomer at the witness: refused, the
// file left as it is. Where the record names the identity, it is put right
// from the record, as the row is.
func TestAnEnvelopeFileThatDoesNotReadIsNeverTakenForNone(t *testing.T) {
	kp, _ := crypto.GenerateKeyPair()
	key := AsIdentityKey(kp)
	other, _ := crypto.GenerateKeyPair()
	born := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	foreign, _ := enrolled(t, AsIdentityKey(other), born)
	recorded, did := enrolled(t, key, born.Add(time.Hour))
	for name, body := range map[string][]byte{
		"torn":        []byte(`{"v":1,"kind":`),
		"another key": foreign,
	} {
		t.Run(name+", the record names none", func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(EnvelopePath(dir), body, 0o600); err != nil {
				t.Fatal(err)
			}
			store := &memEnvelopeStore{}
			if _, _, err := EnsureIdentityEnvelope(key, store, dir); err == nil || store.saves != 0 {
				t.Fatalf("A FILE THAT IS NOT THIS IDENTITY'S ENVELOPE WAS TAKEN FOR NONE (err=%v, saves=%d)", err, store.saves)
			}
			if got, _ := os.ReadFile(EnvelopePath(dir)); !bytes.Equal(got, body) {
				t.Fatal("the file was written over with nothing to put it right from")
			}
		})
		t.Run(name+", the record names the identity", func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(EnvelopePath(dir), body, 0o600); err != nil {
				t.Fatal(err)
			}
			store := &memEnvelopeStore{saved: recorded, enrolledIDs: []string{did}, born: born, witness: born.Add(2 * time.Hour)}
			if got, _, err := EnsureIdentityEnvelope(key, store, dir); err != nil || !bytes.Equal(got, recorded) {
				t.Fatalf("the record's envelope was not taken over a file that is not this identity's: %v", err)
			}
			if got, _ := os.ReadFile(EnvelopePath(dir)); !bytes.Equal(got, recorded) {
				t.Fatal("the file was not put right from the record")
			}
		})
	}
}

// THE FILE OUTRANKS ITS CACHE. While the record names no enrolment, the
// file is the envelope the witness was shown (it is written before it is
// shown); a row that differs from it is replaced by it.
func TestTheFileOutranksTheRowItCaches(t *testing.T) {
	kp, _ := crypto.GenerateKeyPair()
	key := AsIdentityKey(kp)
	beside, _ := enrolled(t, key, time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC))
	row, _ := enrolled(t, key, time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC))
	dir := keptBeside(t, key, beside)
	store := &memEnvelopeStore{saved: row}
	got, _, err := EnsureIdentityEnvelope(key, store, dir)
	if err != nil || !bytes.Equal(got, beside) {
		t.Fatalf("the row was taken over the file beside the ledger: %v", err)
	}
	if !bytes.Equal(store.saved, beside) || store.replaces != 1 {
		t.Fatalf("the row was not replaced by the file (replaces=%d)", store.replaces)
	}
}

// THE ENVELOPE IS KEPT BESIDE THE LEDGER AT ENROLMENT, BEFORE THE WITNESS
// SEES IT. The first bookmark carries a new envelope; while the witness
// answers it, the file already stands beside the ledger and names the
// identity the bookmark names, so a database lost at any moment after can
// never mint a second one.
func TestTheEnvelopeIsKeptBesideTheLedgerBeforeTheWitnessSeesIt(t *testing.T) {
	fw := newFakeWitness(t)
	lg, kp := testLedger(t, 3)
	dir := filepath.Dir(lg.Path())
	var during []string
	fw.slowly(func() {
		raw, err := os.ReadFile(EnvelopePath(dir))
		if err != nil {
			during = append(during, "none: "+err.Error())
			return
		}
		_, env, err := parseIdentityEnvelope(raw)
		if err != nil {
			during = append(during, "unreadable: "+err.Error())
			return
		}
		id, _ := DeriveIdentityID(raw, env)
		during = append(during, id)
	})
	a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, testMinter{lg, kp}, 3, "")
	if err := a.CheckAndAnchor(); err != nil {
		t.Fatal(err)
	}
	if len(during) != 1 || fw.sawIdentityIDs[during[0]] != 1 {
		t.Fatalf("WHILE THE WITNESS ANSWERED THE FIRST BOOKMARK, THE ENVELOPE IT CARRIED WAS NOT BESIDE THE LEDGER: %q (the witness saw %v)", during, fw.sawIdentityIDs)
	}
}
