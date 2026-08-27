// Package oauth adopts the OAuth credentials the operator already has.
//
// CUSTODY LAW: the runtime reads the owner-maintained original — the file
// the vendor's own CLI writes — and never creates a durable copy, mirror,
// filesystem cache, replacement, or write-back path. It also never spends the
// owner's refresh token: rotation could invalidate the original and lock out
// its owner after this process exits. The owner refreshes; AII OS rereads.
package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// expirySkew is how early a token is considered spent. Sixty seconds
// was too tight: a request beginning inside that window can outlive its
// own credential mid-flight. Both real vendors issue tokens measured in
// hours or days, so fifteen minutes costs nothing and removes the race.
const expirySkew = 15 * time.Minute

// ExpirySkew is expirySkew, exported so that every surface which tells
// anyone whether a credential is usable subtracts the SAME amount. The
// dashboard used to render the raw vendor expiry: it said "valid to
// 17:52" while this package had been refusing since 17:37, so the
// operator watched a working credential produce an expired-credential
// error. One fact, one number, one place — the alternative is two
// components disagreeing about whether the identity can think.
const ExpirySkew = expirySkew

// ErrOwnerRefreshRequired classifies a credential that AII OS cannot safely
// renew. The owner must update the original source before retrying.
var ErrOwnerRefreshRequired = errors.New("credential owner refresh required")

// Kinds of credential store this runtime knows how to adopt.
const (
	KindClaudeCode = "claude-code" // Claude Max/Pro via the Claude Code CLI
	KindCodex      = "codex"       // ChatGPT Plus/Pro via the codex CLI

	// KindFilePrefix names an explicitly owned token file, for any provider
	// that issues one. "file:/path/to/token.json". Without it,
	// OAuth would mean "OAuth for the two vendors that happen to ship a
	// desktop CLI" — which is not support, it is a coincidence.
	KindFilePrefix = "file:"
)

// Kinds lists the adoptable stores, for the operator surface. It is
// EMPTY where adoption cannot work, so the picker never offers a choice
// the platform cannot honour.
func Kinds() []string {
	if !platformAdopts {
		return nil
	}
	return []string{KindClaudeCode, KindCodex}
}

// Available reports whether this platform can adopt credentials at all.
func Available() bool { return platformAdopts }

// Credential is one request's worth of authorization.
type Credential struct {
	Token   string
	Headers map[string]string // extra headers this credential requires
	Gen     uint64            // hand back on rejection so an owner-updated source is not rejected twice
}

// spec is everything vendor-specific, as data.
type spec struct {
	abs     string   // explicit absolute path; empty = under $HOME
	file    []string // path segments under $HOME
	baseURL string   // credential-scoped base override ("" = the provider's own)
	needs   string   // scope required for inference ("" = cannot be checked)
	parse   func([]byte) (*state, error)
	dialect string // api_type this credential forces ("" = whatever the entry says)
	billing string // first Anthropic system block required by Claude Code OAuth

	// Client identification the provider expects before it will serve a
	// VALID credential. The credential is the authorization; these are
	// product gates, not security controls, and a token the operator pays
	// for should work. Data, not special cases — a vendor that changes
	// what it wants is a table edit.
	headers map[string]string // sent on every request
	query   map[string]string // added to discovery requests
}

type state struct {
	isAPIKey bool   // the store held a plain key, not an OAuth token
	plan     string // subscription tier the credential names, when it names one
	tier     string
	access   string
	expires  time.Time // zero = unknown
	headers  map[string]string
	scopes   []string
}

func specFor(kind string) (spec, error) {
	// A file we own is not an adoption: it works on every platform,
	// because nothing is being read out of another app's sandbox.
	if path, ok := strings.CutPrefix(kind, KindFilePrefix); ok {
		if path == "" {
			return spec{}, fmt.Errorf("credential %q names no file", kind)
		}
		return spec{abs: path, parse: parseGeneric}, nil
	}
	if !platformAdopts {
		return spec{}, credentialHomeErr()
	}
	switch kind {
	case KindClaudeCode:
		return spec{
			file:    []string{".claude", ".credentials.json"},
			needs:   "user:inference",
			parse:   parseClaudeCode,
			dialect: "anthropic",
		}, nil
	case KindCodex:
		return spec{
			file: []string{".codex", "auth.json"},
			// The ChatGPT credential is NOT valid against api.openai.com:
			// it carries connector scopes, not inference scopes (probed
			// 2026-08-20: 403 missing api.model.read, 429 billing_not_active).
			// Inference lives on the ChatGPT backend, in the Responses dialect.
			baseURL: "https://chatgpt.com/backend-api/codex",
			parse:   parseCodex,
			dialect: "chatgpt",
			// The ChatGPT backend serves its catalogue only to a client
			// version it recognises: without this it answers 400 "Field
			// required: client_version", and with an unrecognised one it
			// answers 200 with an EMPTY list (probed 2026-08-20).
			query: map[string]string{"client_version": "1.0.0"},
		}, nil
	}
	return spec{}, fmt.Errorf("unknown credential source %q (known: %s)", kind, strings.Join(Kinds(), ", "))
}

