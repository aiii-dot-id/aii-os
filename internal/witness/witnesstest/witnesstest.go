// .
// .
// .
// .
// .
// .
// .
package witnesstest

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	slh "github.com/trailofbits/go-slh-dsa/slh_dsa"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

// .
type Platform struct {
	Env       *witness.PublicKeyEnvelope
	mlKp      *crypto.KeyPair
	slhSk     *slh.SecretKey
	slhPubB64 string
}

// .
func NewPlatform(t testing.TB) *Platform {
	t.Helper()
	mlKp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	skVal, pub, err := slh.SLHKeygen(slh.SlhDsaSha2_256s())
	if err != nil {
		t.Fatalf("SLH keygen: %v", err)
	}
	now := time.Now().UTC()
	keyID := "aiii_platform_test_" + mlKp.Fingerprint()[:12]
	pubB64 := base64.StdEncoding.EncodeToString(pub.Bytes())
	mlFp := sigenvelope.SHA256Prefixed([]byte(witness.FingerprintMaterial(witness.AlgMLDSA87, keyID, mlKp.PublicKeyB64())))
	slhFp := sigenvelope.SHA256Prefixed([]byte(witness.FingerprintMaterial(witness.AlgSLHDSASHA2256, keyID, pubB64)))
	return &Platform{
		Env: &witness.PublicKeyEnvelope{
			V: 1, Kind: witness.PublicKeyEnvelopeKind, KeyID: keyID, KeyType: "platform", Profile: witness.ProfileRoot,
			CreatedAt: now.Format(time.RFC3339), NotBefore: now.Add(-time.Hour).Format(time.RFC3339), ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339),
			Keys: []witness.PublicKeyMaterial{
				{Alg: witness.AlgMLDSA87, PublicKeyB64: mlKp.PublicKeyB64(), PublicKeyFingerprint: mlFp},
				{Alg: witness.AlgSLHDSASHA2256, PublicKeyB64: pubB64, PublicKeyFingerprint: slhFp},
			},
		},
		mlKp:      mlKp,
		slhSk:     &skVal,
		slhPubB64: pubB64,
	}
}

