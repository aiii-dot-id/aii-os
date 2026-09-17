// .
// .
// .
// .
// .
// .
// .
package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// .
// .
// .
// .
const expirySkew = 15 * time.Minute

// .
// .
// .
// .
// .
// .
// .
const ExpirySkew = expirySkew

// .
// .
var ErrOwnerRefreshRequired = errors.New("credential owner refresh required")

// .
const (
	KindClaudeCode = "claude-code"
	KindCodex      = "codex"

	// .
	// .
	// .
	// .
	KindFilePrefix = "file:"
)

// .
// .
// .
func Kinds() []string {
	if !platformAdopts {
		return nil
	}
	return []string{KindClaudeCode, KindCodex}
}

// .
func Available() bool { return platformAdopts }

// .
type Credential struct {
	Token   string
	Headers map[string]string
	Gen     uint64
	// .
	// .
	// .
	Refreshed bool
}

// .
type spec struct {
	accountHeader string
	abs           string
	file          []string
	baseURL       string
	needs         string
	parse         func([]byte) (*state, error)
	dialect       string
	billing       string

	// .
	// .
	// .
	// .
	// .
	headers map[string]string
	query   map[string]string

	// .
	// .
	// .
	// .
	keychain string

	// .
	// .
	// .
	generic bool

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	oauth OAuthParams
}

// .
type OAuthParams struct {
	TokenEncoding   string
	TokenHeaders    map[string]string
	TokenParams     map[string]any
	RefreshParams   map[string]any
	ResourceHeaders map[string]string
	ClaimHeaders    map[string][]string
	ClientID        string
	AuthorizeURL    string
	TokenURL        string
	RedirectURI     string
	Scope           string
	// .
	// .
	RequiredScope string
	Originator    string
	// .
	// .
	// .
	// .
	ClientSecret string
	// .
	// .
	IDTokenAddOrganizations bool
	// .
	// .
	// .
	// .
	// .
	AuthorizeParams map[string]string
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	AccountClaim []string
}

// .
// .
// .
func (sp spec) refreshable() bool { return sp.oauth.TokenURL != "" && sp.oauth.ClientID != "" }

// .
func (o OAuthParams) Complete() bool {
	return o.ClientID != "" && o.AuthorizeURL != "" && o.TokenURL != "" && o.RedirectURI != ""
}

type state struct {
	account  string
	isAPIKey bool
	plan     string
	tier     string
	access   string
	expires  time.Time
	headers  map[string]string
	scopes   []string
	// .
	// .
	// .
	// .
	refresh string
	owned   bool
}

func specFor(kind string) (spec, error) {
	// .
	// .
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
		return spec{parse: parseClaudeCode}, nil
	case KindCodex:
		return spec{parse: parseCodex}, nil
	}
	return spec{}, fmt.Errorf("unknown credential source %q (known: %s)", kind, strings.Join(Kinds(), ", "))
}

// .
// .
func credentialHomeErr() error {
	_, err := credentialHome()
	if err == nil {
		err = fmt.Errorf("adopted credentials are not available on this platform")
	}
	return err
}

// .
// .
func readFile(path string) ([]byte, error) { return os.ReadFile(path) }
func isNotExist(err error) bool            { return os.IsNotExist(err) }

// .
// .
var homeDir = credentialHome

