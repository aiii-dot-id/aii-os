package witness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// .
// .
// .
// .

type recordingSealer struct{ heads []uint64 }

func (r *recordingSealer) Seal(h uint64) error { r.heads = append(r.heads, h); return nil }

func TestTheAnchorerPersistsTheKeyAndSealsUnderAVerifiedManifest(t *testing.T) {
	p := synthPlatform(t)
	fw := newFakeWitness(t)
	fw.manifest = buildManifest(t, p, fw.witnessEnv, false)
	lg, kp := testLedger(t, 5)
	defer lg.Close()
	dir := filepath.Dir(lg.Path())

	a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, testMinter{lg, kp}, 5, p.writeEnv(t))
	a.SetSealer(lg)
	if err := a.CheckAndAnchor(); err != nil {
		t.Fatalf("anchor: %v", err)
	}

	keys, err := LoadWitnessKeys(dir, p.env)
	if err != nil {
		t.Fatalf("the persisted key does not load: %v", err)
	}
	if keys[fw.witnessEnv.KeyID] == nil {
		t.Fatal("THE VERIFIED WITNESS KEY WAS NOT PERSISTED BESIDE THE LEDGER")
	}
	if lg.SealedSeq() != 6 {
		t.Fatalf("sealed through %d, want 6 (five records and the head)", lg.SealedSeq())
	}
	if _, err := os.Stat(filepath.Join(dir, "segment-1-6.jsonl.gz")); err != nil {
		t.Fatal("no segment beside the ledger after the seal")
	}
	hv, err := LoadHeadVerifier(dir, p.env)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := ledger.VerifyChain(lg.Path(), kp.PublicKeyBytes(), hv); err != nil || n != 6 {
		t.Fatalf("verify: n=%d err=%v", n, err)
	}
	if hv.Verified() != 1 {
		t.Fatalf("the head was not verified under the persisted key (verified %d)", hv.Verified())
	}
	if _, err := ledger.VerifyChain(lg.Path(), kp.PublicKeyBytes(), nil); err == nil {
		t.Fatal("a sealed segment verified with no witness key at all")
	}

	// .
	for i := 0; i < 5; i++ {
		if _, err := lg.Append(ledger.EventExperienceCreate, kp.Fingerprint(), 3, map[string]interface{}{"id": "e", "content": "x"}, kp); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.CheckAndAnchor(); err != nil {
		t.Fatalf("second anchor: %v", err)
	}
	if lg.SealedSeq() != 12 {
		t.Fatalf("sealed through %d after the second anchor, want 12", lg.SealedSeq())
	}
	if _, err := os.Stat(filepath.Join(dir, "segment-7-12.jsonl.gz")); err != nil {
		t.Fatal("no second segment")
	}
	hv2, _ := LoadHeadVerifier(dir, p.env)
	if n, err := ledger.VerifyChain(lg.Path(), kp.PublicKeyBytes(), hv2); err != nil || n != 12 || hv2.Verified() != 2 {
		t.Fatalf("after the second seal: n=%d verified=%d err=%v", n, hv2.Verified(), err)
	}
}

func TestASelfVouchedWitnessMintsHeadsButSealsNothing(t *testing.T) {
	fw := newFakeWitness(t)
	lg, kp := testLedger(t, 3)
	defer lg.Close()
	dir := filepath.Dir(lg.Path())

	a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, testMinter{lg, kp}, 3, "")
	rec := &recordingSealer{}
	a.SetSealer(rec)
	if err := a.CheckAndAnchor(); err != nil {
		t.Fatalf("anchor: %v", err)
	}
	if len(rec.heads) != 0 {
		t.Fatalf("A SELF-VOUCHED HEAD SEALED: %v", rec.heads)
	}
	if _, err := os.Stat(WitnessKeysDir(dir)); err == nil {
		t.Fatal("a self-vouched key was persisted as if the platform had vouched for it")
	}
	if lg.LastSeq() != 4 {
		t.Fatalf("the head was not minted (last seq %d)", lg.LastSeq())
	}
	hv := NewHeadVerifier(nil)
	if n, err := ledger.VerifyChain(lg.Path(), kp.PublicKeyBytes(), hv); err != nil || n != 4 || hv.Unverified() != 1 {
		t.Fatalf("tail head under an unknown key: n=%d unverified=%d err=%v", n, hv.Unverified(), err)
	}
}
