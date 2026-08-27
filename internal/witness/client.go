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

// Client speaks the ai3-witnessd bookmark protocol.
type Client struct {
	baseURL     string
	http        *http.Client // witness connections — pinned when configured
	genesisHTTP *http.Client // genesis-server downloads — NEVER pinned (different host, different cert)
	pinErr      error        // malformed configured pin — fail every request loudly
	genesisURL  string       // runtime platform-key source (downloaded per verification)
}

// New creates a witness client against baseURL (e.g. https://witness.aiii.id).
//
// TLS PINNING (canon WITNESS_PROTOCOL.md §11.1): when tlsSPKISHA256 is
// non-empty (hex or base64 of the SHA-256 of the server certificate's
// SubjectPublicKeyInfo), every TLS connection TO THE WITNESS must present
// that exact key — a re-issued or hostile certificate fails closed even
// if a CA signed it. Empty pin = standard WebPKI verification (stated
// default: TLS + domain + the pubkey/hash cross-check + optional dual-PQ
// manifest, NOT a pin).
//
// The pin applies to the WITNESS only (finding 6, 2026-08-17 review):
// genesis-server downloads use a separate unpinned client — the platform
// pubkey comes from a different host with a different certificate, and
// pinning it to the witness key made pin+manifest deployments fail TLS
// on every anchor pass (anchoring permanently refused → DEGRADED
// forever). The platform key is still verified END-TO-END by the dual-PQ
// manifest signatures — transport trust for its download is TLS+domain,
// same as an operator-path fetch.
func New(baseURL, tlsSPKISHA256 string) *Client {
	pinned := &http.Client{Timeout: 30 * time.Second}
	if tlsSPKISHA256 != "" {
		pin, err := decodePin(tlsSPKISHA256)
		if err != nil {
			pinned = nil // validated below
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
		c.http = nil // callers see the failure on first use via pinError
		c.pinErr = fmt.Errorf("witness TLS pin malformed: %q", tlsSPKISHA256)
	}
	return c
}

// NewWithRoots is New with a custom root pool (tests with self-signed
// servers; production uses New and the system roots).
func NewWithRoots(baseURL, tlsSPKISHA256 string, roots *x509.CertPool) *Client {
	c := New(baseURL, tlsSPKISHA256)
	if c.http != nil {
		if tr, ok := c.http.Transport.(*http.Transport); ok && tr != nil && tr.TLSClientConfig != nil {
			tr.TLSClientConfig.RootCAs = roots
		}
	}
	return c
}

// decodePin accepts hex (64 chars, optional sha256: prefix) or base64.
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

// spkiPinVerifier returns a VerifyPeerCertificate that requires the leaf's
// SPKI SHA-256 to equal the pin. Standard verification still runs first
// (Go verifies the chain; we pin ON TOP of WebPKI, not instead of it).
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

// sha256Prefixed delegates to the shared grammar's digest rendering —
// HashPrefixSHA256 + lowercase hex, same bytes, one source.
func sha256Prefixed(data []byte) string { return sigenvelope.SHA256Prefixed(data) }

// FetchWitnessKey returns the witness public-key envelope, cross-checked
// against /witness/pubkey/hash (probe-level tamper check: the two
// endpoints must agree on the canonical hash and key_id). This is the
// ALWAYS layer of the trust model; VerifyManifest adds the platform
// layer when a platform key is configured.
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

// SetGenesisURL configures runtime key sourcing: when no platform key
// path is configured, VerifyManifest DOWNLOADS the platform key from the
// genesis server per verification (canon: downloaded over TLS, used
// in-memory, never stored locally — 2026-08-17 ruling).
func (c *Client) SetGenesisURL(genesisURL string) {
	c.genesisURL = genesisURL
}

// HasGenesisURL reports whether a runtime platform-key source is
// configured — the anchorer's signal that manifest verification is
// REQUIRED (path override OR canon genesis download; either way the
// witness key must be platform-vouched, never self-vouched).
func (c *Client) HasGenesisURL() bool {
	return c.genesisURL != ""
}

// VerifyManifest fetches /witness/pubkey/manifest and verifies it against
// the AIII platform key envelope at platformPubkeyPath (dual-PQ,
// ProfileRoot — the same root that signs RING0). On success the served
// witness envelope must match the manifest's key_id and ML-DSA
// fingerprint: the platform vouches for this exact witness key.
//
// The manifest bundle is an AIII-SIGNATURE-V1 envelope (ML-DSA-87 +
// SLH-DSA over the canonical payload hash — the same grammar RING0
// verification uses), verified via internal/sigenvelope; the signature
// input string witnessd's signer tooling produces IS
// sigenvelope.SignatureInput.
func (c *Client) VerifyManifest(witnessKey *PublicKeyEnvelope, platformPubkeyPath string) error {
	raw, err := c.getBytes("/witness/pubkey/manifest")
	if err != nil {
		return fmt.Errorf("witness manifest fetch: %w", err)
	}

	var platformJSON []byte
	if platformPubkeyPath != "" {
		// Operator override (their machine, their choice; not shipped).
		platformJSON, err = readFileTrimmed(platformPubkeyPath)
		if err != nil {
			return fmt.Errorf("platform pubkey: %w", err)
		}
	} else {
		// Canon source: download the platform key from genesis, per
		// verification, in-memory only. Failure = verification refused
		// (which refuses the anchor pass — fail-closed, no fallback).
		if c.genesisURL == "" {
			return fmt.Errorf("no platform key source (no path, no genesis URL) — manifest verification refused")
		}
		platformJSON, err = c.getBytesFrom(c.genesisURL, "/genesis/pubkey")
		if err != nil {
			return fmt.Errorf("platform key download from genesis: %w", err)
		}
	}
	var platformEnv PublicKeyEnvelope
	if err := json.Unmarshal(platformJSON, &platformEnv); err != nil {
		return fmt.Errorf("parse platform pubkey envelope: %w", err)
	}

	// Platform envelope contract (genesis parity: the same
	// ValidatePublicKeyEnvelope genesis runs on this exact artifact at
	// birth — v/profile/key_id, validity window, fingerprint binding of
	// every key entry). The witness download path skipped this until the
	// 2026-08-19 sigenvelope consolidation (finding-9 analog).
	if err := sigenvelope.ValidatePublicKeyEnvelope(&platformEnv, ProfileRoot); err != nil {
		return fmt.Errorf("platform pubkey envelope: %w", err)
	}

	// Envelope verification via the one AIII-SIGNATURE-V1 grammar
	// (internal/sigenvelope): kind/canonicalization/profile gates,
	// canonical payload hash, EXACT dual-PQ signature set — every entry
	// verifying, duplicates and extras rejected. Until 2026-08-19 this
	// was an inline near-copy; the deltas the delegation introduces are
	// tightenings shared with the reference verifier (ai3-bundle):
	// duplicate JSON member names reject, a duplicate-but-valid
	// signature entry rejects.
	payloadRaw, err := sigenvelope.VerifyPayload(raw, &platformEnv, "witness.public_key_manifest", ProfileRoot)
	if err != nil {
		return fmt.Errorf("witness manifest: %w", err)
	}

	// Payload validation — mirrors witnessd validateWitnessPublicKeyManifestPayload
	// (the server holds itself to this; the client holds the served
	// manifest to the same standard, "like AII OS or better").
	var payload struct {
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
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		return fmt.Errorf("parse manifest payload: %w", err)
	}
	if payload.Kind != "witness.public_key_manifest" {
		return fmt.Errorf("manifest payload kind %q", payload.Kind)
	}
	if payload.SchemaVersion != 1 {
		return fmt.Errorf("manifest schema_version %d", payload.SchemaVersion)
	}
	if payload.ArtifactVersion == "" {
		return fmt.Errorf("manifest artifact_version missing")
	}
	// artifact_hash must BE the served key envelope's canonical hash
	canonicalServed, err := canonicaljson.CanonicalizeV1(mustMarshalEnv(witnessKey))
	if err != nil {
		return fmt.Errorf("canonicalize served witness key: %w", err)
	}
	servedHash := sha256Prefixed(canonicalServed)
	if payload.ArtifactHash != servedHash {
		return fmt.Errorf("manifest artifact_hash does not match served public key (%s != %s)", payload.ArtifactHash, servedHash)
	}
	if payload.KeyID != witnessKey.KeyID || !strings.HasPrefix(payload.KeyID, "aiii_witness_") {
		return fmt.Errorf("manifest key_id does not match served public key")
	}
	if payload.SignatureProfile != ProfileRoot {
		return fmt.Errorf("manifest payload signature_profile %q", payload.SignatureProfile)
	}
	if !payload.Critical {
		return fmt.Errorf("manifest payload must be critical")
	}
	if err := validateManifestTimeWindow(payload.NotBefore, payload.ExpiresAt); err != nil {
		return fmt.Errorf("manifest window: %w", err)
	}
	for _, rk := range payload.RevokedKeyIDs {
		if rk == payload.KeyID {
			return fmt.Errorf("manifest revokes its own active key_id")
		}
	}
	for _, rh := range payload.RevokedArtifactHashes {
		if rh == payload.ArtifactHash {
			return fmt.Errorf("manifest revokes its own active artifact_hash")
		}
	}

	// the platform-vouched key must BE the served key (key_id + ML-DSA fingerprint)
	manifestMl, _ := witnessKey.FindPublicKey(AlgMLDSA87)
	if manifestMl.PublicKeyFingerprint == "" {
		return fmt.Errorf("served witness key has no ML-DSA-87 fingerprint")
	}
	return nil
}

// validateManifestTimeWindow mirrors witnessd validateWitnessManifestTimeWindow.
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
		return nil // unreachable for this struct; canonicalize will fail loudly
	}
	return raw
}

