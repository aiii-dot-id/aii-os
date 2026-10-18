package witness

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// failingMinter is a mint that does not land: the witness has answered,
// the record holds nothing.
type failingMinter struct{}

func (failingMinter) MintWitnessed(WitnessReceipt, string) (*ledger.Event, error) {
	return nil, errors.New("the mint did not land")
}

// recordStore answers the store's two witness reads as the store does —
// from the record — and keeps the envelope's row, which is what a lost
// database loses. A new one over the same ledger is aii.db lost.
type recordStore struct {
	lg   *ledger.Ledger
	born time.Time
	row  []byte
}

func (s *recordStore) SaveWitnessEnvelope(j []byte) error {
	if s.row == nil {
		s.row = j
	}
	return nil
}
func (s *recordStore) ReplaceWitnessEnvelope(j []byte) error { s.row = j; return nil }
func (s *recordStore) LoadWitnessEnvelope() ([]byte, error)  { return s.row, nil }

// heads is the record's system.witnessed records: the raw receipt object
// of each, with its time.
func (s *recordStore) heads() (receipts []json.RawMessage, times []string, err error) {
	events, err := ledger.ReadAll(s.lg.Path())
	if err != nil {
		return nil, nil, err
	}
	for _, e := range events {
		if e.Type != ledger.EventSystemWitnessed {
			continue
		}
		var p struct {
			Receipt json.RawMessage `json:"receipt"`
		}
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			return nil, nil, err
		}
		receipts, times = append(receipts, p.Receipt), append(times, e.Timestamp)
	}
	return receipts, times, nil
}

func (s *recordStore) WitnessEnrollment() ([]string, time.Time, time.Time, error) {
	receipts, times, err := s.heads()
	if err != nil {
		return nil, time.Time{}, time.Time{}, err
	}
	var ids []string
	var first time.Time
	seen := map[string]bool{}
	for i, raw := range receipts {
		var r WitnessReceipt
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, time.Time{}, time.Time{}, err
		}
		if i == 0 {
			if first, err = time.Parse(time.RFC3339Nano, times[0]); err != nil {
				return nil, time.Time{}, time.Time{}, err
			}
		}
		if !seen[r.IdentityID] {
			seen[r.IdentityID] = true
			ids = append(ids, r.IdentityID)
		}
	}
	return ids, s.born, first, nil
}

func (s *recordStore) LastWitnessReceipt() (int64, []byte, error) {
	receipts, _, err := s.heads()
	if err != nil || len(receipts) == 0 {
		return 0, nil, err
	}
	last := receipts[len(receipts)-1]
	var r WitnessReceipt
	if err := json.Unmarshal(last, &r); err != nil {
		return 0, nil, err
	}
	return r.LedgerOrdinal, last, nil
}

func recordHeads(t *testing.T, lg *ledger.Ledger) []WitnessReceipt {
	t.Helper()
	raw, _, err := (&recordStore{lg: lg}).heads()
	if err != nil {
		t.Fatal(err)
	}
	out := make([]WitnessReceipt, len(raw))
	for i := range raw {
		if err := json.Unmarshal(raw[i], &out[i]); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func appendRecords(t *testing.T, lg *ledger.Ledger, kp *crypto.KeyPair, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := lg.Append(ledger.EventExperienceCreate, 3, map[string]string{"n": "later"}, kp); err != nil {
			t.Fatal(err)
		}
	}
}

// verifiesAsBootDoes holds the record to the rule boot holds it to: the
// chain, and every head continuing the receipt chain from (-1, the zero
// hash) under one identity.
func verifiesAsBootDoes(t *testing.T, lg *ledger.Ledger, kp *crypto.KeyPair) {
	t.Helper()
	if _, err := ledger.VerifyChain(lg.Path(), kp.PublicKeyBytes(), NewHeadVerifier(nil)); err != nil {
		t.Fatalf("THE RECORD DOES NOT VERIFY AS BOOT VERIFIES IT: %v", err)
	}
}

func witnessHolds(fw *fakeWitness) (identities, askedAbout int) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	return len(fw.state), len(fw.sawIdentityIDs)
}