// credentialHomeErr surfaces the platform's own reason for having no
// adoptable credentials.
func credentialHomeErr() error {
	_, err := credentialHome()
	if err == nil {
		err = fmt.Errorf("adopted credentials are not available on this platform")
	}
	return err
}

// homeDir is the seam tests use to point at a fixture home. Production
// has one implementation and no config key.
var homeDir = credentialHome

// applyOverrides lets the operator correct a vendor path or request-contract
// fact without waiting for a release. It is deliberately not a catalog
// language for two sources.
func applyOverrides(sp spec, ov map[string]string) (spec, error) {
	for k, v := range ov {
		if v == "" {
			return spec{}, fmt.Errorf("credential option %q is empty", k)
		}
		switch {
		case k == "file":
			// An absolute path is absolute. Splitting it into segments and
			// joining under $HOME silently relocated it.
			if filepath.IsAbs(v) || strings.HasPrefix(v, "~/") {
				sp.abs, sp.file = v, nil
			} else {
				sp.file = strings.Split(v, "/")
			}
		case k == "base_url":
			sp.baseURL = v
		case k == "billing_text":
			sp.billing = v
		case strings.HasPrefix(k, "header_"):
			if k == "header_" {
				return spec{}, fmt.Errorf("credential option %q names no header", k)
			}
			if sp.headers == nil {
				sp.headers = map[string]string{}
			}
			sp.headers[strings.TrimPrefix(k, "header_")] = v
		case strings.HasPrefix(k, "query_"):
			if k == "query_" {
				return spec{}, fmt.Errorf("credential option %q names no query parameter", k)
			}
			q := map[string]string{}
			for qk, qv := range sp.query {
				q[qk] = qv
			}
			q[strings.TrimPrefix(k, "query_")] = v
			sp.query = q
		default:
			return spec{}, fmt.Errorf("unknown credential option %q", k)
		}
	}
	return sp, nil
}

// Source adopts one credential store.
type Source struct {
	kind string
	sp   spec
	path string

	mu         sync.Mutex
	st         *state
	sourceHash [sha256.Size]byte
	gen        uint64
}

