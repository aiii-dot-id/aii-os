package witness

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// rewrap_sealed_test.go — a re-wrap of a record a real anchor pass sealed,
// held to what stands beside it as a boot holds it (Beside: the heads
// under the persisted keys, the witness tail), and what a boot's own
// verifier then says of the result. The ledger package's tests re-wrap
// sealed records under a stub head verifier; here the receipts, the key
// and the tail are the ones the anchorer made.

// sealedByAnAnchor writes an identity's record — a genesis naming the
// key, then four records — anchors it once under a platform-vouched
// witness, which mints the head as record 6 and seals through it, appends
// two more records to the tail, and closes it. Beside it stand the
// witness key with the platform's manifest and the witness tail.
func sealedByAnAnchor(t *testing.T) (path string, kp *crypto.KeyPair, p *platformKeys) {
	t.Helper()
	p = synthPlatform(t)
	fw := newFakeWitness(t)
	fw.manifest = buildManifest(t, p, fw.witnessEnv, false)
	path = filepath.Join(t.TempDir(), "ledger.jsonl")
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	lg, err := ledger.New(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lg.Append(ledger.EventRing0Genesis, 0, map[string]any{
		"fingerprint": kp.Fingerprint(), "public_key": kp.PublicKeyB64(),
	}, kp); err != nil {
		t.Fatal(err)
	}
	more := func(n int) {
		for i := 0; i < n; i++ {
			if _, err := lg.Append(ledger.EventExperienceCreate, 3, map[string]any{"id": "e", "content": "x"}, kp); err != nil {
				t.Fatal(err)
			}
		}
	}
	more(4)
	a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, testMinter{lg, kp}, 5, p.writeEnv(t))
	a.SetSealer(lg)
	if err := a.CheckAndAnchor(); err != nil {
		lg.Close()
		t.Fatalf("anchor: %v", err)
	}
	if lg.SealedSeq() != 6 {
		lg.Close()
		t.Fatalf("fixture: sealed through %d, want 6 (five records and the head)", lg.SealedSeq())
	}
	more(2)
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}
	return path, kp, p
}

func TestARewrapOfASealedRecordIsHeldToWhatStandsBesideItAndVerifiesAfterwards(t *testing.T) {
	path, kp, p := sealedByAnAnchor(t)
	dir := filepath.Dir(path)
	accept := func(string) error { return nil }

	beside, err := LoadBeside(dir, p.env)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "ledger.jsonl")
	done, err := ledger.RewrapWith(path, kp, out, accept, ledger.RewrapChecks{Heads: beside, Visit: beside.Visit, Held: beside.Held})
	if err != nil {
		t.Fatalf("re-wrap of a sealed, witnessed record: %v", err)
	}
	if done.Records != 8 || done.Sealed != 6 || done.Footnoted != 1 {
		t.Fatalf("the re-wrap says %+v; want 8 records, 6 of them sealed, the one receipt footnoted", done)
	}
	// The source was verified as a boot verifies it: the head under the
	// persisted key, and the chain holding the record the tail names.
	if beside.Heads.Verified() != 1 || beside.Heads.Unverified() != 0 {
		t.Fatalf("the source's head: verified %d, unverified %d", beside.Heads.Verified(), beside.Heads.Unverified())
	}
	if err := beside.Held(); err != nil {
		t.Fatalf("the source does not hold its witness tail: %v", err)
	}

	// What a boot of the result verifies, under the same keys: every
	// record under its own proof, the receipt a footnote and no head.
	hv, err := LoadHeadVerifier(dir, p.env)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := ledger.VerifyChain(out, kp.PublicKeyBytes(), hv); err != nil || n != 8 {
		t.Fatalf("the result: n=%d err=%v", n, err)
	}
	if hv.Footnotes() != 1 || hv.Verified() != 0 {
		t.Fatalf("the result carries %d footnotes and %d heads; the witness's receipt attests nothing now", hv.Footnotes(), hv.Verified())
	}
	if n, err := ledger.VerifyChain(out, kp.PublicKeyBytes(), nil); err != nil || n != 8 {
		t.Fatalf("the result with no witness key at all: n=%d err=%v", n, err)
	}
}

// A RECORD THE WITNESS TAIL SAYS WAS CUT SHORT IS NOT RE-SIGNED AS WHOLE.
// The chain verifies — a shorter chain always does — and a boot refuses
// it on the tail beside it. A re-wrap that did not ask would sign the
// shorter record as the identity's history and retire the tail that said
// otherwise.
func TestARewrapRefusesARecordItsWitnessTailSaysWasCutShort(t *testing.T) {
	path, kp, p := sealedByAnAnchor(t)
	dir := filepath.Dir(path)
	tail, err := ReadLocalTail(dir)
	if err != nil || tail == nil {
		t.Fatalf("fixture: the anchor left no witness tail: %v", err)
	}
	// The witness attested a record beyond this chain's end.
	tail.LedgerOrdinal = 12
	if err := writeLocalTail(dir, *tail); err != nil {
		t.Fatal(err)
	}
	beside, err := LoadBeside(dir, p.env)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "ledger.jsonl")
	_, err = ledger.RewrapWith(path, kp, out, func(string) error { return nil }, ledger.RewrapChecks{Heads: beside, Visit: beside.Visit, Held: beside.Held})
	var refusal *TailRefusal
	if !errors.As(err, &refusal) || !refusal.Truncation {
		t.Fatalf("want the tail's truncation refusal, got %v", err)
	}
	if _, err := os.Lstat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused re-wrap wrote its output (stat: %v)", err)
	}
}

// A SEALED HEAD WHOSE WITNESS KEY IS NOT BESIDE THE LEDGER HAS NOTHING TO
// STAND ON, for a re-wrap as for a boot: the interior records carry no
// proof, and the key is what says a witness saw them.
func TestARewrapRefusesASealedRecordWhoseWitnessKeyIsNotHeld(t *testing.T) {
	path, kp, p := sealedByAnAnchor(t)
	dir := filepath.Dir(path)
	if err := os.RemoveAll(WitnessKeysDir(dir)); err != nil {
		t.Fatal(err)
	}
	beside, err := LoadBeside(dir, p.env)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "ledger.jsonl")
	_, err = ledger.RewrapWith(path, kp, out, func(string) error { return nil }, ledger.RewrapChecks{Heads: beside, Visit: beside.Visit, Held: beside.Held})
	if !errors.Is(err, ledger.ErrWitnessKeyUnknown) {
		t.Fatalf("want ErrWitnessKeyUnknown, got %v", err)
	}
	if _, err := os.Lstat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused re-wrap wrote its output (stat: %v)", err)
	}
}
