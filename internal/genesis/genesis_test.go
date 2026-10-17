package genesis

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesislive"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/ring"
)

type testRoot struct {
	Env *publicKeyEnvelope
}

var liveOnce struct {
	sync.Once
	bundle []byte
	laws   string
	err    error
}

// mintTestRing0 nothing is minted here any more: this is the RING0 the
// platform is serving, verified against the SHIPPED pinned root, fetched
// once per test binary through genesislive and shared by every test that
// needs it. There is no fabricated constitution to choose from.
func mintTestRing0(t *testing.T) (*testRoot, []byte, string) {
	t.Helper()
	liveOnce.Do(func() {
		a, err := genesislive.Fetch()
		if err != nil {
			liveOnce.err = err
			return
		}
		laws, err := verifyBundle(a.Ring0, pinnedRoot(), "ring0.bundle")
		if err != nil {
			liveOnce.err = fmt.Errorf("live RING0 does not verify against the shipped pin: %w", err)
			return
		}
		liveOnce.bundle, liveOnce.laws = a.Ring0, laws
	})
	if liveOnce.err != nil {
		t.Fatalf("RING0 comes from the real servers and nowhere else (operator ruling 2026-09-19): %v", liveOnce.err)
	}
	return &testRoot{Env: pinnedRoot()}, liveOnce.bundle, liveOnce.laws
}

func TestBirth(t *testing.T) {
	dir := t.TempDir()

	root, bundle, _ := mintTestRing0(t)
	cfg := &BirthConfig{
		Name:        "TestIdentity",
		Ring0Bundle: bundle,
		Root:        root.Env,
		KeyPath:     filepath.Join(dir, "identity.sec"),
		LedgerPath:  filepath.Join(dir, "ledger.jsonl"),
		DBPath:      filepath.Join(dir, "aii.db"),
	}

	result, err := Birth(cfg)
	if err != nil {
		t.Fatalf("Birth failed: %v", err)
	}
	defer result.Ledger.Close()

	// Check identity was created
	if result.Name != "TestIdentity" {
		t.Errorf("name = %q, want TestIdentity", result.Name)
	}

	if result.Fingerprint != result.KeyPair.Fingerprint() {
		t.Error("fingerprint mismatch")
	}

	// Check birth event
	if result.BirthEvent.Seq != 1 {
		t.Errorf("birth seq = %d, want 1", result.BirthEvent.Seq)
	}
	if result.BirthEvent.Type != ledger.EventRing0Genesis {
		t.Errorf("birth type = %q, want ring0.genesis", result.BirthEvent.Type)
	}
	if result.BirthEvent.Ring != 0 {
		t.Errorf("birth ring = %d, want 0", result.BirthEvent.Ring)
	}

	// Check ledger chain verifies
	if _, err := ledger.VerifyChain(cfg.LedgerPath, result.KeyPair.PublicKey, nil); err != nil {
		t.Fatalf("chain verification failed: %v", err)
	}

	// Check key file exists
	if _, err := os.ReadFile(cfg.KeyPath); err != nil {
		t.Errorf("key file not readable: %v", err)
	}
}

func TestLoadRing0(t *testing.T) {
	dir := t.TempDir()

	root, bundle, laws := mintTestRing0(t)
	cfg := &BirthConfig{
		Name:        "TestIdentity",
		Ring0Bundle: bundle,
		Root:        root.Env,
		KeyPath:     filepath.Join(dir, "identity.sec"),
		LedgerPath:  filepath.Join(dir, "ledger.jsonl"),
		DBPath:      filepath.Join(dir, "aii.db"),
	}

	result, _ := Birth(cfg)
	result.Ledger.Close()

	// Reopen ledger and load Ring 0
	l, err := ledger.New(cfg.LedgerPath)
	if err != nil {
		t.Fatalf("reopen ledger failed: %v", err)
	}
	defer l.Close()

	rc, err := LoadRing0(l)
	if err != nil {
		t.Fatalf("LoadRing0 failed: %v", err)
	}

	if rc.Level != ring.Ring0 {
		t.Errorf("ring level = %d, want 0", rc.Level)
	}
	if rc.Content != laws {
		t.Error("Ring 0 content mismatch")
	}
	if rc.SignedBy != result.Fingerprint {
		t.Error("Ring 0 signed_by mismatch")
	}
	if rc.SigAlg != crypto.SigAlg {
		t.Errorf("sig_alg = %q, want %q", rc.SigAlg, crypto.SigAlg)
	}

	// Verify the Ring 0 signature
	err = ring.VerifySignature(rc, result.KeyPair.PublicKey)
	if err != nil {
		t.Fatalf("Ring 0 signature verification failed: %v", err)
	}
}