// .
// .
// .
func applyOverrides(sp spec, ov map[string]string) (spec, error) {
	for k, v := range ov {
		if v == "" {
			return spec{}, fmt.Errorf("credential option %q is empty", k)
		}
		switch {
		case k == "default_file" || k == "file":
			if k == "default_file" && ov["file"] != "" {
				continue
			}
			// .
			// .
			if filepath.IsAbs(v) || strings.HasPrefix(v, "~/") {
				sp.abs, sp.file = v, nil
			} else {
				sp.file = strings.Split(v, "/")
			}
		case k == "dialect":
			sp.dialect = v
		case k == "required_scope":
			sp.needs = v
		case k == "account_header":
			sp.accountHeader = v
		case k == "base_url":
			sp.baseURL = v
		case k == "keychain_service":
			sp.keychain = v
		case k == "client_version":
			// .
			// .
			// .
			// .
		case k == "billing_text":
			sp.billing = v
		case k == "oauth_client_id":
			sp.oauth.ClientID = v
		case k == "oauth_authorize_url":
			sp.oauth.AuthorizeURL = v
		case k == "oauth_token_url":
			sp.oauth.TokenURL = v
		case k == "oauth_redirect_uri":
			sp.oauth.RedirectURI = v
		case k == "oauth_scope":
			sp.oauth.Scope = v
		case k == "oauth_originator":
			sp.oauth.Originator = v
		case k == "oauth_id_token_add_organizations":
			switch v {
			case "true":
				sp.oauth.IDTokenAddOrganizations = true
			case "false":
				sp.oauth.IDTokenAddOrganizations = false
			default:
				return spec{}, fmt.Errorf("credential option %q must be \"true\" or \"false\", got %q", k, v)
			}
		case k == "oauth_account_claim":
			var path []string
			if err := json.Unmarshal([]byte(v), &path); err != nil || len(path) == 0 {
				return spec{}, fmt.Errorf("credential option %q must be a JSON array of claim names, got %q", k, v)
			}
			sp.oauth.AccountClaim = path
		case k == "oauth_authorize_params":
			var m map[string]string
			if err := json.Unmarshal([]byte(v), &m); err != nil {
				return spec{}, fmt.Errorf("credential option %q must be a JSON object of strings: %w", k, err)
			}
			sp.oauth.AuthorizeParams = m
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

// .
type Source struct {
	kind string
	sp   spec
	path string

	// .
	// .
	// .
	// .
	// .
	owned bool
	// .
	// .
	refreshMu sync.Mutex

	mu         sync.Mutex
	st         *state
	sourceHash [sha256.Size]byte
	gen        uint64
	// .
	// .
	client *http.Client
}

// .
// .
func New(kind string, overrides ...map[string]string) (*Source, error) {
	return newBorrowed(kind, OAuthParams{}, overrides...)
}

func NewConfigured(kind string, params OAuthParams, overrides ...map[string]string) (*Source, error) {
	return newBorrowed(kind, params, overrides...)
}

func newBorrowed(kind string, params OAuthParams, overrides ...map[string]string) (*Source, error) {
	sp, err := specFor(kind)
	if err != nil {
		return nil, err
	}
	sp.oauth = params
	sp.headers = copyMap(params.ResourceHeaders)
	for _, ov := range overrides {
		sp, err = applyOverrides(sp, ov)
		if err != nil {
			return nil, fmt.Errorf("credential %q: %w", kind, err)
		}
	}
	// .
	// .
	// .
	// .
	// .
	// .
	path := sp.abs
	if path == "" && len(sp.file) == 0 {
		return nil, fmt.Errorf("credential %q needs a configured file or default_file", kind)
	}
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

// .
// .
// .
// .
// .
func NewOwned(kind, path string, overrides ...map[string]string) (*Source, error) {
	return NewOwnedConfigured(kind, path, OAuthParams{}, overrides...)
}

// .
// .
func NewOwnedConfigured(kind, path string, params OAuthParams, overrides ...map[string]string) (*Source, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("owned credential path must be absolute, got %q", path)
	}
	sp := spec{parse: parseGeneric, generic: true, oauth: params, headers: copyMap(params.ResourceHeaders)}
	var err error
	for _, ov := range overrides {
		sp, err = applyOverrides(sp, ov)
		if err != nil {
			return nil, err
		}
	}
	if !sp.refreshable() {
		return nil, fmt.Errorf("credential %q: an owned source needs a token endpoint and client id", kind)
	}
	sp.abs, sp.file, sp.keychain = path, nil, ""
	s := &Source{kind: kind, sp: sp, path: path, owned: true}
	if _, err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// .
func (s *Source) Owned() bool { return s.owned }

// .
func (s *Source) Path() string { return s.path }

// .
// .
// .
// .
func (s *Source) Dialect() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st != nil && s.st.isAPIKey {
		return ""
	}
	return s.sp.dialect
}

// .
// .
func (s *Source) BaseURL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st != nil && s.st.isAPIKey {
		return ""
	}
	return s.sp.baseURL
}

// .
// .
func (s *Source) BillingText() string { return s.sp.billing }

// .
func (s *Source) OAuth() OAuthParams { return s.sp.oauth }

// .
// .
func (s *Source) DiscoveryQuery() map[string]string {
	query := make(map[string]string, len(s.sp.query))
	for name, value := range s.sp.query {
		query[name] = value
	}
	return query
}

// .
// .
func (s *Source) load() (*state, error) {
	raw, err := adoptedBytes(s.sp.keychain, s.path)
	if err != nil {
		if os.IsNotExist(err) {
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
			if s.sp.oauth.Complete() {
				return nil, fmt.Errorf("no %s credentials at %s%s — sign in to this provider from Settings, or sign in with that tool first: %w",
					s.kind, s.path, keychainNote(s.sp.keychain), err)
			}
			return nil, fmt.Errorf("no %s credentials at %s — sign in with that tool first%s: %w",
				s.kind, s.path, keychainNote(s.sp.keychain), err)
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
	if st.account != "" && s.sp.accountHeader != "" {
		if st.headers == nil {
			st.headers = map[string]string{}
		}
		st.headers[s.sp.accountHeader] = st.account
	}
	paths := copyPaths(s.sp.oauth.ClaimHeaders)
	if s.sp.accountHeader != "" && len(s.sp.oauth.AccountClaim) > 0 {
		if paths == nil {
			paths = map[string][]string{}
		}
		paths[s.sp.accountHeader] = s.sp.oauth.AccountClaim
	}
	for header, path := range paths {
		if st.isAPIKey {
			continue
		}
		if st.headers[header] != "" {
			continue
		}
		value := claimString(st.access, path)
		if value == "" {
			return nil, fmt.Errorf("credential lacks required claim %s", strings.Join(path, "."))
		}
		if st.headers == nil {
			st.headers = map[string]string{}
		}
		st.headers[header] = value
	}
	for k, v := range s.sp.headers {
		if !validHeader(k, v) {
			return nil, fmt.Errorf("invalid credential header %q", k)
		}
	}
	for k, v := range st.headers {
		if !validHeader(k, v) {
			return nil, fmt.Errorf("invalid credential header %q", k)
		}
	}
	if st.access == "" {
		return nil, fmt.Errorf("%s credentials at %s carry no access token", s.kind, s.path)
	}
	// .
	// .
	// .
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
	if s.st != nil && s.st.isAPIKey != st.isAPIKey {
		was, now := "an OAuth credential", "a plain API key"
		if !st.isAPIKey {
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

// .
// .
// .
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
	if !nearExpiry(st) {
		return s.credLocked(st), nil
	}
	// .
	// .
	// .
	// .
	// .
	if !s.owned || st.refresh == "" || !s.sp.refreshable() {
		return Credential{}, s.ownerRefreshError("is expired or too close to expiry")
	}
	refreshTok, gen := st.refresh, s.gen
	s.mu.Unlock()
	cred, err := s.refreshOwned(ctx, refreshTok, gen)
	s.mu.Lock()
	return cred, err
}

func nearExpiry(st *state) bool {
	return !st.expires.IsZero() && !time.Now().Before(st.expires.Add(-expirySkew))
}

// .
// .
// .
// .
func (s *Source) refreshOwned(ctx context.Context, refreshTok string, seenGen uint64) (Credential, error) {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	s.mu.Lock()
	if s.gen != seenGen {
		if st, err := s.load(); err == nil && !nearExpiry(st) {
			c := s.credLocked(st)
			s.mu.Unlock()
			return c, nil
		}
	}
	before, err := os.ReadFile(s.path)
	if err != nil {
		s.mu.Unlock()
		return Credential{}, err
	}
	if sha256.Sum256(before) != s.sourceHash {
		st, err := s.load()
		if err != nil {
			s.mu.Unlock()
			return Credential{}, err
		}
		if !nearExpiry(st) {
			c := s.credLocked(st)
			s.mu.Unlock()
			return c, nil
		}
		refreshTok = st.refresh
	}
	params := s.sp.oauth
	if len(s.st.scopes) > 0 {
		params.Scope = strings.Join(s.st.scopes, " ")
	}
	s.mu.Unlock()
	fresh, rerr := refreshWith(ctx, s.httpClient(), params, refreshTok)
	if rerr != nil {
		return Credential{}, fmt.Errorf("credential at %s: refresh failed: %w", s.path, rerr)
	}
	if fresh.Refresh == "" {
		fresh.Refresh = refreshTok
	}
	if fresh.Scope == "" {
		fresh.Scope = params.Scope
	}
	if ctx.Err() != nil {
		return Credential{}, ctx.Err()
	}
	if werr := replaceTokenFile(s.path, before, fresh); werr != nil {
		return Credential{}, fmt.Errorf("credential at %s: could not store refreshed token: %w", s.path, werr)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.load()
	if err != nil {
		return Credential{}, err
	}
	c := s.credLocked(st)
	c.Refreshed = true
	return c, nil
}

// .
// .
// .
func ParamsFromOptions(opts map[string]string) (OAuthParams, error) {
	sp, err := applyOverrides(spec{}, opts)
	if err != nil {
		return OAuthParams{}, err
	}
	return sp.oauth, nil
}

// .
// .
// .
func (s *Source) Stale(ctx context.Context, gen uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if gen != s.gen {
		return nil
	}
	// .
	// .
	forgetAdopted(s.sp.keychain)
	if _, err := s.load(); err != nil {
		return err
	}
	if gen != s.gen {
		return nil
	}
	return s.ownerRefreshError("was rejected by the provider")
}

// .
// .
// .
func (s *Source) Generation() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gen
}

// .
// .
// .
// .
func (s *Source) ForceRefresh(ctx context.Context, gen uint64) (Credential, error) {
	if err := ctx.Err(); err != nil {
		return Credential{}, err
	}
	s.mu.Lock()
	if gen != s.gen {
		st, err := s.load()
		if err != nil {
			s.mu.Unlock()
			return Credential{}, err
		}
		c := s.credLocked(st)
		s.mu.Unlock()
		return c, nil
	}
	st, err := s.load()
	if err != nil {
		s.mu.Unlock()
		return Credential{}, err
	}
	if !s.owned || st.refresh == "" || !s.sp.refreshable() {
		s.mu.Unlock()
		return Credential{}, s.ownerRefreshError("was rejected by the provider")
	}
	refreshTok, seen := st.refresh, s.gen
	s.mu.Unlock()
	// .
	// .
	return s.refreshOwned(ctx, refreshTok, seen)
}

// .
// .
// .
func (s *Source) SetHTTPClient(c *http.Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.client = c
}

func (s *Source) httpClient() *http.Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		return s.client
	}
	return signInHTTP
}

func (s *Source) credLocked(st *state) Credential {
	h := make(map[string]string, len(st.headers)+len(s.sp.headers))
	for k, v := range s.sp.headers {
		h[k] = v
	}
	for k, v := range st.headers {
		h[k] = v
	}
	return Credential{Token: st.access, Headers: h, Gen: s.gen}
}

func (s *Source) ownerRefreshError(reason string) error {
	if s.owned {
		return fmt.Errorf("%w: credential %s — sign in again", ErrGrantInvalid, reason)
	}
	return fmt.Errorf("%w: %s credential at %s %s — refresh it with its own tool, then retry",
		ErrOwnerRefreshRequired, s.kind, s.path, reason)
}

// .

func parseClaudeCode(raw []byte) (*state, error) {
	var f struct {
		O struct {
			AccessToken      string   `json:"accessToken"`
			ExpiresAt        int64    `json:"expiresAt"`
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
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			AccountID    string `json:"account_id"`
		} `json:"tokens"`
		OwnedBy string `json:"owned_by"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	// .
	// .
	// .
	if f.Tokens.AccessToken == "" && f.APIKey != "" {
		return &state{access: f.APIKey, isAPIKey: true}, nil
	}
	st := &state{access: f.Tokens.AccessToken, refresh: f.Tokens.RefreshToken, owned: f.OwnedBy == "aii-os"}
	if f.Tokens.AccountID != "" {
		st.account = f.Tokens.AccountID
	}
	// .
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

// .
// .
// .
// .
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

// .
// .
// .
// .
type Info struct {
	Kind      string    `json:"kind"`
	Plan      string    `json:"plan,omitempty"`
	Tier      string    `json:"tier,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	IsAPIKey  bool      `json:"is_api_key,omitempty"`
	Path      string    `json:"path,omitempty"`
	// .
	// .
	// .
	Error string `json:"error,omitempty"`
}

// .
// .
// .
// .
// .
// .
func (s *Source) Info() Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := Info{Kind: s.kind, Path: s.path}
	if _, err := s.load(); err != nil {
		i.Error = err.Error()
	}
	if s.st == nil {
		return i
	}
	i.Plan, i.Tier, i.ExpiresAt, i.IsAPIKey = s.st.plan, s.st.tier, s.st.expires, s.st.isAPIKey
	return i
}

// .
// .
func parseGeneric(raw []byte) (*state, error) {
	if st, err := parseClaudeCode(raw); err == nil && st.access != "" {
		return st, nil
	}
	if st, err := parseCodex(raw); err == nil && st.access != "" {
		return st, nil
	}
	var f struct {
		AccessToken  string            `json:"access_token"`
		RefreshToken string            `json:"refresh_token"`
		ExpiresAt    int64             `json:"expires_at"`
		ExpiresAtMS  int64             `json:"expires_at_ms"`
		Scope        string            `json:"scope"`
		Headers      map[string]string `json:"headers"`
		OwnedBy      string            `json:"owned_by"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	// .
	// .
	// .
	st := &state{access: f.AccessToken, refresh: f.RefreshToken, headers: f.Headers, owned: f.OwnedBy == "aii-os"}
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
	// .
	if st.expires.IsZero() {
		if c, err := jwtClaims(st.access); err == nil {
			if exp, ok := c["exp"].(float64); ok && exp > 0 {
				st.expires = time.Unix(int64(exp), 0)
			}
		}
	}
	return st, nil
}

// .
// .
// .
// .
// .
// .
// .
// .
func Dialect(kind string, overrides ...map[string]string) string {
	sp, err := specFor(kind)
	if err != nil {
		return ""
	}
	for _, ov := range overrides {
		sp, err = applyOverrides(sp, ov)
		if err != nil {
			return ""
		}
	}
	return sp.dialect
}

// .
func OverrideParams(base OAuthParams, opts map[string]string) (OAuthParams, error) {
	filtered := map[string]string{}
	for k, v := range opts {
		if strings.HasPrefix(k, "oauth_") {
			filtered[k] = v
		}
	}
	sp, err := applyOverrides(spec{oauth: base}, filtered)
	if err != nil {
		return OAuthParams{}, err
	}
	p := sp.oauth
	p.AuthorizeParams = copyMap(p.AuthorizeParams)
	if p.AuthorizeParams == nil {
		p.AuthorizeParams = map[string]string{}
	}
	if p.Originator != "" {
		p.AuthorizeParams["originator"] = p.Originator
		p.Originator = ""
	}
	if _, set := filtered["oauth_id_token_add_organizations"]; set {
		p.AuthorizeParams["id_token_add_organizations"] = filtered["oauth_id_token_add_organizations"]
		p.IDTokenAddOrganizations = false
	}
	if len(p.AccountClaim) > 0 && opts["account_header"] != "" {
		p.ClaimHeaders = copyPaths(p.ClaimHeaders)
		if p.ClaimHeaders == nil {
			p.ClaimHeaders = map[string][]string{}
		}
		p.ClaimHeaders[opts["account_header"]] = p.AccountClaim
	}
	return p, nil
}

func validHeader(name, value string) bool {
	if name == "" || strings.ContainsAny(value, "\r\n\x00") {
		return false
	}
	for _, c := range name {
		if c <= 32 || c >= 127 || strings.ContainsRune("()<>@,;:\"/[]?={}\t", c) {
			return false
		}
	}
	return !strings.EqualFold(name, "Authorization") && !strings.EqualFold(name, "Host") && !strings.EqualFold(name, "Proxy-Authorization")
}
