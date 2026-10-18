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
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// headverify_test.go — the witness keys persisted beside the ledger and
// the heads they verify: a key file is trusted only after its manifest
// verifies under the platform root and vouches for exactly the stored
// envelope; a head is trusted only when its receipt attests the record
// before it (or, late, an earlier one: late_test.go), continues the
// receipt chain, and verifies under such a key within the key's window.

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
	// The same content again is a no-op: the file is byte-identical.
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
	// A root that did not sign the manifest does not vouch for the key.
	other := synthPlatform(t)
	if _, err := LoadWitnessKeys(f.dir, other.env); err == nil {
		t.Fatal("A KEY FILE VERIFIED UNDER A ROOT THAT DID NOT SIGN ITS MANIFEST")
	}
	// No directory is an empty set, not an error.
	if keys, err := LoadWitnessKeys(t.TempDir(), f.p.env); err != nil || len(keys) != 0 {
		t.Fatalf("absent directory: keys=%d err=%v", len(keys), err)
	}
	// A directory with keys but no root to verify under is an error.
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

// headOf returns the last record of the ledger, which the caller has
// just minted as a head.
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
	head1 := headOf(t, lg) // seq 4
	for i := 0; i < 2; i++ {
		if _, err := lg.Append(ledger.EventExperienceCreate, 3, map[string]any{"id": "e", "content": "x"}, kp); err != nil {
			t.Fatal(err)
		}
	}
	r2 := f.fw.signReceipt(t, WitnessRequest{IdentityID: id, LedgerOrdinal: 6, LedgerHash: lg.LastHash()}, 3, r1.LedgerHash)
	if _, err := mint.MintWitnessed(r2, f.fw.witnessEnv.KeyID); err != nil {
		t.Fatal(err)
	}
	head2 := headOf(t, lg) // seq 7

	hv := NewHeadVerifier(keys)
	if _, err := hv.VerifyHead(head1, nil); err != nil {
		t.Fatalf("first head: %v", err)
	}
	if _, err := hv.VerifyHead(head2, nil); err != nil {
		t.Fatalf("second head: %v", err)
	}
	if hv.Verified() != 2 || hv.Unverified() != 0 {
		t.Fatalf("verified %d unverified %d", hv.Verified(), hv.Unverified())
	}
	if n, err := ledger.VerifyChain(lg.Path(), kp.PublicKeyBytes(), NewHeadVerifier(keys)); err != nil || n != 7 {
		t.Fatalf("whole chain: n=%d err=%v", n, err)
	}

	// WITNESSED THROUGH is the last record a VERIFIED receipt attests:
	// head 2 sits at record 7 and attests record 6.
	if seq, hash, ok := hv.Attested(); !ok || seq != 6 || hash != r2.LedgerHash {
		t.Fatalf("attested (%d, %s, %v), want record 6 by its hash", seq, hash, ok)
	}
	if !strings.Contains(hv.Summary(), "2 witness heads verified under 1 persisted keys, witnessed through record 6 (") {
		t.Fatalf("summary: %s", hv.Summary())
	}

	// THE RECEIPT CHAIN'S CURSOR IS NOT THE WITNESS'S REACH. With no key
	// held, both heads are refused as unknown — and the second is refused
	// as UNKNOWN, not as a broken chain, which shows the cursor moved on
	// past a receipt nobody verified. Attested does not move with it.
	t.Run("a cursor that advanced is not a receipt that verified", func(t *testing.T) {
		v := NewHeadVerifier(nil)
		if _, err := v.VerifyHead(head1, nil); !errors.Is(err, ledger.ErrWitnessKeyUnknown) {
			t.Fatalf("first head: %v", err)
		}
		if _, err := v.VerifyHead(head2, nil); !errors.Is(err, ledger.ErrWitnessKeyUnknown) {
			t.Fatalf("fixture: the cursor did not advance past an unverified receipt: %v", err)
		}
		if seq, _, ok := v.Attested(); ok {
			t.Fatalf("A RECEIPT NOBODY VERIFIED IS REPORTED AS THE WITNESS'S REACH: record %d", seq)
		}
		if !strings.Contains(v.Summary(), "no record witnessed; 2 tail heads unverified") {
			t.Fatalf("summary: %s", v.Summary())
		}
	})

	t.Run("out of order breaks the receipt chain", func(t *testing.T) {
		if _, err := NewHeadVerifier(keys).VerifyHead(head2, nil); err == nil || !strings.Contains(err.Error(), "continues from") {
			t.Fatalf("want a chain error, got %v", err)
		}
	})
	t.Run("a receipt must attest the record before the head", func(t *testing.T) {
		wrong := *head1
		wrong.Prev = strings.Repeat("ab", 32)
		if _, err := NewHeadVerifier(keys).VerifyHead(&wrong, nil); err == nil || !strings.Contains(err.Error(), "attests hash") {
			t.Fatalf("want an attestation error, got %v", err)
		}
	})
	t.Run("an unknown key is counted, not verified", func(t *testing.T) {
		empty := NewHeadVerifier(nil)
		_, err := empty.VerifyHead(head1, nil)
		if !errors.Is(err, ledger.ErrWitnessKeyUnknown) {
			t.Fatalf("want ErrWitnessKeyUnknown, got %v", err)
		}
		if empty.Unverified() != 1 {
			t.Fatalf("unverified %d", empty.Unverified())
		}
		// In the tail such a head stands on the identity's proof; in a
		// segment it does not — the ledger package decides, this only
		// answers. A verifier walks one chain once: a fresh one here.
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
		v.VerifyHead(head1, nil)
		v.VerifyHead(head2, nil)

		chained := f.fw.signReceipt(t, WitnessRequest{IdentityID: id, LedgerOrdinal: int64(bad.Seq), LedgerHash: bad.EntryHash()}, 6, r2.LedgerHash)
		chained.WitnessSignature.SigB64 = r1.WitnessSignature.SigB64
		if _, err := mint.MintWitnessed(chained, f.fw.witnessEnv.KeyID); err != nil {
			t.Fatal(err)
		}
		forgedHead := headOf(t, lg)
		if _, err := v.VerifyHead(bad, nil); err == nil {
			t.Fatal("a head repeating an attested ordinal verified")
		}
		v2 := NewHeadVerifier(keys)
		v2.VerifyHead(head1, nil)
		v2.VerifyHead(head2, nil)
		v2.VerifyHead(bad, nil) // refused above; state unchanged only if the chain check fired first
		if _, err := v2.VerifyHead(forgedHead, nil); err == nil || !strings.Contains(err.Error(), "witness signature") {
			t.Fatalf("want a signature error, got %v", err)
		}
		// The forged receipt chained, so the cursor took it; its
		// signature failed, so the witness's reach did not.
		if seq, _, ok := v2.Attested(); !ok || seq != 6 {
			t.Fatalf("A FORGED RECEIPT MOVED THE WITNESS'S REACH: attested record %d (%v), want 6", seq, ok)
		}
	})
	t.Run("a head outside the key's window is refused", func(t *testing.T) {
		expired := map[string]*WitnessKeyMaterial{}
		for k, v := range keys {
			c := *v
			c.ExpiresAt = time.Now().Add(-48 * time.Hour)
			expired[k] = &c
		}
		if _, err := NewHeadVerifier(expired).VerifyHead(head1, nil); err == nil || !strings.Contains(err.Error(), "expired") {
			t.Fatalf("want an expiry error, got %v", err)
		}
	})
	t.Run("one identity throughout", func(t *testing.T) {
		v := NewHeadVerifier(keys)
		v.VerifyHead(head1, nil)
		other := *head2
		var payload map[string]json.RawMessage
		json.Unmarshal(other.Payload, &payload)
		var receipt map[string]any
		json.Unmarshal(payload["receipt"], &receipt)
		receipt["identity_id"] = "did:aiii:identity:sha256:someone-else"
		rr, _ := json.Marshal(receipt)
		payload["receipt"] = rr
		pp, _ := json.Marshal(payload)
		other.Payload = pp
		if _, err := v.VerifyHead(&other, nil); err == nil || !strings.Contains(err.Error(), "names identity") {
			t.Fatalf("want an identity error, got %v", err)
		}
	})
}