// .
// .
func (p *Platform) WriteEnv(t testing.TB, dir string) string {
	t.Helper()
	raw, err := json.Marshal(p.Env)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "platform.pub.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// .
// .
func (p *Platform) SignManifest(t testing.TB, witnessEnv *witness.PublicKeyEnvelope) []byte {
	t.Helper()
	envRaw, err := json.Marshal(witnessEnv)
	if err != nil {
		t.Fatal(err)
	}
	envCanonical, err := canonicaljson.CanonicalizeV1(envRaw)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	payload := map[string]interface{}{
		"kind": "witness.public_key_manifest", "schema_version": 1,
		"artifact_version":  "2026.09.03.1",
		"artifact_hash":     sigenvelope.SHA256Prefixed(envCanonical),
		"key_id":            witnessEnv.KeyID,
		"signature_profile": witness.ProfileRoot,
		"created_at":        now.Format(time.RFC3339),
		"not_before":        now.Add(-time.Hour).Format(time.RFC3339),
		"expires_at":        now.Add(24 * time.Hour).Format(time.RFC3339),
		"critical":          true,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	canonicalPayload, err := canonicaljson.CanonicalizeV1(raw)
	if err != nil {
		t.Fatal(err)
	}
	payloadSHA := sigenvelope.SHA256Prefixed(canonicalPayload)

	mlFp := sigenvelope.SHA256Prefixed([]byte(witness.FingerprintMaterial(witness.AlgMLDSA87, p.Env.KeyID, p.mlKp.PublicKeyB64())))
	in := sigenvelope.SignatureInput("witness.public_key_manifest", witness.ProfileRoot, witness.AlgMLDSA87, p.Env.KeyID, mlFp, payloadSHA)
	sig, err := crypto.Sign(p.mlKp, []byte(in))
	if err != nil {
		t.Fatal(err)
	}
	slhFp := sigenvelope.SHA256Prefixed([]byte(witness.FingerprintMaterial(witness.AlgSLHDSASHA2256, p.Env.KeyID, p.slhPubB64)))
	in2 := sigenvelope.SignatureInput("witness.public_key_manifest", witness.ProfileRoot, witness.AlgSLHDSASHA2256, p.Env.KeyID, slhFp, payloadSHA)
	sig2, err := p.slhSk.Sign(rand.Reader, []byte(in2), nil)
	if err != nil {
		t.Fatal(err)
	}
	sigs := []witness.SignatureEntry{
		{SignatureProfile: witness.ProfileRoot, Alg: witness.AlgMLDSA87, KeyID: p.Env.KeyID, PublicKeyFingerprint: mlFp,
			SignatureInputSHA256: sigenvelope.SHA256Prefixed([]byte(in)), SigB64: base64.StdEncoding.EncodeToString(sig)},
		{SignatureProfile: witness.ProfileRoot, Alg: witness.AlgSLHDSASHA2256, KeyID: p.Env.KeyID, PublicKeyFingerprint: slhFp,
			SignatureInputSHA256: sigenvelope.SHA256Prefixed([]byte(in2)), SigB64: base64.StdEncoding.EncodeToString(sig2)},
	}
	bundle := map[string]interface{}{
		"artifact_kind":     "witness.public_key_manifest",
		"payload":           json.RawMessage(canonicalPayload),
		"payload_sha256":    payloadSHA,
		"canonicalization":  witness.CanonicalizationV1,
		"signature_profile": witness.ProfileRoot,
		"signatures":        sigs,
	}
	out, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

type row struct {
	ordinal int64
	hash    string
	receipt witness.WitnessReceipt
}

// .
type Witness struct {
	Env      *witness.PublicKeyEnvelope
	KeyID    string
	kp       *crypto.KeyPair
	server   *httptest.Server
	manifest []byte

	mu        sync.Mutex
	rows      map[string]*row
	Bookmarks int
}

// .
// .
func NewWitness(t testing.TB, p *Platform) *Witness {
	t.Helper()
	kp, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	keyID := "aiii_witness_test_" + kp.Fingerprint()[:16]
	env := &witness.PublicKeyEnvelope{
		V: 1, Kind: witness.PublicKeyEnvelopeKind, KeyID: keyID, KeyType: "witness", Profile: witness.ProfileRoot,
		CreatedAt: now.Format(time.RFC3339), NotBefore: now.Add(-time.Hour).Format(time.RFC3339), ExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339),
		Keys: []witness.PublicKeyMaterial{{
			Alg: witness.AlgMLDSA87, PublicKeyB64: kp.PublicKeyB64(),
			PublicKeyFingerprint: sigenvelope.SHA256Prefixed([]byte(witness.FingerprintMaterial(witness.AlgMLDSA87, keyID, kp.PublicKeyB64()))),
		}},
	}
	w := &Witness{Env: env, KeyID: keyID, kp: kp, rows: map[string]*row{}}
	if p != nil {
		w.manifest = p.SignManifest(t, env)
	}
	envRaw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	envCanonical, err := canonicaljson.CanonicalizeV1(envRaw)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/witness/pubkey/hash", func(rw http.ResponseWriter, r *http.Request) {
		writeJSON(rw, map[string]string{"witness_public_key_hash": sigenvelope.SHA256Prefixed(envCanonical), "key_id": keyID})
	})
	mux.HandleFunc("/witness/pubkey", func(rw http.ResponseWriter, r *http.Request) {
		writeJSON(rw, env)
	})
	mux.HandleFunc("/witness/pubkey/manifest", func(rw http.ResponseWriter, r *http.Request) {
		if w.manifest == nil {
			http.Error(rw, "no manifest", http.StatusNotFound)
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		_, _ = rw.Write(w.manifest)
	})
	mux.HandleFunc("/status", func(rw http.ResponseWriter, r *http.Request) {
		writeJSON(rw, map[string]interface{}{"min_periodic_cadence": 0})
	})
	mux.HandleFunc("/witness/bookmark", w.handleBookmark)
	w.server = httptest.NewServer(mux)
	t.Cleanup(w.server.Close)
	return w
}

// .
func (w *Witness) URL() string { return w.server.URL }

// .
// .
// .
// .
// .
// .
// .
// .
// .
func (w *Witness) Holds() (ordinal int64, identities int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	ordinal = -1
	for _, st := range w.rows {
		ordinal = st.ordinal
	}
	return ordinal, len(w.rows)
}

// .
// .
func (w *Witness) Reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.rows = map[string]*row{}
}

