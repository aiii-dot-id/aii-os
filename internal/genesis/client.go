// Package genesis implements AII OS identity birth.
//
// The genesis client fetches and verifies founding artifacts (RING0 bundle,
// Ring 5 bundle, bootstrap packet) from AII OS hosted infrastructure.
// ROOT-profile verification requires ML-DSA-87 and SLH-DSA-SHA2-256s.
package genesis

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

const (
	fetchAttempts  = 3
	fetchBackoff   = 2 * time.Second
	maxBundleBytes = 1 << 20
)

func (c *GenesisClient) getWithRetry(req *http.Request) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < fetchAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * fetchBackoff)
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode >= 500 {
			resp.Body.Close()
			lastErr = fmt.Errorf("%s returned %d", req.URL.Path, resp.StatusCode)
			continue
		}
		return resp, nil
	}
	return nil, lastErr
}

type publicKeyEnvelope = sigenvelope.PublicKeyEnvelope

type ring5Manifest struct {
	Kind                     string   `json:"kind"`
	SchemaVersion            int      `json:"schema_version"`
	ArtifactVersion          string   `json:"artifact_version"`
	ArtifactHash             string   `json:"artifact_hash"`
	KeyID                    string   `json:"key_id"`
	SignatureProfile         string   `json:"signature_profile"`
	NotBefore                string   `json:"not_before"`
	ExpiresAt                string   `json:"expires_at"`
	Critical                 bool     `json:"critical"`
	SupersedesArtifactHashes []string `json:"supersedes_artifact_hashes"`
	RevokedKeyIDs            []string `json:"revoked_key_ids"`
	RevokedArtifactHashes    []string `json:"revoked_artifact_hashes"`
}

// --- GenesisClient ---

// GenesisClient fetches and verifies AII OS founding artifacts.
type GenesisClient struct {
	genesisURL   string
	firewallURL  string
	bootstrapURL string
	token        string // X-Genesis-Token from the Ring 0 fetch; sent only for the gated bootstrap packet
	httpClient   *http.Client

	rootOverride *publicKeyEnvelope
}