func TestAWitnessKeysEnvelopeIsAskedAtTheMomentTheHeadWasWitnessed(t *testing.T) {
	now := time.Now().UTC()
	witnessed := now.Add(-3 * time.Hour)
	for _, c := range []struct {
		name              string
		notBefore, expiry time.Time
		refused           bool
	}{
		{"the window ended after the head was witnessed and before now", now.Add(-48 * time.Hour), now.Add(-time.Hour), false},
		{"the window ended before the head was witnessed", now.Add(-48 * time.Hour), now.Add(-4 * time.Hour), true},
		{"the window opened after the head was witnessed", now.Add(-2 * time.Hour), now.Add(24 * time.Hour), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := synthPlatform(t)
			fw := newFakeWitness(t)
			fw.witnessEnv.NotBefore = c.notBefore.Format(time.RFC3339)
			fw.witnessEnv.ExpiresAt = c.expiry.Format(time.RFC3339)
			dir := t.TempDir()
			if err := persistWitnessKey(dir, fw.witnessEnv.KeyID, buildManifest(t, p, fw.witnessEnv, false), mustCanonical(t, fw.witnessEnv)); err != nil {
				t.Fatal(err)
			}
			// The key loads whatever the clock says of its window: the
			// window is a question about each head.
			keys, err := LoadWitnessKeys(dir, p.env)
			if err != nil {
				t.Fatalf("the key was refused at load, by the clock: %v", err)
			}
			lg, kp := testLedger(t, 3)
			r := fw.signReceiptAt(t, WitnessRequest{IdentityID: "did:aiii:identity:sha256:test", LedgerOrdinal: 3, LedgerHash: lg.LastHash()}, -1, ZeroLedgerHash, witnessed)
			if _, err := (testMinter{lg, kp}).MintWitnessed(r, fw.witnessEnv.KeyID); err != nil {
				t.Fatal(err)
			}
			_, err = NewHeadVerifier(keys).VerifyHead(headOf(t, lg), nil)
			if c.refused {
				if !errors.Is(err, sigenvelope.ErrOutsideWindow) {
					t.Fatalf("want the head refused outside its key's own envelope, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("A HEAD WITNESSED INSIDE ITS KEY'S WINDOW WAS REFUSED AFTER THE WINDOW ENDED: %v", err)
			}
			// And the walk boot runs over the whole chain.
			if n, err := ledger.VerifyChain(lg.Path(), kp.PublicKeyBytes(), NewHeadVerifier(keys)); err != nil || n != 4 {
				t.Fatalf("whole chain: n=%d err=%v", n, err)
			}
		})
	}
}

// THE SUMMARY SAYS WHAT WAS ACCEPTED WITHOUT A RECEIPT. A re-wrapped
// ledger's witnessed records are footnotes: they carry what the receipt
// once attested and no receipt, and they stand on the identity's proof
// alone. They were counted and never said — so a chain whose every
// witnessed record had become a footnote read exactly like one that was
// never witnessed at all.
func TestTheSummaryCountsFootnoteHeads(t *testing.T) {
	v := NewHeadVerifier(nil)
	for seq := uint64(5); seq <= 7; seq++ {
		evt := &ledger.Event{Seq: seq, Type: ledger.EventSystemWitnessed,
			Payload: []byte(`{"receipt_before_rewrap":{"ledger_ordinal":4,"ledger_hash":"x"}}`)}
		if _, err := v.VerifyHead(evt, nil); err != nil {
			t.Fatalf("a footnote head was refused: %v", err)
		}
	}
	if v.Footnotes() != 3 {
		t.Fatalf("fixture: %d footnotes counted", v.Footnotes())
	}
	if got := v.Summary(); !strings.Contains(got, "3 footnote heads accepted with no receipt") {
		t.Fatalf("the summary hides what was accepted without a receipt: %s", got)
	}
	// And a chain with none reads exactly as it always did.
	if got := NewHeadVerifier(nil).Summary(); got != "0 witness heads verified under 0 persisted keys, no record witnessed; 0 tail heads unverified" {
		t.Fatalf("the plain summary changed: %s", got)
	}
}
