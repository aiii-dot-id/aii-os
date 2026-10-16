package genesis

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
	"github.com/aiii-dot-id/aii-os/internal/tlsfault"
)

const maxBundleBytes = 1 << 20

const (
	DefaultFetchAttempts = 3
	DefaultFetchTimeout  = 30 * time.Second
)

var fetchBackoff = 2 * time.Second

func FetchBackoff(attempt int) time.Duration { return time.Duration(attempt) * fetchBackoff }

type StatusError struct {
	Artifact   string
	StatusCode int
	Body       string
	RetryAfter time.Duration
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("bundle fetch returned %d: %s", e.StatusCode, e.Body)
}

type Refusal struct {
	URL               string
	Fault             *tlsfault.Fault
	Attempt, Attempts int
}

func (c *GenesisClient) getWithRetry(req *http.Request) (*http.Response, error) {
	var lastErr error
	var refused *tlsfault.Fault
	attempts := c.attempts
	if attempts <= 0 {
		attempts = DefaultFetchAttempts
	}
	tell := func(f *tlsfault.Fault, attempt int) {
		if c.refusalNotice != nil {
			c.refusalNotice(Refusal{URL: req.URL.Redacted(), Fault: f, Attempt: attempt, Attempts: attempts})
		}
	}
	ctx := req.Context()
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {

			pause := time.NewTimer(FetchBackoff(attempt))
			select {
			case <-pause.C:
			case <-ctx.Done():
				pause.Stop()
				return nil, fmt.Errorf("%s was not asked for again: %w", req.URL.Path, ctx.Err())
			}
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("%s: %w", req.URL.Path, ctx.Err())
			}

			lastErr = err
			if fault := tlsfault.Of(req.URL.Hostname(), err, c.roots()); fault != nil {
				lastErr, refused = fault, fault
				tell(fault, attempt+1)
			}

			if c.family != "" && familyUnavailable(err) {
				break
			}
			continue
		}
		if refused != nil {

			refused = nil
			tell(nil, attempt+1)
		}
		if resp.StatusCode >= 500 {
			resp.Body.Close()
			lastErr = fmt.Errorf("%s returned %d", req.URL.Path, resp.StatusCode)
			continue
		}
		return resp, nil
	}

	if refused != nil && !errors.Is(lastErr, refused) {
		return nil, fmt.Errorf("%w; before it, %w", lastErr, refused)
	}
	return nil, lastErr
}

