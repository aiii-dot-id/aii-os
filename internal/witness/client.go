package witness

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// .
type Client struct {
	baseURL     string
	http        *http.Client
	genesisHTTP *http.Client
	pinErr      error
	genesisURL  string
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func NewPinnedHTTPClient(tlsSPKISHA256 string, timeout time.Duration) (*http.Client, error) {
	hc := &http.Client{Timeout: timeout}
	if tlsSPKISHA256 == "" {
		return hc, nil
	}
	pin, err := decodePin(tlsSPKISHA256)
	if err != nil {
		return nil, fmt.Errorf("TLS pin malformed: %q", tlsSPKISHA256)
	}
	hc.Transport = &http.Transport{TLSClientConfig: &tls.Config{VerifyPeerCertificate: spkiPinVerifier(pin), MinVersion: tls.VersionTLS12}}
	return hc, nil
}

func New(baseURL, tlsSPKISHA256 string) *Client {
	pinned := &http.Client{Timeout: 30 * time.Second}
	if tlsSPKISHA256 != "" {
		pin, err := decodePin(tlsSPKISHA256)
		if err != nil {
			pinned = nil
		} else {
			pinned.Transport = &http.Transport{
				TLSClientConfig: &tls.Config{
					VerifyPeerCertificate: spkiPinVerifier(pin),
					MinVersion:            tls.VersionTLS12,
				},
			}
		}
	}
	c := &Client{
		baseURL:     trimTrailingSlash(baseURL),
		http:        pinned,
		genesisHTTP: &http.Client{Timeout: 30 * time.Second},
	}
	if tlsSPKISHA256 != "" && pinned == nil {
		c.http = nil
		c.pinErr = fmt.Errorf("witness TLS pin malformed: %q", tlsSPKISHA256)
	}
	return c
}

// .
// .
func NewWithRoots(baseURL, tlsSPKISHA256 string, roots *x509.CertPool) *Client {
	c := New(baseURL, tlsSPKISHA256)
	if c.http != nil {
		if tr, ok := c.http.Transport.(*http.Transport); ok && tr != nil && tr.TLSClientConfig != nil {
			tr.TLSClientConfig.RootCAs = roots
		}
	}
	return c
}

// .
func decodePin(s string) ([]byte, error) {
	s = strings.TrimPrefix(s, "sha256:")
	if len(s) == 64 {
		if b, err := hex.DecodeString(s); err == nil {
			return b, nil
		}
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	return nil, fmt.Errorf("not a 32-byte SPKI SHA-256 (hex or base64)")
}

// .
// .
// .
func spkiPinVerifier(pin []byte) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return fmt.Errorf("no certificate presented")
		}
		cert, err := x509.ParseCertificate(rawCerts[0])
		if err != nil {
			return err
		}
		sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
		if !bytes.Equal(sum[:], pin) {
			return fmt.Errorf("TLS SPKI pin mismatch: certificate key is not the pinned witness key")
		}
		return nil
	}
}

func trimTrailingSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

// .
// .
func sha256Prefixed(data []byte) string { return sigenvelope.SHA256Prefixed(data) }

// .
// .
// .
// .
// .
func (c *Client) FetchWitnessKey() (*PublicKeyEnvelope, error) {
	var hashResp struct {
		WitnessPublicKeyHash string `json:"witness_public_key_hash"`
		KeyID                string `json:"key_id"`
	}
	if err := c.getJSON("/witness/pubkey/hash", &hashResp); err != nil {
		return nil, fmt.Errorf("witness pubkey hash: %w", err)
	}

	raw, err := c.getBytes("/witness/pubkey")
	if err != nil {
		return nil, fmt.Errorf("witness pubkey: %w", err)
	}
	canonical, err := canonicaljson.CanonicalizeV1(raw)
	if err != nil {
		return nil, fmt.Errorf("canonicalize witness pubkey: %w", err)
	}
	if got := sha256Prefixed(canonical); got != hashResp.WitnessPublicKeyHash {
		return nil, fmt.Errorf("witness pubkey hash mismatch: %s != %s (endpoints disagree — tampering or misconfiguration)", got, hashResp.WitnessPublicKeyHash)
	}
	env := &PublicKeyEnvelope{}
	if err := json.Unmarshal(canonical, env); err != nil {
		return nil, fmt.Errorf("parse witness pubkey: %w", err)
	}
	if env.KeyID != hashResp.KeyID {
		return nil, fmt.Errorf("witness key_id mismatch: %s != %s", env.KeyID, hashResp.KeyID)
	}
	return env, nil
}