// NewClient creates a genesis client.
func NewClient(genesisURL, firewallURL, bootstrapURL string) *GenesisClient {
	return &GenesisClient{
		genesisURL:   strings.TrimRight(genesisURL, "/"),
		firewallURL:  strings.TrimRight(firewallURL, "/"),
		bootstrapURL: strings.TrimRight(bootstrapURL, "/"),
		httpClient: &http.Client{
			Timeout:       30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func (c *GenesisClient) trustRoot() *publicKeyEnvelope {
	if c.rootOverride != nil {
		return c.rootOverride
	}
	return pinnedRoot()
}

// FetchResult holds a verified artifact.
type FetchResult struct {
	Content string // The verified markdown/text content
	Bundle  []byte // Raw bundle bytes (for provenance)
	Token   string // X-Genesis-Token (if present)
}

// FetchRing0 fetches Ring 0 and verifies it against the shipped root.
func (c *GenesisClient) FetchRing0() (*FetchResult, error) {
	if c.genesisURL == "" {
		return nil, fmt.Errorf("no genesis server configured")
	}

	bundleBytes, token, err := c.fetchBundle(c.genesisURL, "ring0")
	if err != nil {
		return nil, fmt.Errorf("genesis server error: %w", err)
	}

	content, err := verifyBundle(bundleBytes, c.trustRoot(), "ring0.bundle")
	if err != nil {
		return nil, fmt.Errorf("RING0 bundle verification failed (bundle not signed by the shipped AIII root — server inauthentic, or this binary is outdated): %w", err)
	}

	return &FetchResult{
		Content: content,
		Bundle:  bundleBytes,
		Token:   token,
	}, nil
}

// FetchRing5 requires the Ring 5 key chain, bundle, and current manifest.
func (c *GenesisClient) FetchRing5() (*FetchResult, error) {
	if c.firewallURL == "" {
		return nil, fmt.Errorf("no firewall server configured")
	}

	pubkey, err := c.resolveDomainKey(c.firewallURL, "ring5.pubkey", c.trustRoot())
	if err != nil {
		return nil, err
	}

	bundleBytes, token, err := c.fetchBundle(c.firewallURL, "ring5")
	if err != nil {
		return nil, fmt.Errorf("firewall server error: %w", err)
	}

	content, err := verifyBundle(bundleBytes, pubkey, "ring5.bundle")
	if err != nil {
		return nil, fmt.Errorf("Ring 5 bundle verification failed: %w", err)
	}
	manifestBytes, _, err := c.fetchBundle(c.firewallURL, "ring5.manifest")
	if err != nil {
		return nil, fmt.Errorf("Ring 5 manifest fetch failed: %w", err)
	}
	manifestPayload, err := verifyBundlePayload(manifestBytes, pubkey, "ring5.manifest")
	if err != nil {
		return nil, fmt.Errorf("Ring 5 manifest verification failed: %w", err)
	}
	if err := validateRing5Manifest(manifestPayload, bundleBytes, pubkey, time.Now().UTC()); err != nil {
		return nil, fmt.Errorf("Ring 5 manifest invalid: %w", err)
	}

	return &FetchResult{
		Content: content,
		Bundle:  bundleBytes,
		Token:   token,
	}, nil
}

// FetchBootstrap requires the bootstrap key chain and packet.
func (c *GenesisClient) FetchBootstrap() (*FetchResult, error) {
	if c.bootstrapURL == "" {
		return nil, fmt.Errorf("no bootstrap server configured")
	}

	pubkey, err := c.resolveDomainKey(c.bootstrapURL, "bootstrap.pubkey", c.trustRoot())
	if err != nil {
		return nil, err
	}

	bundleBytes, token, err := c.fetchBundle(c.bootstrapURL, "bootstrap")
	if err != nil {
		return nil, fmt.Errorf("bootstrap server error: %w", err)
	}

	content, err := verifyBundle(bundleBytes, pubkey, "bootstrap.packet")
	if err != nil {
		return nil, fmt.Errorf("bootstrap packet verification failed: %w", err)
	}
	return &FetchResult{
		Content: content,
		Bundle:  bundleBytes,
		Token:   token,
	}, nil
}

// resolveDomainKey requires the cross-signed domain-key bundle and verifies it
// against the shipped Ring 0 root.
func (c *GenesisClient) resolveDomainKey(baseURL, kind string, root *publicKeyEnvelope) (*publicKeyEnvelope, error) {
	chainBytes, _, err := c.fetchBundle(baseURL, kind)
	if err != nil {
		return nil, fmt.Errorf("%s domain-key chain unavailable: %w", kind, err)
	}
	content, err := verifyBundlePayload(chainBytes, root, kind)
	if err != nil {
		return nil, fmt.Errorf("%s domain-key chain verification failed (root did not sign this key): %w", kind, err)
	}
	var domainKey publicKeyEnvelope
	if err := json.Unmarshal(content, &domainKey); err != nil {
		return nil, fmt.Errorf("%s domain-key envelope unreadable: %w", kind, err)
	}
	keyType := strings.TrimSuffix(kind, ".pubkey")
	if err := validateDomainKey(&domainKey, keyType); err != nil {
		return nil, fmt.Errorf("%s domain-key envelope invalid: %w", kind, err)
	}
	return &domainKey, nil
}

// SetToken installs the genesis session token used to fetch the bootstrap
// packet. Ring 5 and domain-key bundles are public self-proving material.
func (c *GenesisClient) SetToken(t string) { c.token = t }

// Root returns the shipped trust root used again at the mint.
func (c *GenesisClient) Root() *publicKeyEnvelope { return c.trustRoot() }

// SetTrustRootForTest replaces the shipped root in tests only.
func (c *GenesisClient) SetTrustRootForTest(root *publicKeyEnvelope) { c.rootOverride = root }

func (c *GenesisClient) fetchBundle(baseURL, kind string) ([]byte, string, error) {
	var url string
	switch kind {
	case "ring0":
		url = baseURL + "/genesis/bundle"
	case "ring5":
		url = baseURL + "/v1/ring5/bundle"
	case "ring5.manifest":
		url = baseURL + "/v1/ring5/manifest"
	case "bootstrap.pubkey":
		url = baseURL + "/bootstrap/pubkey.bundle"
	case "ring5.pubkey":
		url = baseURL + "/v1/ring5/pubkey.bundle"
	case "bootstrap":
		url = baseURL + "/bootstrap"
	default:
		return nil, "", fmt.Errorf("unknown bundle kind: %s", kind)
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, "", err
	}
	if kind == "bootstrap" && c.token != "" {
		req.Header.Set("X-Genesis-Token", c.token)
	}
	resp, err := c.getWithRetry(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, "", fmt.Errorf("bundle fetch returned %d: %s", resp.StatusCode, string(body))
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBundleBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("read bundle response: %w", err)
	}
	if len(data) > maxBundleBytes {
		return nil, "", fmt.Errorf("bundle response exceeds %d bytes", maxBundleBytes)
	}

	token := resp.Header.Get("X-Genesis-Token")

	return data, token, nil
}

// verifyBundle verifies a signed text artifact and its exact payload shape.
func verifyBundle(bundleBytes []byte, pubkey *publicKeyEnvelope, expectedKind string) (string, error) {
	raw, err := verifyBundlePayload(bundleBytes, pubkey, expectedKind)
	if err != nil {
		return "", err
	}
	switch expectedKind {
	case "ring0.bundle":
		var p struct {
			Laws         string `json:"laws"`
			Purpose      string `json:"purpose"`
			Ring0Version int    `json:"ring0_version"`
			Ordinal      int    `json:"ordinal"`
			Timestamp    int64  `json:"timestamp"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return "", fmt.Errorf("parse Ring 0 payload: %w", err)
		}
		if p.Laws == "" || p.Purpose == "" ||
			p.Ring0Version <= 0 || p.Ordinal <= 0 || p.Timestamp <= 0 {
			return "", fmt.Errorf("Ring 0 payload requires non-empty laws and purpose, and positive ring0_version, ordinal, and timestamp")
		}
		return p.Laws, nil
	case "ring5.bundle":
		var p struct {
			Purpose      string `json:"purpose"`
			Ring5Version int    `json:"ring5_version"`
			Ordinal      int    `json:"ordinal"`
			Timestamp    int64  `json:"timestamp"`
			Posture      string `json:"posture"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return "", fmt.Errorf("parse Ring 5 payload: %w", err)
		}
		if strings.TrimSpace(p.Purpose) == "" || p.Ring5Version <= 0 || p.Ordinal <= 0 || p.Timestamp <= 0 || strings.TrimSpace(p.Posture) == "" {
			return "", fmt.Errorf("Ring 5 payload requires purpose, positive version, ordinal and timestamp, and non-empty posture")
		}
		return p.Posture, nil
	case "bootstrap.packet":
		var p struct {
			Kind              string `json:"kind"`
			BootstrapMarkdown string `json:"bootstrap_markdown"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return "", fmt.Errorf("parse bootstrap payload: %w", err)
		}
		if p.Kind != "bootstrap" || strings.TrimSpace(p.BootstrapMarkdown) == "" {
			return "", fmt.Errorf("bootstrap payload requires kind bootstrap and non-empty bootstrap_markdown")
		}
		return p.BootstrapMarkdown, nil
	default:
		return "", fmt.Errorf("unsupported text bundle kind %q", expectedKind)
	}
}

func validateDomainKey(key *publicKeyEnvelope, keyType string) error {
	if err := validatePubKeyEnvelope(key); err != nil {
		return err
	}
	if key.Kind != "aiii.server_key.public" {
		return fmt.Errorf("kind must be aiii.server_key.public")
	}
	if key.KeyType != keyType {
		return fmt.Errorf("key_type must be %s", keyType)
	}
	if !strings.HasPrefix(key.KeyID, "aiii_"+keyType+"_") {
		return fmt.Errorf("key_id must start with aiii_%s_", keyType)
	}
	if strings.TrimSpace(key.ExpiresAt) == "" {
		return fmt.Errorf("expires_at is required")
	}
	seen := make(map[string]bool, 2)
	for _, material := range key.Keys {
		if material.Alg != crypto.SigAlg && material.Alg != crypto.SLHAlg {
			return fmt.Errorf("unsupported %s public key algorithm %q", keyType, material.Alg)
		}
		seen[material.Alg] = true
	}
	for _, alg := range []string{crypto.SigAlg, crypto.SLHAlg} {
		if !seen[alg] {
			return fmt.Errorf("%s requires public key algorithm %s", crypto.ProfileRoot, alg)
		}
	}
	return nil
}

func validateRing5Manifest(payload json.RawMessage, bundle []byte, key *publicKeyEnvelope, now time.Time) error {
	var m ring5Manifest
	if err := json.Unmarshal(payload, &m); err != nil {
		return fmt.Errorf("parse payload: %w", err)
	}
	if m.Kind != "ring5.manifest" {
		return fmt.Errorf("kind must be ring5.manifest")
	}
	if m.SchemaVersion != 1 {
		return fmt.Errorf("unsupported schema_version %d", m.SchemaVersion)
	}
	if strings.TrimSpace(m.ArtifactVersion) == "" {
		return fmt.Errorf("artifact_version is required")
	}
	if want := sigenvelope.SHA256Prefixed(bundle); m.ArtifactHash != want {
		return fmt.Errorf("artifact_hash does not match Ring 5 bundle")
	}
	if m.KeyID != key.KeyID {
		return fmt.Errorf("key_id %q does not match Ring 5 key %q", m.KeyID, key.KeyID)
	}
	if m.SignatureProfile != crypto.ProfileRoot {
		return fmt.Errorf("signature_profile must be %s", crypto.ProfileRoot)
	}
	if !m.Critical {
		return fmt.Errorf("manifest must be critical")
	}
	for _, hash := range m.SupersedesArtifactHashes {
		if strings.TrimSpace(hash) == "" {
			return fmt.Errorf("supersedes_artifact_hashes contains an empty value")
		}
	}
	for _, keyID := range m.RevokedKeyIDs {
		if strings.TrimSpace(keyID) == "" {
			return fmt.Errorf("revoked_key_ids contains an empty value")
		}
		if keyID == m.KeyID {
			return fmt.Errorf("manifest revokes active key_id %s", keyID)
		}
	}
	for _, hash := range m.RevokedArtifactHashes {
		if strings.TrimSpace(hash) == "" {
			return fmt.Errorf("revoked_artifact_hashes contains an empty value")
		}
		if hash == m.ArtifactHash {
			return fmt.Errorf("manifest revokes active artifact_hash %s", hash)
		}
	}
	if strings.TrimSpace(m.NotBefore) != "" {
		notBefore, err := time.Parse(time.RFC3339, m.NotBefore)
		if err != nil {
			return fmt.Errorf("invalid not_before: %w", err)
		}
		if now.Before(notBefore) {
			return fmt.Errorf("manifest not yet valid")
		}
	}
	if strings.TrimSpace(m.ExpiresAt) == "" {
		return fmt.Errorf("expires_at is required")
	}
	expires, err := time.Parse(time.RFC3339, m.ExpiresAt)
	if err != nil {
		return fmt.Errorf("invalid expires_at: %w", err)
	}
	if !now.Before(expires) {
		return fmt.Errorf("manifest expired")
	}
	return nil
}

// verifyBundlePayload verifies the AIII-SIGNATURE-V1 envelope via the
// shared grammar. Genesis accepts exactly ProfileRoot — the platform
// signs founding artifacts with both PQ algorithms; nothing weaker
// verifies a birth.
func verifyBundlePayload(bundleBytes []byte, pubkey *publicKeyEnvelope, expectedKind string) (json.RawMessage, error) {
	return sigenvelope.VerifyPayload(bundleBytes, pubkey, expectedKind, crypto.ProfileRoot)
}

// --- Helpers ---

func validatePubKeyEnvelope(env *publicKeyEnvelope) error {
	return sigenvelope.ValidatePublicKeyEnvelope(env, crypto.ProfileRoot)
}