func familyUnavailable(err error) bool {
	var noAddress *net.AddrError
	var lookup *net.DNSError
	return errors.Is(err, netUnreachable) || errors.As(err, &noAddress) || errors.As(err, &lookup) && lookup.IsNotFound
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

type GenesisClient struct {
	genesisURL   string
	firewallURL  string
	bootstrapURL string
	token        string
	httpClient   *http.Client
	family       string
	attempts     int

	refusalNotice func(Refusal)

	life context.Context

	rootOverride *publicKeyEnvelope
}

func NewClient(genesisURL, firewallURL, bootstrapURL string) *GenesisClient {
	return &GenesisClient{
		genesisURL:   strings.TrimRight(genesisURL, "/"),
		firewallURL:  strings.TrimRight(firewallURL, "/"),
		bootstrapURL: strings.TrimRight(bootstrapURL, "/"),
		httpClient: &http.Client{
			Timeout:       DefaultFetchTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func (c *GenesisClient) WithPatience(attempts int, timeout time.Duration) *GenesisClient {
	if attempts > 0 {
		c.attempts = attempts
	}
	if timeout > 0 {
		c.httpClient.Timeout = timeout
	}
	return c
}

func (c *GenesisClient) WithRefusalNotice(notice func(Refusal)) *GenesisClient {
	c.refusalNotice = notice
	return c
}

func (c *GenesisClient) WithLifetime(ctx context.Context) *GenesisClient {
	c.life = ctx
	return c
}

func (c *GenesisClient) lifetime() context.Context {
	if c.life != nil {
		return c.life
	}
	return context.Background()
}

func (c *GenesisClient) roots() *x509.CertPool {
	if tr, ok := c.httpClient.Transport.(*http.Transport); ok && tr != nil && tr.TLSClientConfig != nil {
		return tr.TLSClientConfig.RootCAs
	}
	return nil
}

func (c *GenesisClient) OverFamily(network string) *GenesisClient {
	transport := &http.Transport{}
	for _, rt := range []http.RoundTripper{c.httpClient.Transport, http.DefaultTransport} {
		if base, ok := rt.(*http.Transport); ok {
			transport = base.Clone()
			break
		}
	}
	dial := transport.DialContext
	if dial == nil {
		dial = (&net.Dialer{Timeout: 30 * time.Second}).DialContext
	}
	transport.DialContext = func(ctx context.Context, _, addr string) (net.Conn, error) {
		return dial(ctx, network, addr)
	}
	pinned, httpClient := *c, *c.httpClient
	httpClient.Transport = transport
	pinned.httpClient, pinned.family = &httpClient, network
	return &pinned
}

func (c *GenesisClient) trustRoot() (*publicKeyEnvelope, error) {
	if c.rootOverride != nil {
		return c.rootOverride, nil
	}
	return validPinnedRoot()
}

func validPinnedRoot() (*publicKeyEnvelope, error) {
	root := pinnedRoot()
	if err := sigenvelope.ValidWindow(root, time.Now()); err != nil {
		return nil, fmt.Errorf("the shipped AIII root does not admit this device's clock (check the clock): %w", err)
	}
	return root, nil
}

type FetchResult struct {
	Content string
	Bundle  []byte
	Token   string
}

func (c *GenesisClient) FetchRing0() (*FetchResult, error) {
	if c.genesisURL == "" {
		return nil, fmt.Errorf("no genesis server configured")
	}

	root, err := c.trustRoot()
	if err != nil {
		return nil, err
	}
	bundleBytes, token, err := c.fetchBundle(c.genesisURL, "ring0")
	if err != nil {
		return nil, fmt.Errorf("genesis server error: %w", err)
	}

	content, err := verifyBundle(bundleBytes, root, "ring0.bundle")
	if err != nil {
		return nil, fmt.Errorf("RING0 bundle verification failed (bundle not signed by the shipped AIII root — server inauthentic, or this binary is outdated): %w", err)
	}

	return &FetchResult{
		Content: content,
		Bundle:  bundleBytes,
		Token:   token,
	}, nil
}

func (c *GenesisClient) FetchRing5() (*FetchResult, error) {
	if c.firewallURL == "" {
		return nil, fmt.Errorf("no firewall server configured")
	}

	root, err := c.trustRoot()
	if err != nil {
		return nil, err
	}
	chainBytes, _, err := c.fetchBundle(c.firewallURL, "ring5.pubkey")
	if err != nil {
		return nil, fmt.Errorf("ring5.pubkey domain-key chain unavailable: %w", err)
	}
	bundleBytes, token, err := c.fetchBundle(c.firewallURL, "ring5")
	if err != nil {
		return nil, fmt.Errorf("firewall server error: %w", err)
	}
	manifestBytes, _, err := c.fetchBundle(c.firewallURL, "ring5.manifest")
	if err != nil {
		return nil, fmt.Errorf("Ring 5 manifest fetch failed: %w", err)
	}
	content, err := VerifyRing5(chainBytes, bundleBytes, manifestBytes, root, time.Now().UTC())
	if err != nil {
		return nil, err
	}

	return &FetchResult{
		Content: content,
		Bundle:  bundleBytes,
		Token:   token,
	}, nil
}

func VerifyRing5(chainBytes, bundleBytes, manifestBytes []byte, root *publicKeyEnvelope, now time.Time) (string, error) {
	pubkey, err := domainKeyFrom(chainBytes, "ring5.pubkey", root)
	if err != nil {
		return "", err
	}
	content, err := verifyBundle(bundleBytes, pubkey, "ring5.bundle")
	if err != nil {
		return "", fmt.Errorf("Ring 5 bundle verification failed: %w", err)
	}
	manifestPayload, err := verifyBundlePayload(manifestBytes, pubkey, "ring5.manifest")
	if err != nil {
		return "", fmt.Errorf("Ring 5 manifest verification failed: %w", err)
	}
	if err := validateRing5Manifest(manifestPayload, bundleBytes, pubkey, now); err != nil {
		return "", fmt.Errorf("Ring 5 manifest invalid: %w", err)
	}
	return content, nil
}

func (c *GenesisClient) FetchBootstrap() (*FetchResult, error) {
	if c.bootstrapURL == "" {
		return nil, fmt.Errorf("no bootstrap server configured")
	}

	root, err := c.trustRoot()
	if err != nil {
		return nil, err
	}
	pubkey, err := c.resolveDomainKey(c.bootstrapURL, "bootstrap.pubkey", root)
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

func (c *GenesisClient) resolveDomainKey(baseURL, kind string, root *publicKeyEnvelope) (*publicKeyEnvelope, error) {
	chainBytes, _, err := c.fetchBundle(baseURL, kind)
	if err != nil {
		return nil, fmt.Errorf("%s domain-key chain unavailable: %w", kind, err)
	}
	return domainKeyFrom(chainBytes, kind, root)
}

func domainKeyFrom(chainBytes []byte, kind string, root *publicKeyEnvelope) (*publicKeyEnvelope, error) {
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

func (c *GenesisClient) SetToken(t string) { c.token = t }

func (c *GenesisClient) Root() *publicKeyEnvelope {
	if c.rootOverride != nil {
		return c.rootOverride
	}
	return pinnedRoot()
}

func (c *GenesisClient) SetTrustRootForTest(root *publicKeyEnvelope) { c.rootOverride = root }

func (c *GenesisClient) SetTransportForTest(transport *http.Transport) {
	c.httpClient.Transport = transport
}

func SetFetchBackoffForTest(d time.Duration) (restore func()) {
	saved := fetchBackoff
	fetchBackoff = d
	return func() { fetchBackoff = saved }
}

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

	req, err := http.NewRequestWithContext(c.lifetime(), "GET", url, nil)
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

		wait, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		if wait < 0 || wait > 86400 {
			wait = 0
		}

		return nil, "", &StatusError{Artifact: kind, StatusCode: resp.StatusCode, Body: tlsfault.Printable(string(body), tlsfault.MaxSaid), RetryAfter: time.Duration(wait) * time.Second}
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

func VerifyArtifact(bundleBytes []byte, key *publicKeyEnvelope, expectedKind string) (string, error) {
	return verifyBundle(bundleBytes, key, expectedKind)
}

func DomainKeyFromBundle(bundleBytes []byte, expectedKind string) (*publicKeyEnvelope, error) {
	root, err := validPinnedRoot()
	if err != nil {
		return nil, err
	}
	content, err := verifyBundlePayload(bundleBytes, root, expectedKind)
	if err != nil {
		return nil, err
	}
	var env publicKeyEnvelope
	if err := json.Unmarshal(content, &env); err != nil {
		return nil, fmt.Errorf("domain-key bundle payload is not a public key envelope: %w", err)
	}
	return &env, nil
}

func verifyBundlePayload(bundleBytes []byte, pubkey *publicKeyEnvelope, expectedKind string) (json.RawMessage, error) {
	return sigenvelope.VerifyPayload(bundleBytes, pubkey, expectedKind, crypto.ProfileRoot)
}

func validatePubKeyEnvelope(env *publicKeyEnvelope) error {
	return sigenvelope.ValidatePublicKeyEnvelope(env, crypto.ProfileRoot)
}