func pendingOnDisk(t *testing.T, dir string) bool {
	t.Helper()
	_, err := os.Stat(PendingReceiptPath(dir))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

func TestALostDatabaseAfterAnUnmintedFirstReceiptKeepsOneIdentity(t *testing.T) {
	fw := newFakeWitness(t)
	lg, kp := testLedger(t, 3)
	key := AsIdentityKey(kp)
	born := time.Now().Add(-time.Minute)
	db := &recordStore{lg: lg, born: born}
	if err := NewAnchorer(New(fw.server.URL, ""), lg, key, db, db, failingMinter{}, 1, "").CheckAndAnchor(); err == nil {
		t.Fatal("fixture: the mint landed")
	}
	original := db.row
	time.Sleep(1100 * time.Millisecond) // a later second: a fresh envelope would name another identity

	lost := &recordStore{lg: lg, born: born} // no row, and the record names nothing
	if err := NewAnchorer(New(fw.server.URL, ""), lg, key, lost, lost, testMinter{lg, kp}, 1, "").CheckAndAnchor(); err != nil {
		t.Fatalf("the pass after the lost database: %v", err)
	}
	if ids, asked := witnessHolds(fw); ids != 1 || asked != 1 {
		t.Fatalf("THE WITNESS HOLDS %d IDENTITIES (asked about %d) FOR ONE LEDGER", ids, asked)
	}
	heads := recordHeads(t, lg)
	if len(heads) != 2 || heads[0].LedgerOrdinal != 3 || heads[1].PreviousWitnessedLedgerOrdinal != 3 {
		t.Fatalf("want the kept receipt as the first head and the next one chained from it, got %+v", heads)
	}
	if !bytes.Equal(lost.row, original) {
		t.Fatal("the envelope is not the one the witness knows")
	}
	verifiesAsBootDoes(t, lg, kp)
	if pendingOnDisk(t, filepath.Dir(lg.Path())) {
		t.Fatal("a receipt the record carries is still kept beside it")
	}
}

// While the record names no identity, the receipt pending beside the
// ledger does: the envelope is rebuilt to it and never minted anew. Here
// the record has moved on, so the receipt cannot be minted and the anchor
// pass refuses — but the certificate and relay paths still ask for the
// envelope, and the row they would mint is what forked the identity.
func TestAReceiptPendingBesideTheLedgerNamesTheEnvelope(t *testing.T) {
	fw := newFakeWitness(t)
	lg, kp := testLedger(t, 3)
	key := AsIdentityKey(kp)
	dir := filepath.Dir(lg.Path())
	born := time.Now().Add(-time.Minute)
	db := &recordStore{lg: lg, born: born}
	if err := NewAnchorer(New(fw.server.URL, ""), lg, key, db, db, failingMinter{}, 1, "").CheckAndAnchor(); err == nil {
		t.Fatal("fixture: the mint landed")
	}
	original := db.row
	_, did := enrolledFromRow(t, original)
	appendRecords(t, lg, kp, 1)
	time.Sleep(1100 * time.Millisecond)

	t.Run("rebuilt to the receipt's identity", func(t *testing.T) {
		lost := &recordStore{lg: lg, born: born}
		got, _, err := EnsureIdentityEnvelope(key, lost, dir)
		if err != nil {
			t.Fatalf("the receipt beside the ledger names %s; its envelope must be rebuilt: %v", did, err)
		}
		if !bytes.Equal(got, original) || !bytes.Equal(lost.row, original) {
			t.Fatal("A NEW ENVELOPE WAS MINTED WHILE A RECEIPT BESIDE THE LEDGER NAMES THE IDENTITY")
		}
	})
	t.Run("a row that names another identity is replaced", func(t *testing.T) {
		stranger, _ := enrolled(t, key, time.Now().Add(-time.Hour))
		lost := &recordStore{lg: lg, born: born, row: stranger}
		if got, _, err := EnsureIdentityEnvelope(key, lost, dir); err != nil || !bytes.Equal(got, original) {
			t.Fatalf("the row contradicting the receipt must give way to it: %v", err)
		}
	})
	t.Run("outside the window the record allows: refused, nothing saved", func(t *testing.T) {
		late := &recordStore{lg: lg, born: time.Now().Add(72 * time.Hour)} // a window that cannot hold the envelope
		_, _, err := EnsureIdentityEnvelope(key, late, dir)
		if !errors.Is(err, ErrEnvelopeUnrecoverable) || !strings.Contains(err.Error(), did) || late.row != nil {
			t.Fatalf("want ErrEnvelopeUnrecoverable naming %s and nothing saved, got %v (row %v)", did, err, late.row != nil)
		}
	})
	t.Run("the record names another identity: the fork, refused", func(t *testing.T) {
		_, other := enrolled(t, key, born)
		named := &memEnvelopeStore{enrolledIDs: []string{other}, born: born, witness: time.Now()}
		if _, _, err := EnsureIdentityEnvelope(key, named, dir); !errors.Is(err, ErrEnrollmentForked) || named.saves != 0 {
			t.Fatalf("want ErrEnrollmentForked and nothing saved, got %v", err)
		}
	})
	t.Run("a file that does not read: refused, never taken for none", func(t *testing.T) {
		torn := t.TempDir()
		if err := os.WriteFile(PendingReceiptPath(torn), []byte(`{"identity_id":`), 0o600); err != nil {
			t.Fatal(err)
		}
		lost := &memEnvelopeStore{}
		if _, _, err := EnsureIdentityEnvelope(key, lost, torn); err == nil || lost.saves != 0 {
			t.Fatalf("an unreadable receipt beside the ledger must refuse, not mint (err=%v, saves=%d)", err, lost.saves)
		}
	})
}

// enrolledFromRow parses a stored envelope and gives its DID.
func enrolledFromRow(t *testing.T, row []byte) (*PublicKeyEnvelope, string) {
	t.Helper()
	canonical, env, err := parseIdentityEnvelope(row)
	if err != nil {
		t.Fatal(err)
	}
	did, err := DeriveIdentityID(canonical, env)
	if err != nil {
		t.Fatal(err)
	}
	return env, did
}

func TestAFailedMintIsMintedAtTheNextBootAndTheNextAnchorChains(t *testing.T) {
	fw := newFakeWitness(t)
	lg, kp := testLedger(t, 3)
	key := AsIdentityKey(kp)
	db := &recordStore{lg: lg, born: time.Now().Add(-time.Minute)}
	if err := NewAnchorer(New(fw.server.URL, ""), lg, key, db, db, failingMinter{}, 1, "").CheckAndAnchor(); err == nil {
		t.Fatal("fixture: the mint landed")
	}

	boot := NewAnchorer(New(fw.server.URL, ""), lg, key, db, db, testMinter{lg, kp}, 1, "")
	if err := boot.SettlePending(); err != nil {
		t.Fatalf("boot must mint the kept receipt: %v", err)
	}
	if heads := recordHeads(t, lg); lg.LastSeq() != 4 || len(heads) != 1 || heads[0].LedgerOrdinal != 3 {
		t.Fatalf("boot did not mint the kept receipt as the head after record 3 (last %d, heads %+v)", lg.LastSeq(), heads)
	}
	if boot.LastAnchoredSeq() != 3 || pendingOnDisk(t, filepath.Dir(lg.Path())) {
		t.Fatalf("anchored at %d, kept copy present %v", boot.LastAnchoredSeq(), pendingOnDisk(t, filepath.Dir(lg.Path())))
	}

	appendRecords(t, lg, kp, 2)
	if err := boot.CheckAndAnchor(); err != nil {
		t.Fatalf("the next anchor: %v", err)
	}
	heads := recordHeads(t, lg)
	if len(heads) != 2 || heads[1].PreviousWitnessedLedgerOrdinal != 3 || heads[1].PreviousWitnessedLedgerHash != heads[0].LedgerHash {
		t.Fatalf("the next head does not chain from the kept one: %+v", heads)
	}
	verifiesAsBootDoes(t, lg, kp)
}

// observingMinter looks beside the ledger at the moment it is asked to mint.
type observingMinter struct {
	inner EventMinter
	dir   string
	saw   *pendingReceipt
	err   error
}

func (m *observingMinter) MintWitnessed(r WitnessReceipt, keyID string) (*ledger.Event, error) {
	m.saw, m.err = readPendingReceipt(m.dir)
	return m.inner.MintWitnessed(r, keyID)
}

// A normal anchor: the receipt is beside the ledger when the mint is asked
// for, and gone once the record carries it.
func TestANormalAnchorKeepsTheReceiptOnlyUntilTheRecordCarriesIt(t *testing.T) {
	fw := newFakeWitness(t)
	lg, kp := testLedger(t, 3)
	dir := filepath.Dir(lg.Path())
	m := &observingMinter{inner: testMinter{lg, kp}, dir: dir}
	a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, m, 1, "")
	for pass := 1; pass <= 2; pass++ {
		if pass == 2 {
			appendRecords(t, lg, kp, 1)
		}
		m.saw = nil
		if err := a.CheckAndAnchor(); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		if m.err != nil || m.saw == nil || uint64(m.saw.LedgerOrdinal) != a.LastAnchoredSeq() {
			t.Fatalf("pass %d: THE RECEIPT WAS NOT BESIDE THE LEDGER WHEN ITS MINT WAS ASKED FOR (saw %+v, err %v)", pass, m.saw, m.err)
		}
		if pendingOnDisk(t, dir) {
			t.Fatalf("pass %d: a normal anchor left its receipt beside the ledger", pass)
		}
	}
}