// Status returns server limits (used for range-window sizing).
func (c *Client) Status() (*WitnessStatus, error) {
	var st WitnessStatus
	if err := c.getJSON("/status", &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// BookmarkResult carries the receipt plus whether this was the identity's
// first anchor (201) or an advance/retry (200).
type BookmarkResult struct {
	Receipt WitnessReceipt
	First   bool
}

// The server's conflict vocabulary — witnessd store.go emits these exact
// strings through its conflict() constructor (types.go:242-244) and the
// handler renders them as {"error": <message>} (server.go:377-378
// writeJSONError). The MESSAGE is the protocol's only conflict
// discriminator; there is no machine code field.
const (
	// conflictMsgCadence — store.go:381: the bookmark advanced by fewer
	// events than the hosted minimum cadence. The one 409 that is NOT an
	// integrity signal.
	conflictMsgCadence = "witness bookmark cadence below hosted minimum"
	// conflictMsgRollbackFork — store.go:375: ledger_ordinal <= the
	// durably persisted last-tail (the server-side monotonic guard).
	conflictMsgRollbackFork = "witness bookmark rollback or fork"
	// conflictMsgIdentityMismatch — store.go:362: the submitted identity
	// public key hash differs from the registered one.
	conflictMsgIdentityMismatch = "identity public key does not match registered witness identity"
)

// ConflictError is a bookmark 409: the witness REFUSED the anchor as a
// protocol conflict — a different failure class from transport errors,
// because the server's durable per-identity state (Postgres last-tail;
// witnessd store.go:375 monotonic guard, :362 identity binding) is
// DISAGREEING with ours. Local means the anchorer's own chain-continuity
// check synthesized it (client-side fork detection) rather than the
// server returning 409.
type ConflictError struct {
	Message string // the server's {"error": ...} body, or the local check's finding
	Local   bool
}

func (e *ConflictError) Error() string {
	origin := "witness 409"
	if e.Local {
		origin = "local witness-state check"
	}
	return fmt.Sprintf("witness conflict (%s): %s", origin, e.Message)
}

// IsCadence reports whether this conflict is the server's cadence gate —
// an operational pacing refusal that self-heals as events accumulate,
// never an integrity signal. Everything else (rollback/fork, identity
// mismatch, reconstruction conflicts, and any message this client does
// not recognize) is treated as integrity: the unknown-conflict default
// leans toward the alarming interpretation, because a witness that
// refuses for a reason we cannot classify is exactly the situation the
// alarm exists for.
func (e *ConflictError) IsCadence() bool {
	return !e.Local && e.Message == conflictMsgCadence
}

// newConflictError parses the 409 body. The wire shape is JSON
// {"error": <message>} (witnessd server.go:377-378); anything else is
// carried raw so no conflict is ever silently flattened.
func newConflictError(body []byte) *ConflictError {
	var wire struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &wire); err == nil && wire.Error != "" {
		return &ConflictError{Message: wire.Error}
	}
	return &ConflictError{Message: strings.TrimSpace(string(body))}
}

// Bookmark submits a signed request. 201/200 with a verifiable receipt
// are success; 409 comes back as a typed *ConflictError (rollback/fork/
// cadence — the caller must NOT lump it with transport failures);
// everything else is an ordinary error.
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

// VerifyReceipt checks a receipt against the witness key over the exact
// receipt signature input: profile, algorithm, key identity, input hash,
// and ML-DSA-87 signature. Echoed fields must match the request. A
// receipt that fails any check is never persisted by the anchorer.
func VerifyReceipt(receipt WitnessReceipt, req WitnessRequest, witnessKey *PublicKeyEnvelope) error {
	if receipt.WitnessVersion != WitnessVersion {
		return fmt.Errorf("receipt witness_version %q", receipt.WitnessVersion)
	}
	if receipt.IdentityID != req.IdentityID {
		return fmt.Errorf("receipt identity_id mismatch")
	}
	if receipt.LedgerOrdinal != req.LedgerOrdinal || receipt.LedgerHash != req.LedgerHash {
		return fmt.Errorf("receipt ledger fields do not match request")
	}
	if receipt.RangeStartOrdinal != req.RangeStartOrdinal || receipt.RangeHash != req.RangeHash {
		return fmt.Errorf("receipt range fields do not match request")
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

// --- HTTP helpers ---

func (c *Client) getJSON(path string, out interface{}) error {
	body, err := c.getBytes(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

func (c *Client) getBytesFrom(baseURL, path string) ([]byte, error) {
	// Genesis downloads ride the UNPINNED client (finding 6): the pin is
	// the witness server's key — a different host must not be held to it.
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