// .
// .
// .
// .
func (c *Client) SetGenesisURL(genesisURL string) {
	c.genesisURL = genesisURL
}

// .
// .
// .
// .
func (c *Client) HasGenesisURL() bool {
	return c.genesisURL != ""
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func (c *Client) VerifyManifest(witnessKey *PublicKeyEnvelope, platformPubkeyPath string) ([]byte, error) {
	raw, err := c.getBytes("/witness/pubkey/manifest")
	if err != nil {
		return nil, fmt.Errorf("witness manifest fetch: %w", err)
	}

	var platformJSON []byte
	if platformPubkeyPath != "" {
		// .
		platformJSON, err = readFileTrimmed(platformPubkeyPath)
		if err != nil {
			return nil, fmt.Errorf("platform pubkey: %w", err)
		}
	} else {
		// .
		// .
		// .
		if c.genesisURL == "" {
			return nil, fmt.Errorf("no platform key source (no path, no genesis URL) — manifest verification refused")
		}
		platformJSON, err = c.getBytesFrom(c.genesisURL, "/genesis/pubkey")
		if err != nil {
			return nil, fmt.Errorf("platform key download from genesis: %w", err)
		}
	}
	var platformEnv PublicKeyEnvelope
	if err := json.Unmarshal(platformJSON, &platformEnv); err != nil {
		return nil, fmt.Errorf("parse platform pubkey envelope: %w", err)
	}
	if _, err := verifyManifestBundle(raw, &platformEnv, witnessKey, true); err != nil {
		return nil, err
	}
	return raw, nil
}

// .
// .
type manifestPayload struct {
	Kind                  string   `json:"kind"`
	SchemaVersion         int      `json:"schema_version"`
	ArtifactVersion       string   `json:"artifact_version"`
	ArtifactHash          string   `json:"artifact_hash"`
	KeyID                 string   `json:"key_id"`
	SignatureProfile      string   `json:"signature_profile"`
	CreatedAt             string   `json:"created_at"`
	NotBefore             string   `json:"not_before,omitempty"`
	ExpiresAt             string   `json:"expires_at"`
	Critical              bool     `json:"critical"`
	RevokedKeyIDs         []string `json:"revoked_key_ids,omitempty"`
	RevokedArtifactHashes []string `json:"revoked_artifact_hashes,omitempty"`
}

// .
// .
// .
// .
// .
func verifyManifestBundle(raw []byte, platformEnv *PublicKeyEnvelope, witnessKey *PublicKeyEnvelope, checkWindow bool) (*manifestPayload, error) {
	// .
	// .
	// .
	// .
	// .
	if err := sigenvelope.ValidatePublicKeyEnvelope(platformEnv, ProfileRoot); err != nil {
		return nil, fmt.Errorf("platform pubkey envelope: %w", err)
	}

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	payloadRaw, err := sigenvelope.VerifyPayload(raw, platformEnv, "witness.public_key_manifest", ProfileRoot)
	if err != nil {
		return nil, fmt.Errorf("witness manifest: %w", err)
	}

	// .
	// .
	// .
	var payload manifestPayload
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		return nil, fmt.Errorf("parse manifest payload: %w", err)
	}
	if payload.Kind != "witness.public_key_manifest" {
		return nil, fmt.Errorf("manifest payload kind %q", payload.Kind)
	}
	if payload.SchemaVersion != 1 {
		return nil, fmt.Errorf("manifest schema_version %d", payload.SchemaVersion)
	}
	if payload.ArtifactVersion == "" {
		return nil, fmt.Errorf("manifest artifact_version missing")
	}
	// .
	canonicalServed, err := canonicalEnvelopeBytes(witnessKey)
	if err != nil {
		return nil, fmt.Errorf("canonicalize served witness key: %w", err)
	}
	servedHash := sha256Prefixed(canonicalServed)
	if payload.ArtifactHash != servedHash {
		return nil, fmt.Errorf("manifest artifact_hash does not match served public key (%s != %s)", payload.ArtifactHash, servedHash)
	}
	if payload.KeyID != witnessKey.KeyID || !strings.HasPrefix(payload.KeyID, "aiii_witness_") {
		return nil, fmt.Errorf("manifest key_id does not match served public key")
	}
	if payload.SignatureProfile != ProfileRoot {
		return nil, fmt.Errorf("manifest payload signature_profile %q", payload.SignatureProfile)
	}
	if !payload.Critical {
		return nil, fmt.Errorf("manifest payload must be critical")
	}
	if checkWindow {
		if err := validateManifestTimeWindow(payload.NotBefore, payload.ExpiresAt); err != nil {
			return nil, fmt.Errorf("manifest window: %w", err)
		}
	} else if payload.ExpiresAt == "" {
		return nil, fmt.Errorf("manifest window: expires_at missing")
	}
	for _, rk := range payload.RevokedKeyIDs {
		if rk == payload.KeyID {
			return nil, fmt.Errorf("manifest revokes its own active key_id")
		}
	}
	for _, rh := range payload.RevokedArtifactHashes {
		if rh == payload.ArtifactHash {
			return nil, fmt.Errorf("manifest revokes its own active artifact_hash")
		}
	}

	// .
	manifestMl, _ := witnessKey.FindPublicKey(AlgMLDSA87)
	if manifestMl.PublicKeyFingerprint == "" {
		return nil, fmt.Errorf("served witness key has no ML-DSA-87 fingerprint")
	}
	return &payload, nil
}

