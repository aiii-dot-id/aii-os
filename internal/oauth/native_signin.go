package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/fileperm"
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
type Login struct {
	params   OAuthParams
	verifier string
	state    string
	URL      string
}

// .
var signInHTTP = &http.Client{Timeout: 30 * time.Second}

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
		return nil, errors.New("this credential has no native sign-in configured (oauth_client_id, oauth_authorize_url, oauth_token_url, oauth_redirect_uri)")
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
	return "", "", errors.New("paste the complete code shown by the provider (code#state), or the full redirect URL; a bare code is not enough")
}

// .
// .
// .
func claimString(tok string, path []string) string {
	c, err := jwtClaims(tok)
	if err != nil || len(path) == 0 {
		return ""
	}
	var cur any = map[string]any(c)
	for _, seg := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur, ok = obj[seg]
		if !ok {
			return ""
		}
	}
	s, _ := cur.(string)
	return s
}

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