func TestBirthValidation(t *testing.T) {
	root, bundle, _ := mintTestRing0(t)

	// Missing name
	_, err := Birth(&BirthConfig{Ring0Bundle: bundle, Root: root.Env, KeyPath: "k", LedgerPath: "l", DBPath: "d"})
	if err == nil {
		t.Error("should fail with empty name")
	}

	// Missing Ring 0 bundle.
	_, err = Birth(&BirthConfig{Name: "x", Root: root.Env, KeyPath: "k", LedgerPath: "l", DBPath: "d"})
	if err == nil {
		t.Error("should fail with empty Ring 0 bundle")
	}

	// Missing trust root.
	_, err = Birth(&BirthConfig{Name: "x", Ring0Bundle: bundle, KeyPath: "k", LedgerPath: "l", DBPath: "d"})
	if err == nil {
		t.Error("should fail with missing trust root")
	}

	// Missing paths
	_, err = Birth(&BirthConfig{Name: "x", Ring0Bundle: bundle, Root: root.Env})
	if err == nil {
		t.Error("should fail with missing paths")
	}
}

// The payload-contract cases are gone with the fabricated vectors: a
// VALIDLY SIGNED bundle carrying a malformed payload can only be made by
// whoever holds the signing key, and that is the platform. What a test
// here can prove with real material is the refusal that matters — a
// bundle the anchor did not sign — and TestParallelServerAttackRefused
// and the provenance test below do exactly that.

func TestBirthAttestationPayload(t *testing.T) {
	dir := t.TempDir()
	root, bundle, _ := mintTestRing0(t)
	cfg := &BirthConfig{
		Name:        "Mira",
		Ring0Bundle: bundle,
		Root:        root.Env,
		KeyPath:     filepath.Join(dir, "identity.sec"),
		LedgerPath:  filepath.Join(dir, "ledger.jsonl"),
		DBPath:      filepath.Join(dir, "aii.db"),
	}

	result, _ := Birth(cfg)
	result.Ledger.Close()

	events, _ := ledger.ReadAll(cfg.LedgerPath)
	if len(events) != 1 {
		t.Fatalf("expected only ring0.genesis at birth, got %d events", len(events))
	}

	// First event is ring0.genesis
	if events[0].Type != "ring0.genesis" {
		t.Errorf("expected ring0.genesis, got %s", events[0].Type)
	}

	// Parse the birth payload
	var payload BirthAttestationPayload
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatalf("cannot parse payload: %v", err)
	}

	if payload.Name != "Mira" {
		t.Errorf("payload name = %q, want Mira", payload.Name)
	}
	if payload.PublicKey != result.KeyPair.PublicKeyB64() {
		t.Error("payload public key mismatch")
	}
	if payload.Ring0SigAlg != crypto.SigAlg {
		t.Errorf("payload sig alg = %q, want %q", payload.Ring0SigAlg, crypto.SigAlg)
	}
}

// Birth records the verified platform bundle as Ring 0 provenance.
func TestBirthRing0Provenance(t *testing.T) {
	dir := t.TempDir()
	paths := func() (string, string, string) {
		n := rand.Int()
		return filepath.Join(dir, fmt.Sprintf("k%d.sec", n)),
			filepath.Join(dir, fmt.Sprintf("l%d.jsonl", n)),
			filepath.Join(dir, fmt.Sprintf("d%d.db", n))
	}

	// Absent bundle: refused.
	k, l, d := paths()
	if _, err := Birth(&BirthConfig{Name: "Prov", KeyPath: k, LedgerPath: l, DBPath: d}); err == nil {
		t.Fatal("birth must refuse an absent Ring 0 bundle")
	}

	// A genuinely signed bundle: recorded as platform_bundle, the bundle
	// embedded, and the recorded Ring 0 content is exactly the bundle's.
	goodRoot, goodBundle, constitution := mintTestRing0(t)
	k, l, d = paths()
	r2, err := Birth(&BirthConfig{Name: "Prov", Ring0Bundle: goodBundle, Root: goodRoot.Env, KeyPath: k, LedgerPath: l, DBPath: d})
	if err != nil {
		t.Fatalf("bundle birth: %v", err)
	}
	r2.Ledger.Close()
	events2, _ := ledgerReadAllForProvenance(t, r2)
	var att struct {
		Ring0Provenance string `json:"ring0_provenance"`
		Ring0BundleB64  string `json:"ring0_bundle_b64"`
		Ring0Content    string `json:"ring0_content"`
	}
	_ = json.Unmarshal(events2[0].Payload, &att)
	if att.Ring0Provenance != "platform_bundle" {
		t.Fatalf("bundle birth must record platform_bundle, got %q", att.Ring0Provenance)
	}
	if att.Ring0BundleB64 == "" {
		t.Fatal("bundle birth must embed the bundle bytes for third-party verification")
	}
	if att.Ring0Content != constitution {
		t.Fatalf("recorded Ring 0 content must be the verified bundle's, got %q", att.Ring0Content)
	}

	// A bundle signed by a DIFFERENT root than the anchor: refused — no
	// identity is minted under a constitution the anchor did not sign.
	live, err := genesislive.Fetch()
	if err != nil {
		t.Fatal(err)
	}
	notTheSigner, err := DomainKeyFromBundle(live.Ring5PubkeyBundle, "ring5.pubkey")
	if err != nil {
		t.Fatal(err)
	}
	k, l, d = paths()
	if _, err := Birth(&BirthConfig{Name: "Prov", Ring0Bundle: goodBundle, Root: notTheSigner, KeyPath: k, LedgerPath: l, DBPath: d}); err == nil {
		t.Fatal("birth must refuse a bundle the trust anchor did not sign — minting under a constitution nobody vouched for")
	}
}

func ledgerReadAllForProvenance(t *testing.T, r *BirthResult) ([]ledger.Event, error) {
	t.Helper()
	return ledger.ReadAll(r.Ledger.Path())
}

func TestBirthAcceptsServerBundleKind(t *testing.T) {
	dir := t.TempDir()
	// A genuinely-signed bundle stamped with the SERVER's artifact_kind
	// ("ring0.bundle" — the vector signer and the live genesis server both
	// emit). Birth must verify and accept it; a birth
	// that fails on the real wire constant is a birth that never happens.
	root, serverBundle, _ := mintTestRing0(t)
	result, err := Birth(&BirthConfig{
		Name:        "WirePin",
		Ring0Bundle: serverBundle,
		Root:        root.Env,
		KeyPath:     filepath.Join(dir, "k.sec"),
		LedgerPath:  filepath.Join(dir, "l.jsonl"),
		DBPath:      filepath.Join(dir, "d.db"),
	})
	if err != nil {
		t.Fatalf("birth must accept the server-stamped artifact_kind (ring0.bundle): %v", err)
	}
	result.Ledger.Close()
}

func TestVerifySelfContained(t *testing.T) {
	dir := t.TempDir()
	root, bundle, _ := mintTestRing0(t)
	_, err := Birth(&BirthConfig{
		Name:        "PortableTest",
		Ring0Bundle: bundle,
		Root:        root.Env,
		KeyPath:     filepath.Join(dir, "identity.sec"),
		LedgerPath:  filepath.Join(dir, "ledger.jsonl"),
		DBPath:      filepath.Join(dir, "aii.db"),
	})
	if err != nil {
		t.Fatal(err)
	}

	n, fp, err := VerifySelfContained(filepath.Join(dir, "ledger.jsonl"))
	if err != nil {
		t.Fatalf("fresh chain must verify self-contained: %v", err)
	}
	if n != 1 || fp == "" {
		t.Fatalf("verify returned n=%d fp=%q", n, fp)
	}

	// Tamper one byte of a signature → NOT VERIFIED.
	raw, _ := os.ReadFile(filepath.Join(dir, "ledger.jsonl"))
	i := bytes.Index(raw, []byte(`"signature":"`))
	j := i + len(`"signature":"`)
	if raw[j] == '0' {
		raw[j] = '1'
	} else {
		raw[j] = '0'
	}
	os.WriteFile(filepath.Join(dir, "ledger.jsonl"), raw, 0600)
	if _, _, err := VerifySelfContained(filepath.Join(dir, "ledger.jsonl")); err == nil {
		t.Fatal("tampered chain must not verify")
	}
}