// New opens a credential source. It reads the file once so a broken or
// unfit credential is refused at configuration time, not at call time.
func New(kind string, overrides ...map[string]string) (*Source, error) {
	sp, err := specFor(kind)
	if err != nil {
		return nil, err
	}
	for _, ov := range overrides {
		sp, err = applyOverrides(sp, ov)
		if err != nil {
			return nil, fmt.Errorf("credential %q: %w", kind, err)
		}
	}
	// The required-option gate lives with the registry that declares
	// them (internal/app), not here. This package once carried the list
	// as a Go slice under a kind == KindClaudeCode special case, which
	// contradicted its own comment above: "Data, not special cases — a
	// vendor that changes what it wants is a table edit." It is a table
	// edit now.
	path := sp.abs
	if path == "" {
		home, herr := homeDir()
		if herr != nil {
			return nil, fmt.Errorf("cannot locate the home directory holding %s credentials: %w", kind, herr)
		}
		path = filepath.Join(append([]string{home}, sp.file...)...)
	} else if strings.HasPrefix(path, "~/") {
		home, herr := homeDir()
		if herr != nil {
			return nil, herr
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	s := &Source{kind: kind, sp: sp, path: path}
	if _, err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// Path is the owner-maintained file this source reads (never writes).
func (s *Source) Path() string { return s.path }

// Dialect is the api_type this credential forces, if any. A plain API
// key found in the same store forces nothing: it is valid on the
// provider's ordinary base, and routing it to a subscription backend
// would break a credential that works.
func (s *Source) Dialect() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st != nil && s.st.isAPIKey {
		return ""
	}
	return s.sp.dialect
}

// BaseURL is the endpoint this credential is valid against, if it is
// scoped to one.
func (s *Source) BaseURL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st != nil && s.st.isAPIKey {
		return ""
	}
	return s.sp.baseURL
}

// BillingText is request metadata owned by the credential protocol. It is
// empty for credentials that do not require a first system block.
func (s *Source) BillingText() string { return s.sp.billing }

// DiscoveryQuery is what this provider requires on a model-list request
// before it will answer for a valid credential.
func (s *Source) DiscoveryQuery() map[string]string {
	query := make(map[string]string, len(s.sp.query))
	for name, value := range s.sp.query {
		query[name] = value
	}
	return query
}

// load re-reads the original when it has changed on disk — the owning CLI
// refreshes it out from under us, and that is the normal case.
func (s *Source) load() (*state, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no %s credentials at %s — sign in with that tool first%s",
				s.kind, s.path, keychainNote())
		}
		return nil, fmt.Errorf("%s credentials at %s: %w", s.kind, s.path, err)
	}
	sourceHash := sha256.Sum256(raw)
	if s.st != nil && sourceHash == s.sourceHash {
		return s.st, nil
	}
	st, err := s.sp.parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s credentials at %s: %w", s.kind, s.path, err)
	}
	if st.access == "" {
		return nil, fmt.Errorf("%s credentials at %s carry no access token", s.kind, s.path)
	}
	// The credential says what it is good for. Refuse one that cannot do
	// inference at CONFIGURATION time, with the reason — rather than
	// letting it fail later as an opaque 403.
	if s.sp.needs != "" && len(st.scopes) > 0 {
		ok := false
		for _, sc := range st.scopes {
			if sc == s.sp.needs {
				ok = true
				break
			}
		}
		if !ok {
			return nil, fmt.Errorf("%s credential does not grant %q (it has: %s) — it cannot serve inference",
				s.kind, s.sp.needs, strings.Join(st.scopes, ", "))
		}
	}
	// A CREDENTIAL SOURCE DOES NOT CHANGE SPECIES MID-FLIGHT.
	//
	// Dialect() and BaseURL() answer differently for an OAuth
	// subscription than for a plain API key found in the same store, and
	// the caller reads them ONCE, when it builds the client
	// (app/providers.go entryTransport). This function re-reads the file
	// on every request, because the owning CLI refreshes it out from
	// under us and that is normal. So an operator signing out of a
	// subscription and back in with an API key — or the reverse — kept a
	// route chosen for the credential that is gone: the new token going
	// to the old endpoint, in the old wire dialect.
	//
	// A refresh of the SAME kind is the normal case and still silent.
	// Only a change of kind refuses, and it names the reload, because a
	// route is decided at construction and only construction can redecide
	// it.
	if s.st != nil && s.st.isAPIKey != st.isAPIKey {
		was, now := "an OAuth credential", "a plain API key"
		if st.isAPIKey == false {
			was, now = now, was
		}
		return nil, fmt.Errorf("%s credentials at %s changed from %s to %s — "+
			"the endpoint and wire dialect were chosen for the old one; restart or re-select the provider so the route is decided again",
			s.kind, s.path, was, now)
	}
	s.st, s.sourceHash = st, sourceHash
	s.gen++
	return st, nil
}

