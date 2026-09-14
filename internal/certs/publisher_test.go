package certs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/witness"
	"github.com/aiii-dot-id/aii-os/internal/witness/witnesstest"
)

// .
// .
// .

// .
// .
type fakeCertd struct {
	t      *testing.T
	srv    *httptest.Server
	zone   string
	mu     sync.Mutex
	claims map[string]string
	env    map[string]witness.PublicKeyEnvelope
	txt    map[string]map[string]bool
	nonces map[string]bool
	seen   []string
}

func newFakeCertd(t *testing.T) *fakeCertd {
	f := &fakeCertd{t: t, zone: "example.test", claims: map[string]string{}, env: map[string]witness.PublicKeyEnvelope{}, txt: map[string]map[string]bool{}, nonces: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/certd/claim", f.claim)
	mux.HandleFunc("/certd/publish", f.publish)
	mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		zone := f.zone
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"service": "ai3-certd", "zone": zone, "relays": []string{"relay1." + zone}})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeCertd) verify(env witness.PublicKeyEnvelope, sig witness.SignatureEntry, input []byte) bool {
	ml, ok := env.FindPublicKey(witness.AlgMLDSA87)
	if !ok || sig.KeyID != env.KeyID || sig.PublicKeyFingerprint != ml.PublicKeyFingerprint {
		return false
	}
	pub, err := witnesstest.DecodeB64(ml.PublicKeyB64)
	if err != nil {
		return false
	}
	raw, err := witnesstest.DecodeB64(sig.SigB64)
	if err != nil {
		return false
	}
	return crypto.Verify(pub, input, raw) == nil
}

func (f *fakeCertd) claim(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IdentityID        string                 `json:"identity_id"`
		IdentityPublicKey json.RawMessage        `json:"identity_public_key"`
		NameID            string                 `json:"name_id"`
		Nonce             string                 `json:"nonce"`
		ExpiresAt         string                 `json:"expires_at"`
		Signature         witness.SignatureEntry `json:"identity_signature"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "malformed", http.StatusBadRequest)
		return
	}
	var env witness.PublicKeyEnvelope
	if err := json.Unmarshal(body.IdentityPublicKey, &env); err != nil {
		http.Error(w, "envelope", http.StatusBadRequest)
		return
	}
	if !f.verify(env, body.Signature, ClaimInput(body.IdentityID, body.IdentityPublicKey, body.NameID, body.Nonce, body.ExpiresAt)) {
		http.Error(w, `{"error":"signature does not verify"}`, http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, "claim "+body.NameID)
	if f.nonces[body.Nonce] {
		http.Error(w, `{"error":"nonce replayed"}`, http.StatusConflict)
		return
	}
	f.nonces[body.Nonce] = true
	owner, taken := f.claims[body.NameID]
	code := http.StatusCreated
	if taken {
		if owner != body.IdentityID {
			http.Error(w, `{"error":"claimed by another identity"}`, http.StatusConflict)
			return
		}
		code = http.StatusOK
	}
	f.claims[body.NameID] = body.IdentityID
	f.env[body.IdentityID] = env
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"name": "ui." + body.NameID + "." + f.zone, "zone": f.zone})
}

func (f *fakeCertd) publish(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IdentityID string                 `json:"identity_id"`
		Name       string                 `json:"name"`
		TXT        string                 `json:"txt"`
		Nonce      string                 `json:"nonce"`
		ExpiresAt  string                 `json:"expires_at"`
		Signature  witness.SignatureEntry `json:"identity_signature"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "malformed", http.StatusBadRequest)
		return
	}
	tag := publishInputTag
	if r.Method == http.MethodDelete {
		tag = unpublishInputTag
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	env, known := f.env[body.IdentityID]
	if !known || !f.verify(env, body.Signature, PublishInput(tag, body.IdentityID, body.Name, body.TXT, body.Nonce, body.ExpiresAt)) {
		http.Error(w, `{"error":"signature does not verify"}`, http.StatusUnauthorized)
		return
	}
	// .
	owned := false
	for id, owner := range f.claims {
		if owner == body.IdentityID && body.Name == "_acme-challenge.ui."+id+"."+f.zone {
			owned = true
		}
	}
	if !owned {
		http.Error(w, `{"error":"not your name"}`, http.StatusForbidden)
		return
	}
	f.seen = append(f.seen, r.Method+" "+body.Name)
	if r.Method == http.MethodDelete {
		delete(f.txt[body.Name], body.TXT)
		w.WriteHeader(http.StatusOK)
		return
	}
	if f.txt[body.Name] == nil {
		f.txt[body.Name] = map[string]bool{}
	}
	f.txt[body.Name][body.TXT] = true
	w.WriteHeader(http.StatusAccepted)
}

func identityForTest(t *testing.T) (witness.IdentityKey, []byte, *witness.PublicKeyEnvelope) {
	t.Helper()
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	store := &memEnvelopes{}
	canonical, env, err := witness.EnsureIdentityEnvelope(witness.AsIdentityKey(kp), store)
	if err != nil {
		t.Fatal(err)
	}
	return witness.AsIdentityKey(kp), canonical, env
}

type memEnvelopes struct{ raw []byte }

