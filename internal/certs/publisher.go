package certs

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/witness"
)

// .
// .
// .
// .
// .
// .
// .

const (
	claimInputTag     = "AIII-CERTD-CLAIM"
	publishInputTag   = "AIII-CERTD-PUBLISH"
	unpublishInputTag = "AIII-CERTD-UNPUBLISH"
	// .
	// .
	// .
	// .
	claimLifetime    = 9 * time.Minute
	publishLifetime  = 14 * time.Minute
	maxResponseBytes = 64 << 10
)

// .
type PublisherClient struct {
	baseURL    string
	http       *http.Client
	key        witness.IdentityKey
	env        *witness.PublicKeyEnvelope
	envelope   []byte
	identityID string
	now        func() time.Time
	nonce      func() (string, error)
}

// .
// .
// .
func NewPublisherClient(serverURL, tlsSPKISHA256 string, key witness.IdentityKey, canonicalEnvelope []byte, env *witness.PublicKeyEnvelope) (*PublisherClient, error) {
	if serverURL == "" {
		return nil, errors.New("certs: no certificate server URL")
	}
	hc, err := witness.NewPinnedHTTPClient(tlsSPKISHA256, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("certs: %w", err)
	}
	id, err := witness.DeriveIdentityID(canonicalEnvelope, env)
	if err != nil {
		return nil, err
	}
	return &PublisherClient{
		baseURL: strings.TrimSuffix(serverURL, "/"), http: hc, key: key, env: env, envelope: canonicalEnvelope, identityID: id,
		now: time.Now, nonce: newNonce,
	}, nil
}

// .
func (c *PublisherClient) IdentityID() string { return c.identityID }

func newNonce() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// .
func ClaimInput(identityID string, canonicalEnvelope []byte, nameID, nonce, expiresAt string) []byte {
	return []byte(claimInputTag + "\n" +
		"identity_id:" + identityID + "\n" +
		"identity_public_key:" + string(canonicalEnvelope) + "\n" +
		"name_id:" + nameID + "\n" +
		"nonce:" + nonce + "\n" +
		"expires_at:" + expiresAt + "\n")
}