// Credential returns the current credential from the owner-maintained source.
// An expired source is never refreshed by AII OS; doing so could rotate the
// owner's refresh token and break their own tool.
func (s *Source) Credential(ctx context.Context) (Credential, error) {
	if err := ctx.Err(); err != nil {
		return Credential{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return Credential{}, err
	}
	if !st.expires.IsZero() && !time.Now().Before(st.expires.Add(-expirySkew)) {
		return Credential{}, s.ownerRefreshError("is expired or too close to expiry")
	}
	return s.credLocked(st), nil
}

// Stale rereads the owner-maintained source after a provider rejection. If the
// owner has already advanced it, the caller may replay once. Otherwise AII OS
// refuses and tells the operator where the credential must be refreshed.
func (s *Source) Stale(ctx context.Context, gen uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if gen != s.gen {
		return nil
	}
	if _, err := s.load(); err != nil {
		return err
	}
	if gen != s.gen {
		return nil
	}
	return s.ownerRefreshError("was rejected by the provider")
}

func (s *Source) credLocked(st *state) Credential {
	h := make(map[string]string, len(st.headers)+len(s.sp.headers))
	for k, v := range s.sp.headers {
		h[k] = v
	}
	for k, v := range st.headers {
		h[k] = v // the credential's own values win over the spec's
	}
	return Credential{Token: st.access, Headers: h, Gen: s.gen}
}

func (s *Source) ownerRefreshError(reason string) error {
	return fmt.Errorf("%w: %s credential at %s %s — refresh it with its own tool, then retry",
		ErrOwnerRefreshRequired, s.kind, s.path, reason)
}

// ---- vendor shapes -------------------------------------------------

func parseClaudeCode(raw []byte) (*state, error) {
	var f struct {
		O struct {
			AccessToken      string   `json:"accessToken"`
			ExpiresAt        int64    `json:"expiresAt"` // MILLISECOND epoch
			Scopes           []string `json:"scopes"`
			SubscriptionType string   `json:"subscriptionType"`
			RateLimitTier    string   `json:"rateLimitTier"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	st := &state{access: f.O.AccessToken, scopes: f.O.Scopes, plan: f.O.SubscriptionType, tier: f.O.RateLimitTier}
	if f.O.ExpiresAt > 0 {
		st.expires = time.UnixMilli(f.O.ExpiresAt)
	}
	return st, nil
}

func parseCodex(raw []byte) (*state, error) {
	var f struct {
		APIKey string `json:"OPENAI_API_KEY"`
		Tokens struct {
			AccessToken string `json:"access_token"`
			AccountID   string `json:"account_id"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	// The same file expresses EITHER an OAuth token or an API key,
	// depending on how the operator signed in. Reading only the first
	// reported "no access token" for a perfectly good credential.
	if f.Tokens.AccessToken == "" && f.APIKey != "" {
		return &state{access: f.APIKey, isAPIKey: true}, nil
	}
	st := &state{access: f.Tokens.AccessToken}
	if f.Tokens.AccountID != "" {
		st.headers = map[string]string{"ChatGPT-Account-ID": f.Tokens.AccountID}
	}
	// No expiry field: the access token is a JWT and carries its own.
	if c, err := jwtClaims(st.access); err == nil {
		if exp, ok := c["exp"].(float64); ok && exp > 0 {
			st.expires = time.Unix(int64(exp), 0)
		}
		if auth, ok := c["https://api.openai.com/auth"].(map[string]any); ok {
			if p, ok := auth["chatgpt_plan_type"].(string); ok {
				st.plan = p
			}
		}
		if scp, ok := c["scp"].([]any); ok {
			for _, v := range scp {
				if s, ok := v.(string); ok {
					st.scopes = append(st.scopes, s)
				}
			}
		}
	}
	return st, nil
}

// jwtClaims decodes the payload of our OWN token to read its metadata.
// The signature is deliberately not verified: this is not a trust
// decision, it is reading the expiry stamped on a credential we already
// hold and are about to present.
func jwtClaims(tok string) (map[string]any, error) {
	parts := strings.Split(tok, ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("not a JWT")
	}
	seg := parts[1]
	if p := len(seg) % 4; p != 0 {
		seg += strings.Repeat("=", 4-p)
	}
	dec, err := base64.URLEncoding.DecodeString(seg)
	if err != nil {
		return nil, err
	}
	var c map[string]any
	if err := json.Unmarshal(dec, &c); err != nil {
		return nil, err
	}
	return c, nil
}

// Info is what the credential says about itself — a derived NOW-fact for
// the operator surface, never stored. Running an identity's rhythms on a
// subscription is a different posture from an API key, and the operator
// should be able to see which one they are on and when it lapses.
type Info struct {
	Kind      string    `json:"kind"`
	Plan      string    `json:"plan,omitempty"`
	Tier      string    `json:"tier,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	IsAPIKey  bool      `json:"is_api_key,omitempty"`
	Path      string    `json:"path,omitempty"`
}

// Info reports the loaded credential's self-description.
func (s *Source) Info() Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := Info{Kind: s.kind, Path: s.path}
	if s.st == nil {
		return i
	}
	i.Plan, i.Tier, i.ExpiresAt, i.IsAPIKey = s.st.plan, s.st.tier, s.st.expires, s.st.isAPIKey
	return i
}

// parseGeneric reads an explicitly named owner-maintained token file. It
// accepts the flat RFC 6749 shape and both vendor shapes.
func parseGeneric(raw []byte) (*state, error) {
	if st, err := parseClaudeCode(raw); err == nil && st.access != "" {
		return st, nil
	}
	if st, err := parseCodex(raw); err == nil && st.access != "" {
		return st, nil
	}
	var f struct {
		AccessToken string            `json:"access_token"`
		ExpiresAt   int64             `json:"expires_at"` // unix SECONDS
		ExpiresAtMS int64             `json:"expires_at_ms"`
		Scope       string            `json:"scope"`
		Headers     map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	st := &state{access: f.AccessToken, headers: f.Headers}
	switch {
	case f.ExpiresAtMS > 0:
		st.expires = time.UnixMilli(f.ExpiresAtMS)
	case f.ExpiresAt > 0:
		st.expires = time.Unix(f.ExpiresAt, 0)
	}
	if f.Scope != "" {
		st.scopes = strings.Fields(f.Scope)
	}
	if st.access == "" {
		return nil, fmt.Errorf("no access_token")
	}
	// A JWT carries its own expiry; prefer it over a stale stamped one.
	if st.expires.IsZero() {
		if c, err := jwtClaims(st.access); err == nil {
			if exp, ok := c["exp"].(float64); ok && exp > 0 {
				st.expires = time.Unix(int64(exp), 0)
			}
		}
	}
	return st, nil
}
