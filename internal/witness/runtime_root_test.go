package witness

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// A delivery server can publish a coherent substitute platform key and a
// manifest signed by it. That is not authority to seal an identity's history:
// the next boot verifies under the shipped root, not that downloaded key.
func TestDownloadedRootCannotAuthorizeAnUnbootableWitnessHead(t *testing.T) {
	p := synthPlatform(t)
	// A key ID is a public label, not the trust anchor. The substitute
	// deliberately claims the shipped root's ID while carrying other keys.
	p.env.KeyID = genesis.PinnedRoot().KeyID
	for i := range p.env.Keys {
		material := &p.env.Keys[i]
		material.PublicKeyFingerprint = sigenvelope.PublicKeyFingerprint(material.Alg, p.env.KeyID, material.PublicKeyB64)
	}
	fw := newFakeWitness(t)
	fw.manifest = buildManifest(t, p, fw.witnessEnv, false)
	var downloads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloads.Add(1)
		if r.URL.Path != "/genesis/pubkey" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(p.env)
	}))
	t.Cleanup(server.Close)
	client := New(fw.server.URL, "")
	client.SetGenesisURL(server.URL)
	lg, key := testLedger(t, 3)
	defer lg.Close()
	dir := filepath.Dir(lg.Path())
	a := NewAnchorer(client, lg, AsIdentityKey(key), &memEnvelopeStore{}, &memReceiptStore{}, testMinter{lg, key}, 3, "")
	a.SetSealer(lg)
	seq, hash := lg.LastSeq(), lg.LastHash()
	if err := a.CheckAndAnchor(); !errors.Is(err, ErrManifestUntrusted) {
		t.Errorf("server-selected root must receive the typed manifest refusal: %v", err)
	}
	if downloads.Load() != 0 {
		t.Error("runtime still depends on a key download instead of its boot trust root")
	}
	if lg.LastSeq() != seq || lg.LastHash() != hash || lg.SealedSeq() != 0 {
		t.Errorf("untrusted pass changed the record: seq=%d sealed=%d", lg.LastSeq(), lg.SealedSeq())
	}
	if _, err := os.Stat(WitnessKeysDir(dir)); !os.IsNotExist(err) {
		t.Errorf("untrusted manifest was persisted beside the ledger: %v", err)
	}
	if a.IntegrityConflict() != nil {
		t.Error("a rejected signer is not proof of an identity history conflict")
	}
	fw.mu.Lock()
	bookmarks := len(fw.sawIdentityIDs)
	fw.mu.Unlock()
	if bookmarks != 0 {
		t.Error("an untrusted manifest reached the remote bookmark boundary")
	}
	hv, err := LoadHeadVerifier(dir, genesis.PinnedRoot())
	if err != nil {
		t.Fatalf("runtime left witness evidence the boot root cannot verify: %v", err)
	}
	if n, err := ledger.VerifyChain(lg.Path(), key.PublicKeyBytes(), hv); err != nil || uint64(n) != seq {
		t.Fatalf("record must remain boot-verifiable: n=%d err=%v", n, err)
	}
}

func TestConfiguredRootKeepsHistoryAndRetriesAfterARejectedManifest(t *testing.T) {
	p := synthPlatform(t)
	fw := newFakeWitness(t)
	manifest := buildManifest(t, p, fw.witnessEnv, false)
	fw.manifest = manifest
	lg, key := testLedger(t, 3)
	defer lg.Close()
	client := New(fw.server.URL, "")
	client.SetGenesisURL("http://127.0.0.1:1") // an explicit file needs no key server
	path := p.writeEnv(t)
	a := NewAnchorer(client, lg, AsIdentityKey(key), &memEnvelopeStore{}, &memReceiptStore{}, testMinter{lg, key}, 3, path)
	a.SetSealer(lg)
	if err := a.CheckAndAnchor(); err != nil {
		t.Fatalf("configured root must remain usable: %v", err)
	}
	dir := filepath.Dir(lg.Path())
	keyPath := filepath.Join(WitnessKeysDir(dir), fw.witnessEnv.KeyID+".json")
	before, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := lg.Append(ledger.EventExperienceCreate, 3, map[string]string{"content": "still living"}, key); err != nil {
			t.Fatal(err)
		}
	}
	fw.mu.Lock()
	fw.manifest = []byte(`{}`)
	fw.mu.Unlock()
	seq, hash, sealed := lg.LastSeq(), lg.LastHash(), lg.SealedSeq()
	if err := a.CheckAndAnchor(); !errors.Is(err, ErrManifestUntrusted) {
		t.Fatalf("invalid manifest must refuse this pass: %v", err)
	}
	after, err := os.ReadFile(keyPath)
	if err != nil || !bytes.Equal(before, after) || lg.LastSeq() != seq || lg.LastHash() != hash || lg.SealedSeq() != sealed {
		t.Fatal("a refused pass damaged existing witness evidence or history")
	}
	root, err := LoadPlatformEnvelope(path)
	if err != nil {
		t.Fatal(err)
	}
	hv, err := LoadHeadVerifier(dir, root)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := ledger.VerifyChain(lg.Path(), key.PublicKeyBytes(), hv); err != nil || uint64(n) != seq || hv.Verified() != 1 {
		t.Fatalf("existing history no longer verifies after refusal: n=%d err=%v", n, err)
	}
	// The same running owner retries normally after the publisher recovers.
	fw.mu.Lock()
	fw.manifest = manifest
	fw.mu.Unlock()
	if err := a.CheckAndAnchor(); err != nil {
		t.Fatalf("recovered manifest must resume anchoring without reset: %v", err)
	}
	hv, err = LoadHeadVerifier(dir, root)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := ledger.VerifyChain(lg.Path(), key.PublicKeyBytes(), hv); err != nil || uint64(n) != seq+1 || hv.Verified() != 2 {
		t.Fatalf("old and new heads must both verify: n=%d err=%v", n, err)
	}
}

func TestWitnessRootSelectionUsesThePinAndNeverHidesABrokenOverride(t *testing.T) {
	root, err := LoadPlatformEnvelope("")
	if err != nil || !bytes.Equal(mustCanonical(t, root), mustCanonical(t, genesis.PinnedRoot())) {
		t.Fatalf("default root differs from the existing genesis pin: %v", err)
	}
	path := filepath.Join(t.TempDir(), "platform.json")
	if _, err := LoadPlatformEnvelope(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing explicit root must not select the default: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPlatformEnvelope(path); err == nil {
		t.Fatal("invalid explicit root must not select the default")
	}
}