// crashMinter ends the process inside the mint: before it appends, or
// after the head is in the record and before the kept copy is retired.
type crashMinter struct {
	lg    *ledger.Ledger
	kp    *crypto.KeyPair
	after bool
}

func (m crashMinter) MintWitnessed(r WitnessReceipt, keyID string) (*ledger.Event, error) {
	if m.after {
		if _, err := (testMinter{m.lg, m.kp}).MintWitnessed(r, keyID); err != nil {
			os.Exit(3)
		}
	}
	os.Exit(7)
	return nil, nil
}

// A process that dies inside the mint leaves the receipt beside the
// ledger, and the next boot settles it: minted when the record does not
// carry it, retired when it does — once, either way, and the record goes
// on anchoring.
func TestACrashInsideTheMintIsSettledAtTheNextBoot(t *testing.T) {
	for _, at := range []string{"before-the-append", "after-the-append"} {
		t.Run(at, func(t *testing.T) {
			fw := newFakeWitness(t)
			lg, kp := testLedger(t, 3)
			path, dir := lg.Path(), filepath.Dir(lg.Path())
			keyPath := filepath.Join(t.TempDir(), "identity.key")
			if _, err := crypto.SaveKeyPair(kp, keyPath); err != nil {
				t.Fatal(err)
			}
			lg.Close()

			child := exec.Command(os.Args[0], "-test.run=^TestPendingCrashChild$", "-test.count=1")
			child.Env = append(os.Environ(), "AII_WITNESS_CRASH_LEDGER="+path, "AII_WITNESS_CRASH_KEY="+keyPath,
				"AII_WITNESS_CRASH_URL="+fw.server.URL, "AII_WITNESS_CRASH_AT="+at)
			out, err := child.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 7 {
				t.Fatalf("the child did not die inside the mint (err=%v):\n%s", err, out)
			}
			if p, err := readPendingReceipt(dir); err != nil || p == nil || p.LedgerOrdinal != 3 {
				t.Fatalf("THE RECEIPT WAS NOT KEPT BESIDE THE LEDGER BEFORE THE MINT (got %+v, %v)", p, err)
			}

			lg2, err := ledger.New(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { lg2.Close() })
			want := uint64(3)
			if at == "after-the-append" {
				want = 4
			}
			if lg2.LastSeq() != want {
				t.Fatalf("fixture: the child left %d records, want %d", lg2.LastSeq(), want)
			}
			db := &recordStore{lg: lg2, born: time.Now().Add(-time.Hour)}
			boot := NewAnchorer(New(fw.server.URL, ""), lg2, AsIdentityKey(kp), db, db, testMinter{lg2, kp}, 1, "")
			if err := boot.SettlePending(); err != nil {
				t.Fatalf("boot after the crash: %v", err)
			}
			if heads := recordHeads(t, lg2); lg2.LastSeq() != 4 || len(heads) != 1 || pendingOnDisk(t, dir) {
				t.Fatalf("after boot: last %d, %d heads, kept copy present %v — want one head at 4 and no copy", lg2.LastSeq(), len(heads), pendingOnDisk(t, dir))
			}

			appendRecords(t, lg2, kp, 2)
			if err := boot.CheckAndAnchor(); err != nil {
				t.Fatalf("the next anchor: %v", err)
			}
			if heads := recordHeads(t, lg2); len(heads) != 2 || heads[1].PreviousWitnessedLedgerOrdinal != 3 {
				t.Fatalf("the next head does not chain from the settled one: %+v", heads)
			}
			if ids, _ := witnessHolds(fw); ids != 1 {
				t.Fatalf("the witness holds %d identities", ids)
			}
			verifiesAsBootDoes(t, lg2, kp)
		})
	}
}