func (w *Witness) handleBookmark(rw http.ResponseWriter, r *http.Request) {
	var req witness.WitnessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(rw, "malformed", http.StatusBadRequest)
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.Bookmarks++
	input := witness.RequestSignatureInput(req, req.IdentityPublicKey)
	if sigenvelope.SHA256Prefixed(input) != req.IdentitySignature.SignatureInputSHA256 {
		http.Error(rw, "identity signature input hash mismatch", http.StatusUnauthorized)
		return
	}
	var identityEnv witness.PublicKeyEnvelope
	if err := json.Unmarshal(req.IdentityPublicKey, &identityEnv); err != nil {
		http.Error(rw, "identity key unparseable", http.StatusBadRequest)
		return
	}
	ml, ok := identityEnv.FindPublicKey(witness.AlgMLDSA87)
	if !ok {
		http.Error(rw, "identity key missing", http.StatusBadRequest)
		return
	}
	pub, err := base64.StdEncoding.DecodeString(ml.PublicKeyB64)
	if err != nil {
		http.Error(rw, "identity key encoding", http.StatusBadRequest)
		return
	}
	sig, err := base64.StdEncoding.DecodeString(req.IdentitySignature.SigB64)
	if err != nil {
		http.Error(rw, "bad sig encoding", http.StatusUnauthorized)
		return
	}
	if err := crypto.Verify(pub, input, sig); err != nil {
		http.Error(rw, "identity signature verification failed", http.StatusUnauthorized)
		return
	}
	st, found := w.rows[req.IdentityID]
	if !found {
		receipt := w.sign(req, -1, witness.ZeroLedgerHash)
		w.rows[req.IdentityID] = &row{req.LedgerOrdinal, req.LedgerHash, receipt}
		rw.WriteHeader(http.StatusCreated)
		writeJSON(rw, receipt)
		return
	}
	if st.ordinal == req.LedgerOrdinal && st.hash == req.LedgerHash {
		writeJSON(rw, st.receipt)
		return
	}
	if req.LedgerOrdinal <= st.ordinal {
		rw.WriteHeader(http.StatusConflict)
		writeJSON(rw, map[string]string{"error": "ledger ordinal must advance and chain from the previous witnessed ordinal (rollback or fork refused)"})
		return
	}
	receipt := w.sign(req, st.ordinal, st.hash)
	st.ordinal, st.hash, st.receipt = req.LedgerOrdinal, req.LedgerHash, receipt
	writeJSON(rw, receipt)
}

// .
// .
func (w *Witness) SignReceipt(t testing.TB, req witness.WitnessRequest, prevOrdinal int64, prevHash string) witness.WitnessReceipt {
	t.Helper()
	return w.sign(req, prevOrdinal, prevHash)
}

func (w *Witness) sign(req witness.WitnessRequest, prevOrdinal int64, prevHash string) witness.WitnessReceipt {
	receipt := witness.WitnessReceipt{
		IdentityID:                     req.IdentityID,
		PreviousWitnessedLedgerOrdinal: prevOrdinal,
		PreviousWitnessedLedgerHash:    prevHash,
		LedgerOrdinal:                  req.LedgerOrdinal,
		LedgerHash:                     req.LedgerHash,
		WitnessedAt:                    time.Now().UTC().Format(time.RFC3339),
	}
	input := witness.ReceiptSignatureInput(receipt)
	sig, err := crypto.Sign(w.kp, input)
	if err != nil {
		panic("witnesstest: signing a receipt cannot fail: " + err.Error())
	}
	wm, _ := w.Env.FindPublicKey(witness.AlgMLDSA87)
	receipt.WitnessSignature = witness.SignatureEntry{
		SignatureProfile:     witness.ProfileFast,
		Alg:                  witness.AlgMLDSA87,
		KeyID:                w.KeyID,
		PublicKeyFingerprint: wm.PublicKeyFingerprint,
		SignatureInputSHA256: sigenvelope.SHA256Prefixed(input),
		SigB64:               base64.StdEncoding.EncodeToString(sig),
	}
	return receipt
}

func writeJSON(rw http.ResponseWriter, v interface{}) {
	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(v)
}

// .
// .
func DecodeB64(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }
