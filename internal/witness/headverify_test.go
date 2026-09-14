package witness

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// .
// .
// .
// .
// .
// .

type persisted struct {
	dir      string
	p        *platformKeys
	fw       *fakeWitness
	manifest []byte
}

func persistedFixture(t *testing.T) persisted {
	t.Helper()
	p := synthPlatform(t)
	fw := newFakeWitness(t)
	dir := t.TempDir()
	manifest := buildManifest(t, p, fw.witnessEnv, false)
	if err := persistWitnessKey(dir, fw.witnessEnv.KeyID, manifest, mustCanonical(t, fw.witnessEnv)); err != nil {
		t.Fatal(err)
	}
	return persisted{dir: dir, p: p, fw: fw, manifest: manifest}
}

func TestPersistedWitnessKeyVerifiesUnderThePlatformRoot(t *testing.T) {
	f := persistedFixture(t)
	keys, err := LoadWitnessKeys(f.dir, f.p.env)
	if err != nil {
		t.Fatal(err)
	}
	km := keys[f.fw.witnessEnv.KeyID]
	if km == nil {
		t.Fatal("the persisted key was not loaded")
	}
	if string(km.PublicKey) != string(f.fw.witnessKp.PublicKeyBytes()) {
		t.Fatal("loaded key material differs from the witness's key")
	}
	if km.ExpiresAt.IsZero() || !km.ExpiresAt.After(time.Now()) {
		t.Fatal("the validity window was not taken from the manifest")
	}
	// .
	path := filepath.Join(WitnessKeysDir(f.dir), f.fw.witnessEnv.KeyID+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := persistWitnessKey(f.dir, f.fw.witnessEnv.KeyID, f.manifest, mustCanonical(t, f.fw.witnessEnv)); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("re-persisting the same key rewrote the file")
	}
	// .
	other := synthPlatform(t)
	if _, err := LoadWitnessKeys(f.dir, other.env); err == nil {
		t.Fatal("A KEY FILE VERIFIED UNDER A ROOT THAT DID NOT SIGN ITS MANIFEST")
	}
	// .
	if keys, err := LoadWitnessKeys(t.TempDir(), f.p.env); err != nil || len(keys) != 0 {
		t.Fatalf("absent directory: keys=%d err=%v", len(keys), err)
	}
	// .
	if _, err := LoadWitnessKeys(f.dir, nil); err == nil {
		t.Fatal("keys loaded with no platform root")
	}
}