// TestPendingCrashChild is the crash test's child: it anchors the ledger
// named in the environment and dies inside the mint. Skipped when run
// directly.
func TestPendingCrashChild(t *testing.T) {
	path := os.Getenv("AII_WITNESS_CRASH_LEDGER")
	if path == "" {
		t.Skip("child of TestACrashInsideTheMintIsSettledAtTheNextBoot")
	}
	kp, err := crypto.LoadKeyPair(os.Getenv("AII_WITNESS_CRASH_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	lg, err := ledger.New(path)
	if err != nil {
		t.Fatal(err)
	}
	db := &recordStore{lg: lg, born: time.Now().Add(-time.Hour)}
	minter := crashMinter{lg: lg, kp: kp, after: os.Getenv("AII_WITNESS_CRASH_AT") == "after-the-append"}
	err = NewAnchorer(New(os.Getenv("AII_WITNESS_CRASH_URL"), ""), lg, AsIdentityKey(kp), db, db, minter, 1, "").CheckAndAnchor()
	t.Fatalf("the child outlived its mint: %v", err)
}

func TestAPendingReceiptThatCannotStandAsTheNextHeadIsRefused(t *testing.T) {
	type fixture struct {
		fw       *fakeWitness
		lg       *ledger.Ledger
		kp       *crypto.KeyPair
		db       *recordStore
		platform string
	}
	// witnessed is a three-record ledger after one anchor pass; mint says
	// whether its mint lands, and vouched whether the witness's key is
	// persisted beside the ledger under a platform (the rules before the
	// key's are asked whether it is held or not).
	witnessed := func(t *testing.T, mint, vouched bool) fixture {
		t.Helper()
		f := fixture{fw: newFakeWitness(t)}
		if vouched {
			p := synthPlatform(t)
			f.platform = p.writeEnv(t)
			f.fw.manifest = buildManifest(t, p, f.fw.witnessEnv, false)
		}
		f.lg, f.kp = testLedger(t, 3)
		f.db = &recordStore{lg: f.lg, born: time.Now().Add(-time.Minute)}
		var minter EventMinter = failingMinter{}
		if mint {
			minter = testMinter{f.lg, f.kp}
		}
		err := NewAnchorer(New(f.fw.server.URL, ""), f.lg, AsIdentityKey(f.kp), f.db, f.db, minter, 1, f.platform).CheckAndAnchor()
		if (err == nil) != mint {
			t.Fatalf("fixture: mint=%v, anchor: %v", mint, err)
		}
		return f
	}
	// keepFor writes beside the ledger a receipt the witness signed for id
	// attesting (ordinal, hash), continuing from (prevOrdinal, prevHash);
	// keep, one for the record's tail.
	keepFor := func(t *testing.T, f fixture, id string, ordinal int64, hash string, prevOrdinal int64, prevHash string) {
		t.Helper()
		r := f.fw.signReceipt(t, WitnessRequest{IdentityID: id, LedgerOrdinal: ordinal, LedgerHash: hash}, prevOrdinal, prevHash)
		if err := writePendingReceipt(filepath.Dir(f.lg.Path()), r, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	keep := func(t *testing.T, f fixture, id string, prevOrdinal int64, prevHash string) {
		t.Helper()
		keepFor(t, f, id, int64(f.lg.LastSeq()), f.lg.LastHash(), prevOrdinal, prevHash)
	}
	refused := func(t *testing.T, f fixture, why string) {
		t.Helper()
		dir := filepath.Dir(f.lg.Path())
		before, _ := os.ReadFile(PendingReceiptPath(dir))
		seq := f.lg.LastSeq()
		_, asked := witnessHolds(f.fw)
		f.fw.mu.Lock()
		bookmarks := f.fw.sawIdentityIDs[onlyKey(f.fw)]
		f.fw.mu.Unlock()
		a := NewAnchorer(New(f.fw.server.URL, ""), f.lg, AsIdentityKey(f.kp), f.db, f.db, testMinter{f.lg, f.kp}, 1, f.platform)
		if err := a.SettlePending(); !errors.Is(err, ErrPendingReceiptRefused) || !strings.Contains(err.Error(), why) {
			t.Fatalf("want ErrPendingReceiptRefused saying %q, got %v", why, err)
		}
		if err := a.CheckAndAnchor(); !errors.Is(err, ErrPendingReceiptRefused) {
			t.Fatalf("the anchor pass must refuse while the receipt is unsettled, got %v", err)
		}
		after, _ := os.ReadFile(PendingReceiptPath(dir))
		_, askedAfter := witnessHolds(f.fw)
		f.fw.mu.Lock()
		bookmarksAfter := f.fw.sawIdentityIDs[onlyKey(f.fw)]
		f.fw.mu.Unlock()
		if f.lg.LastSeq() != seq || len(after) == 0 || !bytes.Equal(before, after) || askedAfter != asked || bookmarksAfter != bookmarks || a.IntegrityConflict() != nil {
			t.Fatalf("a refused receipt changed something: record %d→%d, file kept unchanged %v, witness asked %d→%d (%d→%d bookmarks), latched %v",
				seq, f.lg.LastSeq(), len(after) > 0 && bytes.Equal(before, after), asked, askedAfter, bookmarks, bookmarksAfter, a.IntegrityConflict() != nil)
		}
		verifiesAsBootDoes(t, f.lg, f.kp)
	}

	t.Run("the record it attests is not the one the witness saw", func(t *testing.T) {
		f := witnessed(t, true, false)
		appendRecords(t, f.lg, f.kp, 2)
		last := recordHeads(t, f.lg)[0]
		events, err := ledger.ReadAll(f.lg.Path())
		if err != nil {
			t.Fatal(err)
		}
		// Record 5 named by record 4's hash, as a restore that replaced
		// record 5 would leave it.
		keepFor(t, f, last.IdentityID, 5, events[3].EntryHash(), last.LedgerOrdinal, last.LedgerHash)
		refused(t, f, "receipt attests hash "+events[3].EntryHash()+" for record 5, which is "+events[4].EntryHash())
	})
	t.Run("it attests the record the record's last head attested", func(t *testing.T) {
		f := witnessed(t, true, false)
		appendRecords(t, f.lg, f.kp, 1)
		last := recordHeads(t, f.lg)[0]
		keepFor(t, f, last.IdentityID, last.LedgerOrdinal, last.LedgerHash, last.LedgerOrdinal, last.LedgerHash)
		refused(t, f, "neither the record before the head nor a record after the one the previous head attested (3)")
	})
	t.Run("it does not continue the record's receipt chain", func(t *testing.T) {
		f := witnessed(t, true, false)
		appendRecords(t, f.lg, f.kp, 1)
		last := recordHeads(t, f.lg)[0]
		keep(t, f, last.IdentityID, -1, ZeroLedgerHash) // a first receipt again, as from a witness that forgot
		refused(t, f, "receipt continues from (-1")
	})
	t.Run("it names another identity than the record's", func(t *testing.T) {
		f := witnessed(t, true, false)
		appendRecords(t, f.lg, f.kp, 1)
		last := recordHeads(t, f.lg)[0]
		_, other := enrolled(t, AsIdentityKey(f.kp), time.Now().Add(-time.Hour))
		keep(t, f, other, last.LedgerOrdinal, last.LedgerHash)
		refused(t, f, "receipt names identity "+other)
	})
	t.Run("its witness signature does not verify under the persisted key", func(t *testing.T) {
		f := witnessed(t, false, true)
		dir := filepath.Dir(f.lg.Path())
		p, err := readPendingReceipt(dir)
		if err != nil || p == nil {
			t.Fatalf("fixture: %v", err)
		}
		p.Receipt.WitnessedAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339) // not the moment it was signed over
		if err := writePendingReceipt(dir, p.Receipt, time.Now()); err != nil {
			t.Fatal(err)
		}
		refused(t, f, "witness signature under "+f.fw.witnessEnv.KeyID)
	})
	t.Run("the file does not read", func(t *testing.T) {
		f := witnessed(t, false, false)
		if err := os.WriteFile(PendingReceiptPath(filepath.Dir(f.lg.Path())), []byte(`{"identity_id":"x"`), 0o600); err != nil {
			t.Fatal(err)
		}
		refused(t, f, "corrupt")
	})
}
