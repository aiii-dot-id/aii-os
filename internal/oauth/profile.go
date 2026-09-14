package oauth

// .
// .
// .
// .
// .
// .
// .
// .

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// .
type Tokens struct {
	Access  string
	Refresh string
	IDToken string
	Scope   string
	// .
	// .
	// .
	Expires time.Time
}

// .
// .
// .
var ErrGrantInvalid = errors.New("the authority no longer accepts this credential (invalid_grant)")

// .
// .
func exchange(ctx context.Context, client *http.Client, p OAuthParams, form url.Values) (*Tokens, error) {
	if p.TokenURL == "" {
		return nil, errors.New("no token endpoint configured")
	}
	if p.ClientSecret != "" {
		form.Set("client_secret", p.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if client == nil {
		client = codexHTTP
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var e struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(body, &e)
	switch e.Error {
	case "authorization_pending", "slow_down", "access_denied", "expired_token":
		// .
		// .
		return nil, devicePending{code: e.Error}
	}
	if resp.StatusCode != http.StatusOK {
		if e.Error == "invalid_grant" {
			return nil, fmt.Errorf("token endpoint answered %d: %w", resp.StatusCode, ErrGrantInvalid)
		}
		return nil, fmt.Errorf("token endpoint answered %d: %s", resp.StatusCode, scrubAuthorityError(body))
	}
	var j struct {
		AccessToken  string  `json:"access_token"`
		RefreshToken string  `json:"refresh_token"`
		IDToken      string  `json:"id_token"`
		Scope        string  `json:"scope"`
		ExpiresIn    float64 `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &j); err != nil {
		return nil, fmt.Errorf("token response: %w", err)
	}
	if j.AccessToken == "" {
		return nil, errors.New("token response is missing access_token")
	}
	t := &Tokens{Access: j.AccessToken, Refresh: j.RefreshToken, IDToken: j.IDToken, Scope: j.Scope}
	if j.ExpiresIn > 0 {
		t.Expires = time.Now().Add(time.Duration(j.ExpiresIn * float64(time.Second)))
	}
	return t, nil
}

// .
// .
func (l *Login) Exchange(ctx context.Context, client *http.Client, code, state string) (*Tokens, error) {
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
	return exchange(ctx, client, l.params, form)
}

// .
func refreshWith(ctx context.Context, client *http.Client, p OAuthParams, refreshToken string) (*Tokens, error) {
	if p.ClientID == "" {
		return nil, errors.New("no client id configured for refresh")
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {p.ClientID},
	}
	return exchange(ctx, client, p, form)
}

// .
// .
func RefreshTokens(ctx context.Context, client *http.Client, p OAuthParams, refreshToken string) (*Tokens, error) {
	return refreshWith(ctx, client, p, refreshToken)
}

// .
// .
func WriteTokenFile(path string, t *Tokens) error {
	doc := map[string]any{
		"access_token":  t.Access,
		"refresh_token": t.Refresh,
		"owned_by":      "aii-os",
		"written_at":    time.Now().UTC().Format(time.RFC3339Nano),
	}
	if t.IDToken != "" {
		doc["id_token"] = t.IDToken
	}
	if t.Scope != "" {
		doc["scope"] = t.Scope
	}
	if !t.Expires.IsZero() {
		doc["expires_at"] = t.Expires.Unix()
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
// .
// .
// .
func NewProfileSource(path string, p OAuthParams, client *http.Client) (*Source, error) {
	if path == "" {
		return nil, errors.New("a profile source needs its token file")
	}
	if p.TokenURL == "" || p.ClientID == "" {
		return nil, errors.New("a profile source needs a token endpoint and a client id to refresh")
	}
	s := &Source{kind: KindFilePrefix + path, sp: spec{abs: path, parse: parseGeneric, oauth: p, generic: true}, path: path, owned: true, client: client}
	if _, err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}