// .
// .
func PublishInput(tag, identityID, name, txt, nonce, expiresAt string) []byte {
	return []byte(tag + "\n" +
		"identity_id:" + identityID + "\n" +
		"name:" + name + "\n" +
		"txt:" + txt + "\n" +
		"nonce:" + nonce + "\n" +
		"expires_at:" + expiresAt + "\n")
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
type ClaimResult struct {
	Name  string
	Alias string
	Zone  string
}

// .
// .
// .
// .
func (c *PublisherClient) Claim(ctx context.Context, nameID string) (ClaimResult, error) {
	nonce, err := c.nonce()
	if err != nil {
		return ClaimResult{}, err
	}
	expires := c.now().Add(claimLifetime).UTC().Truncate(time.Second).Format(time.RFC3339)
	sig, err := witness.SignInput(c.key, c.env, ClaimInput(c.identityID, c.envelope, nameID, nonce, expires))
	if err != nil {
		return ClaimResult{}, err
	}
	body := map[string]interface{}{
		"identity_id":         c.identityID,
		"identity_public_key": json.RawMessage(c.envelope),
		"name_id":             nameID,
		"nonce":               nonce,
		"expires_at":          expires,
		"identity_signature":  sig,
	}
	var out struct {
		Name  string `json:"name"`
		Alias string `json:"alias"`
		Zone  string `json:"zone"`
	}
	if err := c.post(ctx, "/certd/claim", body, &out, http.StatusCreated, http.StatusOK); err != nil {
		return ClaimResult{}, err
	}
	if out.Name == "" || out.Zone == "" {
		return ClaimResult{}, errors.New("certs: claim answered without a name")
	}
	// .
	// .
	return ClaimResult{Name: out.Name, Alias: out.Alias, Zone: out.Zone}, nil
}

// .
// .
// .
// .
// .
type RelayEndpoint struct {
	Name string `json:"name"`
	Port int    `json:"port"`
}

// .
// .
// .
// .
// .
// .
// .
// .
type ServiceStatus struct {
	Zone           string
	Relays         []string
	Capabilities   []string
	RecordSetTypes []string
	RelayEndpoints []RelayEndpoint
	// .
	// .
	MaxPublishLifetime time.Duration
	// .
	// .
	// .
	// .
	// .
	MaxPublishesPerHour int
	MaxRequestsPerHour  int
	MaxRequestBodyBytes int
}

// .
func (s ServiceStatus) Supports(capability string) bool {
	for _, c := range s.Capabilities {
		if c == capability {
			return true
		}
	}
	return false
}

// .
// .
// .
func (s ServiceStatus) AcceptsRecordType(kind string) bool {
	for _, t := range s.RecordSetTypes {
		if t == kind {
			return true
		}
	}
	return false
}

// .
// .
func (s ServiceStatus) Relay(name string) (RelayEndpoint, bool) {
	for _, r := range s.RelayEndpoints {
		if r.Name == name {
			return r, true
		}
	}
	return RelayEndpoint{}, false
}

// .
// .
// .
func (s ServiceStatus) RouteLease() time.Duration {
	lease := s.MaxPublishLifetime
	if lease <= 0 {
		return publishLifetime
	}
	if margin := lease / 15; lease-margin < publishLifetime {
		return lease - margin
	}
	return publishLifetime
}

// .
func (c *PublisherClient) ServiceStatus(ctx context.Context) (ServiceStatus, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/status", nil)
	if err != nil {
		return ServiceStatus{}, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return ServiceStatus{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		// .
		// .
		// .
		// .
		// .
		data, _ := readWholeBody(res)
		return ServiceStatus{}, &RecordSetError{
			Op: "status", Name: c.baseURL, Status: res.StatusCode,
			Message: serviceMessage(data), RetryAfter: retryAfter(res.Header.Get("Retry-After")),
		}
	}
	data, err := readWholeBody(res)
	if err != nil {
		return ServiceStatus{}, err
	}
	var out struct {
		Zone                string          `json:"zone"`
		Relays              []string        `json:"relays"`
		Capabilities        []string        `json:"capabilities"`
		RecordSetTypes      []string        `json:"record_set_types"`
		RelayEndpoints      []RelayEndpoint `json:"relay_endpoints"`
		MaxPublishLifetime  int             `json:"max_publish_lifetime_seconds"`
		MaxPublishesPerHour int             `json:"max_publishes_per_identity_per_hour"`
		MaxRequestsPerHour  int             `json:"max_requests_per_ip_per_hour"`
		MaxRequestBodyBytes int             `json:"max_request_body_bytes"`
	}
	if err := decodeWhole(data, &out); err != nil {
		return ServiceStatus{}, fmt.Errorf("certs: status: %w", err)
	}
	// .
	// .
	// .
	if out.MaxPublishLifetime < 0 {
		out.MaxPublishLifetime = 0
	}
	if out.Zone == "" {
		return ServiceStatus{}, errors.New("certs: status names no zone")
	}
	endpoints := make([]RelayEndpoint, 0, len(out.RelayEndpoints))
	for _, r := range out.RelayEndpoints {
		// .
		// .
		if r.Name == "" || r.Port <= 0 || r.Port > 65535 {
			continue
		}
		endpoints = append(endpoints, r)
	}
	return ServiceStatus{
		Zone: out.Zone, Relays: out.Relays,
		Capabilities: out.Capabilities, RecordSetTypes: out.RecordSetTypes, RelayEndpoints: endpoints,
		MaxPublishLifetime:  time.Duration(out.MaxPublishLifetime) * time.Second,
		MaxPublishesPerHour: out.MaxPublishesPerHour, MaxRequestsPerHour: out.MaxRequestsPerHour,
		MaxRequestBodyBytes: out.MaxRequestBodyBytes,
	}, nil
}

// .
func (c *PublisherClient) ServiceZone(ctx context.Context) (string, error) {
	st, err := c.ServiceStatus(ctx)
	return st.Zone, err
}

// .
// .
func (c *PublisherClient) Publish(ctx context.Context, name, txt string) error {
	return c.publish(ctx, http.MethodPost, publishInputTag, name, txt)
}

// .
func (c *PublisherClient) Unpublish(ctx context.Context, name, txt string) error {
	return c.publish(ctx, http.MethodDelete, unpublishInputTag, name, txt)
}

func (c *PublisherClient) publish(ctx context.Context, method, tag, name, txt string) error {
	nonce, err := c.nonce()
	if err != nil {
		return err
	}
	expires := c.now().Add(publishLifetime).UTC().Format(time.RFC3339)
	sig, err := witness.SignInput(c.key, c.env, PublishInput(tag, c.identityID, name, txt, nonce, expires))
	if err != nil {
		return err
	}
	body := map[string]interface{}{
		"identity_id":        c.identityID,
		"name":               name,
		"txt":                txt,
		"nonce":              nonce,
		"expires_at":         expires,
		"identity_signature": sig,
	}
	return c.do(ctx, method, "/certd/publish", body, nil, http.StatusAccepted, http.StatusOK)
}

// .
// .
// .
// .
// .
func readWholeBody(res *http.Response) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("certs: the answer could not be read whole: %w", err)
	}
	if len(data) > maxResponseBytes {
		return nil, fmt.Errorf("certs: the answer is larger than %d bytes", maxResponseBytes)
	}
	return data, nil
}

// .
// .
// .
func decodeWhole(data []byte, out interface{}) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(out); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("more than one JSON value in the answer")
	}
	return nil
}

func (c *PublisherClient) post(ctx context.Context, path string, body, out interface{}, want ...int) error {
	return c.do(ctx, http.MethodPost, path, body, out, want...)
}

func (c *PublisherClient) do(ctx context.Context, method, path string, body, out interface{}, want ...int) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	canonical, err := canonicaljson.CanonicalizeV1(raw)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(canonical))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("certificate server: %w", err)
	}
	defer res.Body.Close()
	data, readErr := readWholeBody(res)
	ok := false
	for _, w := range want {
		if res.StatusCode == w {
			ok = true
		}
	}
	if !ok {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = strings.TrimSpace(string(data))
		}
		if readErr != nil {
			e.Error = readErr.Error()
		}
		return fmt.Errorf("certificate server %s %s: HTTP %d: %s", method, path, res.StatusCode, e.Error)
	}
	// .
	// .
	if readErr != nil {
		return fmt.Errorf("certificate server %s %s: HTTP %d: %w", method, path, res.StatusCode, readErr)
	}
	if out != nil && len(data) > 0 {
		if err := decodeWhole(data, out); err != nil {
			return fmt.Errorf("certificate server answered unparseably: %w", err)
		}
	}
	return nil
}