// .
// .
func canonicalEnvelopeBytes(env *PublicKeyEnvelope) ([]byte, error) {
	return canonicaljson.CanonicalizeV1(mustMarshalEnv(env))
}

// .
func validateManifestTimeWindow(notBefore, expiresAt string) error {
	now := time.Now().UTC()
	if notBefore != "" {
		nb, err := time.Parse(time.RFC3339, notBefore)
		if err != nil {
			return fmt.Errorf("not_before unparseable: %w", err)
		}
		if now.Before(nb) {
			return fmt.Errorf("not yet valid (not_before %s)", notBefore)
		}
	}
	exp, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return fmt.Errorf("expires_at unparseable: %w", err)
	}
	if !now.Before(exp) {
		return fmt.Errorf("expired at %s", expiresAt)
	}
	return nil
}

func mustMarshalEnv(env *PublicKeyEnvelope) []byte {
	raw, err := json.Marshal(env)
	if err != nil {
		return nil
	}
	return raw
}

// .
func (c *Client) Status() (*WitnessStatus, error) {
	var st WitnessStatus
	if err := c.getJSON("/status", &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// .
// .
type BookmarkResult struct {
	Receipt WitnessReceipt
	First   bool
}

// .
// .
// .
// .
// .
const (
	// .
	// .
	// .
	conflictMsgCadence = "witness bookmark cadence below hosted minimum"
	// .
	// .
	conflictMsgRollbackFork = "witness bookmark rollback or fork"
	// .
	// .
	conflictMsgIdentityMismatch = "identity public key does not match registered witness identity"
)

// .
// .
// .
// .
// .
// .
// .
type ConflictError struct {
	Message string
	Local   bool
}

func (e *ConflictError) Error() string {
	origin := "witness 409"
	if e.Local {
		origin = "local witness-state check"
	}
	return fmt.Sprintf("witness conflict (%s): %s", origin, e.Message)
}

// .
// .
// .
// .
// .
// .
// .
// .
func (e *ConflictError) IsCadence() bool {
	return !e.Local && e.Message == conflictMsgCadence
}

// .
// .
// .
func newConflictError(body []byte) *ConflictError {
	var wire struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &wire); err == nil && wire.Error != "" {
		return &ConflictError{Message: wire.Error}
	}
	return &ConflictError{Message: strings.TrimSpace(string(body))}
}

// .
// .
// .
// .
func (c *Client) Bookmark(req WitnessRequest) (*BookmarkResult, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if c.pinErr != nil {
		return nil, c.pinErr
	}
	httpReq, err := http.NewRequest("POST", c.baseURL+"/witness/bookmark", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("witness request failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == StatusConflict {
		return nil, newConflictError(respBody)
	}
	if resp.StatusCode != StatusCreated && resp.StatusCode != StatusOK {
		return nil, fmt.Errorf("witness returned %d: %s", resp.StatusCode, string(respBody))
	}
	var receipt WitnessReceipt
	if err := json.Unmarshal(respBody, &receipt); err != nil {
		return nil, fmt.Errorf("decode receipt: %w", err)
	}
	return &BookmarkResult{Receipt: receipt, First: resp.StatusCode == StatusCreated}, nil
}

// .
// .
// .
// .
func VerifyReceipt(receipt WitnessReceipt, req WitnessRequest, witnessKey *PublicKeyEnvelope) error {
	if receipt.IdentityID != req.IdentityID {
		return fmt.Errorf("receipt identity_id mismatch")
	}
	if receipt.LedgerOrdinal != req.LedgerOrdinal || receipt.LedgerHash != req.LedgerHash {
		return fmt.Errorf("receipt ledger fields do not match request")
	}
	sig := receipt.WitnessSignature
	if sig.SignatureProfile != ProfileFast {
		return fmt.Errorf("receipt signature_profile %q", sig.SignatureProfile)
	}
	if sig.Alg != AlgMLDSA87 {
		return fmt.Errorf("receipt signature alg %q", sig.Alg)
	}
	if sig.KeyID != witnessKey.KeyID {
		return fmt.Errorf("receipt key_id %q does not match witness key", sig.KeyID)
	}
	wm, ok := witnessKey.FindPublicKey(AlgMLDSA87)
	if !ok {
		return fmt.Errorf("witness key has no ML-DSA-87 material")
	}
	if sig.PublicKeyFingerprint != wm.PublicKeyFingerprint {
		return fmt.Errorf("receipt fingerprint does not match witness key")
	}
	input := ReceiptSignatureInput(receipt)
	if got := sha256Prefixed(input); got != sig.SignatureInputSHA256 {
		return fmt.Errorf("receipt signature_input_sha256 mismatch")
	}
	pubBytes, err := base64.StdEncoding.DecodeString(wm.PublicKeyB64)
	if err != nil {
		return fmt.Errorf("witness key decode: %w", err)
	}
	sigBytes, err := base64.StdEncoding.DecodeString(sig.SigB64)
	if err != nil {
		return fmt.Errorf("receipt signature decode: %w", err)
	}
	return crypto.Verify(pubBytes, input, sigBytes)
}

// .

func (c *Client) getJSON(path string, out interface{}) error {
	body, err := c.getBytes(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

func (c *Client) getBytesFrom(baseURL, path string) ([]byte, error) {
	// .
	// .
	if c.genesisHTTP == nil {
		return nil, fmt.Errorf("client misconfigured (no genesis transport)")
	}
	resp, err := c.genesisHTTP.Get(baseURL + path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("%s returned %d: %s", path, resp.StatusCode, string(body))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

func (c *Client) getBytes(path string) ([]byte, error) {
	if c.pinErr != nil {
		return nil, c.pinErr
	}
	resp, err := c.http.Get(c.baseURL + path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("%s returned %d: %s", path, resp.StatusCode, string(body))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}