func TestAPersistedKeyThatDoesNotVerifyRefusesTheLoad(t *testing.T) {
	f := persistedFixture(t)
	dir := WitnessKeysDir(f.dir)
	good, _ := os.ReadFile(filepath.Join(dir, f.fw.witnessEnv.KeyID+".json"))

	t.Run("tampered manifest", func(t *testing.T) {
		d := t.TempDir()
		if err := persistWitnessKey(d, f.fw.witnessEnv.KeyID, buildManifest(t, f.p, f.fw.witnessEnv, true), mustCanonical(t, f.fw.witnessEnv)); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadWitnessKeys(d, f.p.env); err == nil {
			t.Fatal("a tampered manifest loaded")
		}
	})
	t.Run("manifest for another key", func(t *testing.T) {
		d := t.TempDir()
		other := newFakeWitness(t)
		if err := persistWitnessKey(d, other.witnessEnv.KeyID, f.manifest, mustCanonical(t, other.witnessEnv)); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadWitnessKeys(d, f.p.env); err == nil {
			t.Fatal("a manifest vouching for a different envelope loaded")
		}
	})
	t.Run("file named for another key", func(t *testing.T) {
		d := t.TempDir()
		if err := os.MkdirAll(WitnessKeysDir(d), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(WitnessKeysDir(d), "aiii_witness_other.json"), good, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadWitnessKeys(d, f.p.env); err == nil || !strings.Contains(err.Error(), "named") {
			t.Fatalf("a misnamed key file loaded: %v", err)
		}
	})
	t.Run("garbage", func(t *testing.T) {
		d := t.TempDir()
		if err := os.MkdirAll(WitnessKeysDir(d), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(WitnessKeysDir(d), "aiii_witness_x.json"), []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadWitnessKeys(d, f.p.env); err == nil {
			t.Fatal("garbage loaded")
		}
	})
}

// .
// .
func headOf(t *testing.T, lg *ledger.Ledger) *ledger.Event {
	t.Helper()
	events, err := ledger.ReadAll(lg.Path())
	if err != nil {
		t.Fatal(err)
	}
	e := events[len(events)-1]
	return &e
}

func TestHeadVerifierChecksTheReceiptChainAndTheWitnessSignature(t *testing.T) {
	f := persistedFixture(t)
	keys, err := LoadWitnessKeys(f.dir, f.p.env)
	if err != nil {
		t.Fatal(err)
	}
	lg, kp := testLedger(t, 3)
	defer lg.Close()
	mint := testMinter{lg, kp}
	const id = "did:aiii:identity:sha256:test"

	r1 := f.fw.signReceipt(t, WitnessRequest{IdentityID: id, LedgerOrdinal: 3, LedgerHash: lg.LastHash()}, -1, ZeroLedgerHash)
	if _, err := mint.MintWitnessed(r1, f.fw.witnessEnv.KeyID); err != nil {
		t.Fatal(err)
	}
	head1 := headOf(t, lg)
	for i := 0; i < 2; i++ {
		if _, err := lg.Append(ledger.EventExperienceCreate, kp.Fingerprint(), 3, map[string]interface{}{"id": "e", "content": "x"}, kp); err != nil {
			t.Fatal(err)
		}
	}
	r2 := f.fw.signReceipt(t, WitnessRequest{IdentityID: id, LedgerOrdinal: 6, LedgerHash: lg.LastHash()}, 3, r1.LedgerHash)
	if _, err := mint.MintWitnessed(r2, f.fw.witnessEnv.KeyID); err != nil {
		t.Fatal(err)
	}
	head2 := headOf(t, lg)

	hv := NewHeadVerifier(keys)
	if err := hv.VerifyHead(head1); err != nil {
		t.Fatalf("first head: %v", err)
	}
	if err := hv.VerifyHead(head2); err != nil {
		t.Fatalf("second head: %v", err)
	}
	if hv.Verified() != 2 || hv.Unverified() != 0 {
		t.Fatalf("verified %d unverified %d", hv.Verified(), hv.Unverified())
	}
	if n, err := ledger.VerifyChain(lg.Path(), kp.PublicKeyBytes(), NewHeadVerifier(keys)); err != nil || n != 7 {
		t.Fatalf("whole chain: n=%d err=%v", n, err)
	}

	t.Run("out of order breaks the receipt chain", func(t *testing.T) {
		if err := NewHeadVerifier(keys).VerifyHead(head2); err == nil || !strings.Contains(err.Error(), "continues from") {
			t.Fatalf("want a chain error, got %v", err)
		}
	})
	t.Run("a receipt must attest the record before the head", func(t *testing.T) {
		wrong := *head1
		wrong.Prev = strings.Repeat("ab", 32)
		if err := NewHeadVerifier(keys).VerifyHead(&wrong); err == nil || !strings.Contains(err.Error(), "attests hash") {
			t.Fatalf("want an attestation error, got %v", err)
		}
	})
	t.Run("an unknown key is counted, not verified", func(t *testing.T) {
		empty := NewHeadVerifier(nil)
		err := empty.VerifyHead(head1)
		if !errors.Is(err, ledger.ErrWitnessKeyUnknown) {
			t.Fatalf("want ErrWitnessKeyUnknown, got %v", err)
		}
		if empty.Unverified() != 1 {
			t.Fatalf("unverified %d", empty.Unverified())
		}
		// .
		// .
		// .
		fresh := NewHeadVerifier(nil)
		if n, err := ledger.VerifyChain(lg.Path(), kp.PublicKeyBytes(), fresh); err != nil || n != 7 || fresh.Unverified() != 2 {
			t.Fatalf("tail heads under an unknown key: n=%d unverified=%d err=%v", n, fresh.Unverified(), err)
		}
	})
	t.Run("a forged witness signature is refused", func(t *testing.T) {
		forged := r2
		forged.WitnessSignature.SigB64 = r1.WitnessSignature.SigB64
		if _, err := mint.MintWitnessed(forged, f.fw.witnessEnv.KeyID); err != nil {
			t.Fatal(err)
		}
		bad := headOf(t, lg)
		v := NewHeadVerifier(keys)
		v.VerifyHead(head1)
		v.VerifyHead(head2)
		// .
		// .
		chained := f.fw.signReceipt(t, WitnessRequest{IdentityID: id, LedgerOrdinal: int64(bad.Seq), LedgerHash: bad.EntryHash()}, 6, r2.LedgerHash)
		chained.WitnessSignature.SigB64 = r1.WitnessSignature.SigB64
		if _, err := mint.MintWitnessed(chained, f.fw.witnessEnv.KeyID); err != nil {
			t.Fatal(err)
		}
		forgedHead := headOf(t, lg)
		if err := v.VerifyHead(bad); err == nil {
			t.Fatal("a head repeating an attested ordinal verified")
		}
		v2 := NewHeadVerifier(keys)
		v2.VerifyHead(head1)
		v2.VerifyHead(head2)
		v2.VerifyHead(bad)
		if err := v2.VerifyHead(forgedHead); err == nil || !strings.Contains(err.Error(), "witness signature") {
			t.Fatalf("want a signature error, got %v", err)
		}
	})
	t.Run("a head outside the key's window is refused", func(t *testing.T) {
		expired := map[string]*WitnessKeyMaterial{}
		for k, v := range keys {
			c := *v
			c.ExpiresAt = time.Now().Add(-48 * time.Hour)
			expired[k] = &c
		}
		if err := NewHeadVerifier(expired).VerifyHead(head1); err == nil || !strings.Contains(err.Error(), "expired") {
			t.Fatalf("want an expiry error, got %v", err)
		}
	})
	t.Run("one identity throughout", func(t *testing.T) {
		v := NewHeadVerifier(keys)
		v.VerifyHead(head1)
		other := *head2
		var payload map[string]json.RawMessage
		json.Unmarshal(other.Payload, &payload)
		var receipt map[string]interface{}
		json.Unmarshal(payload["receipt"], &receipt)
		receipt["identity_id"] = "did:aiii:identity:sha256:someone-else"
		rr, _ := json.Marshal(receipt)
		payload["receipt"] = rr
		pp, _ := json.Marshal(payload)
		other.Payload = pp
		if err := v.VerifyHead(&other); err == nil || !strings.Contains(err.Error(), "names identity") {
			t.Fatalf("want an identity error, got %v", err)
		}
	})
}
