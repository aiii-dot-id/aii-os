package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/fileperm"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// .
// .
// .
// .
// .
// .

// .
type CodexTokens struct {
	Access    string
	Refresh   string
	IDToken   string
	Expires   time.Time
	AccountID string
}

// .
type Login struct {
	params   OAuthParams
	verifier string
	state    string
	URL      string
}

// .
var codexHTTP = &http.Client{Timeout: 30 * time.Second}

func randomURLSafe(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// .
// .
func NewLogin(p OAuthParams) (*Login, error) {
	if !p.Complete() {
		return nil, errors.New("this credential has no native sign-in configured (oauth_client_id, oauth_authorize_url, oauth_token_url, oauth_redirect_uri, oauth_scope)")
	}
	verifier, err := randomURLSafe(64)
	if err != nil {
		return nil, err
	}
	state, err := randomURLSafe(24)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	u, err := url.Parse(p.AuthorizeURL)
	if err != nil {
		return nil, fmt.Errorf("authorize url: %w", err)
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", p.ClientID)
	q.Set("redirect_uri", p.RedirectURI)
	q.Set("scope", p.Scope)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	if p.IDTokenAddOrganizations {
		q.Set("id_token_add_organizations", "true")
	}
	if p.Originator != "" {
		q.Set("originator", p.Originator)
	}
	for k, v := range p.AuthorizeParams {
		if q.Has(k) {
			return nil, fmt.Errorf("oauth_authorize_params may not set the protocol field %q", k)
		}
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return &Login{params: p, verifier: verifier, state: state, URL: u.String()}, nil
}

// .
func (l *Login) State() string { return l.state }

// .
// .
// .
func ParseAuthorizationInput(input string) (code, state string, err error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", "", errors.New("nothing to parse")
	}
	if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
		u, err := url.Parse(input)
		if err != nil {
			return "", "", err
		}
		code, state = u.Query().Get("code"), u.Query().Get("state")
		if code == "" || state == "" {
			return "", "", errors.New("the pasted URL must carry both code and state")
		}
		return code, state, nil
	}
	if i := strings.IndexByte(input, '#'); i > 0 && i < len(input)-1 {
		return input[:i], input[i+1:], nil
	}
	return "", "", errors.New("paste the full redirect URL — it carries the state that binds the code to this sign-in; a bare code is not enough")
}

// .
// .
func (l *Login) Complete(ctx context.Context, code, state string) (*CodexTokens, error) {
	// .
	// .
	// .
	// .
	if state == "" || state != l.state {
		return nil, errors.New("state missing or mismatched — this response is not for this sign-in")
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {l.params.ClientID},
		"code":          {code},
		"code_verifier": {l.verifier},
		"redirect_uri":  {l.params.RedirectURI},
	}
	return tokenRequest(ctx, l.params, form)
}

// .
func Refresh(ctx context.Context, p OAuthParams, refreshToken string) (*CodexTokens, error) {
	if p.TokenURL == "" || p.ClientID == "" {
		return nil, errors.New("no token endpoint configured for refresh")
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {p.ClientID},
	}
	return tokenRequest(ctx, p, form)
}

// .
// .
// .
func tokenRequest(ctx context.Context, p OAuthParams, form url.Values) (*CodexTokens, error) {
	g, err := exchange(ctx, codexHTTP, p, form)
	if err != nil {
		return nil, err
	}
	if g.Refresh == "" || g.Expires.IsZero() {
		return nil, errors.New("token response is missing access_token, refresh_token or expires_in")
	}
	t := &CodexTokens{Access: g.Access, Refresh: g.Refresh, IDToken: g.IDToken, Expires: g.Expires}
	t.AccountID = accountIDFromToken(g.Access)
	if t.AccountID == "" {
		return nil, errors.New("the access token carries no chatgpt_account_id claim")
	}
	return t, nil
}

// .
// .
func accountIDFromToken(tok string) string {
	c, err := jwtClaims(tok)
	if err != nil {
		return ""
	}
	if auth, ok := c["https://api.openai.com/auth"].(map[string]any); ok {
		if id, ok := auth["chatgpt_account_id"].(string); ok {
			return id
		}
	}
	return ""
}

// .
// .
// .
// .
func (l *Login) ServeCallback(ctx context.Context) (code string, err error) {
	u, err := url.Parse(l.params.RedirectURI)
	if err != nil {
		return "", err
	}
	ln, err := net.Listen("tcp", u.Host)
	if err != nil {
		return "", fmt.Errorf("cannot listen for the sign-in redirect on %s (paste the redirect URL instead): %w", u.Host, err)
	}
	got := make(chan string, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != u.Path {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		if q.Get("state") != l.state || q.Get("code") == "" {
			http.Error(w, "sign-in response does not match this attempt", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, "<!doctype html><title>AII OS</title><p>Signed in. You can close this tab and return to AII OS.</p>")
		select {
		case got <- q.Get("code"):
		default:
		}
	})}
	go srv.Serve(ln)
	// .
	// .
	// .
	// .
	// .
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := srv.Shutdown(sctx); err != nil {
			srv.Close()
		}
	}()
	select {
	case code = <-got:
		return code, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// .
// .
// .
func WriteAuthFile(path string, t *CodexTokens) error {
	doc := map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]any{
			"access_token":  t.Access,
			"refresh_token": t.Refresh,
			"id_token":      t.IDToken,
			"account_id":    t.AccountID,
		},
		"last_refresh": time.Now().UTC().Format(time.RFC3339Nano),
		"owned_by":     "aii-os",
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return writePrivate(path, raw)
}

// .
// .
// .
func writePrivate(path string, raw []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// .
	// .
	// .
	// .
	// .
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	// .
	// .
	// .
	// .
	// .
	if err := fileperm.RestrictToOwner(f); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// .
	// .
	// .
	// .
	published, err := atomicfile.Replace(tmp, path)
	if err != nil {
		if published {
			return fmt.Errorf("credential published but not durable: %w", err)
		}
		return err
	}
	return nil
}

// .
// .
func scrubAuthorityError(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	s = tokenShaped.ReplaceAllString(s, "<redacted>")
	return s
}

var tokenShaped = regexp.MustCompile(`(eyJ[A-Za-z0-9_-]{8,}[A-Za-z0-9._-]*|sk-[A-Za-z0-9_-]{8,})`)