func (m *memEnvelopes) SaveWitnessEnvelope(c []byte) error   { m.raw = c; return nil }
func (m *memEnvelopes) LoadWitnessEnvelope() ([]byte, error) { return m.raw, nil }

func TestTheClientClaimsPublishesAndUnpublishesUnderItsOwnSignature(t *testing.T) {
	f := newFakeCertd(t)
	key, canonical, env := identityForTest(t)
	c, err := NewPublisherClient(f.srv.URL, "", key, canonical, env)
	if err != nil {
		t.Fatal(err)
	}
	const id = "abcdefghijklmnopqrstuvwxyz"
	claimed, err := c.Claim(context.Background(), id)
	name, zone := claimed.Name, claimed.Zone
	if err != nil || name != "ui."+id+".example.test" || zone != "example.test" {
		t.Fatalf("claim: %q %q %v", name, zone, err)
	}
	// .
	if _, err := c.Claim(context.Background(), id); err != nil {
		t.Fatalf("repeating one's own claim: %v", err)
	}
	// .
	okey, ocanonical, oenv := identityForTest(t)
	other, _ := NewPublisherClient(f.srv.URL, "", okey, ocanonical, oenv)
	if _, err := other.Claim(context.Background(), id); err == nil {
		t.Fatal("another identity claimed a taken id")
	}
	ch := "_acme-challenge." + name
	if err := c.Publish(context.Background(), ch, "value-one"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := c.Publish(context.Background(), ch, "value-two"); err != nil {
		t.Fatalf("second value: %v", err)
	}
	if !f.txt[ch]["value-one"] || !f.txt[ch]["value-two"] {
		t.Fatal("two values must stand at once")
	}
	if err := c.Unpublish(context.Background(), ch, "value-one"); err != nil {
		t.Fatalf("unpublish: %v", err)
	}
	if f.txt[ch]["value-one"] || !f.txt[ch]["value-two"] {
		t.Fatal("unpublish removed the wrong value")
	}
	// .
	if err := c.Publish(context.Background(), "_acme-challenge.ui.zzzzzzzzzzzzzzzzzzzzzzzzzz.example.test", "x"); err == nil {
		t.Fatal("published for a name the identity does not own")
	}
	// .
	if err := other.Publish(context.Background(), ch, "x"); err == nil {
		t.Fatal("another identity published for this name")
	}
	// .
	var _ Publisher = c
}

// .
// .
func TestSignatureInputsMatchTheServiceREADME(t *testing.T) {
	claim := ClaimInput("did:aiii:identity:sha256:abc", []byte(`{"k":"v"}`), "abcdefghijklmnopqrstuvwxyz", "n0nce", "2026-09-03T12:10:00Z")
	wantClaim := "AIII-CERTD-CLAIM\nidentity_id:did:aiii:identity:sha256:abc\nidentity_public_key:{\"k\":\"v\"}\nname_id:abcdefghijklmnopqrstuvwxyz\nnonce:n0nce\nexpires_at:2026-09-03T12:10:00Z\n"
	if string(claim) != wantClaim {
		t.Fatalf("claim input drifted:\n%q\nwant\n%q", claim, wantClaim)
	}
	pub := PublishInput(publishInputTag, "did:aiii:identity:sha256:abc", "_acme-challenge.ui.abcdefghijklmnopqrstuvwxyz.example.test", "tXt", "n0nce", "2026-09-03T12:15:00Z")
	wantPub := "AIII-CERTD-PUBLISH\nidentity_id:did:aiii:identity:sha256:abc\nname:_acme-challenge.ui.abcdefghijklmnopqrstuvwxyz.example.test\ntxt:tXt\nnonce:n0nce\nexpires_at:2026-09-03T12:15:00Z\n"
	if string(pub) != wantPub {
		t.Fatalf("publish input drifted:\n%q\nwant\n%q", pub, wantPub)
	}
	if un := PublishInput(unpublishInputTag, "i", "n", "t", "x", "e"); string(un) != "AIII-CERTD-UNPUBLISH\nidentity_id:i\nname:n\ntxt:t\nnonce:x\nexpires_at:e\n" {
		t.Fatalf("unpublish input drifted: %q", un)
	}
	_ = time.Now
}

// .
// .
func TestServiceZoneIsReadFromStatus(t *testing.T) {
	f := newFakeCertd(t)
	key, canonical, env := identityForTest(t)
	c, err := NewPublisherClient(f.srv.URL, "", key, canonical, env)
	if err != nil {
		t.Fatal(err)
	}
	zone, err := c.ServiceZone(context.Background())
	if err != nil || zone != "example.test" {
		t.Fatalf("zone %q err %v", zone, err)
	}
	f.mu.Lock()
	f.zone = "aiios.id"
	f.mu.Unlock()
	if zone, _ := c.ServiceZone(context.Background()); zone != "aiios.id" {
		t.Fatalf("the moved zone is read live: %q", zone)
	}
	st, err := c.ServiceStatus(context.Background())
	if err != nil || len(st.Relays) != 1 || st.Relays[0] != "relay1.aiios.id" {
		t.Fatalf("the relays the service feeds are read with the zone: %+v err %v", st, err)
	}
}
