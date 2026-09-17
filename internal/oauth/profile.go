package oauth

// .
// .
// .

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
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
func exchange(ctx context.Context, client *http.Client, p OAuthParams, fields map[string]any) (*Tokens, error) {
	if p.TokenURL == "" {
		return nil, errors.New("no token endpoint configured")
	}
	for name, value := range p.ResourceHeaders {
		if !validHeader(name, value) {
			return nil, fmt.Errorf("invalid configured resource header %q", name)
		}
	}
	for name, path := range p.ClaimHeaders {
		if !validHeader(name, "") || len(path) == 0 {
			return nil, fmt.Errorf("invalid configured claim header %q", name)
		}
	}
	if p.ClientSecret != "" {
		fields["client_secret"] = p.ClientSecret
	}
	raw, contentType, err := encodeGrant(p.TokenEncoding, fields)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TokenURL, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	for name, value := range p.TokenHeaders {
		if !validHeader(name, value) || strings.EqualFold(name, "Content-Type") || strings.EqualFold(name, "Content-Length") {
			return nil, fmt.Errorf("invalid configured token header %q (token_encoding controls Content-Type)", name)
		}
		req.Header.Set(name, value)
	}
	if client == nil {
		client = signInHTTP
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
	if len(p.AccountClaim) > 0 && claimString(t.Access, p.AccountClaim) == "" {
		return nil, fmt.Errorf("access token lacks required claim %s", strings.Join(p.AccountClaim, "."))
	}
	for _, path := range p.ClaimHeaders {
		if claimString(t.Access, path) == "" {
			return nil, fmt.Errorf("access token lacks required claim %s", strings.Join(path, "."))
		}
	}
	return t, nil
}

// .
// .
func (l *Login) Exchange(ctx context.Context, client *http.Client, code, state string) (*Tokens, error) {
	if state == "" || state != l.state {
		return nil, errors.New("state missing or mismatched — this response is not for this sign-in")
	}
	return exchangeCode(ctx, client, l.params, code, l.verifier, l.state)
}

// .
// .
// .
func ExchangeCode(ctx context.Context, client *http.Client, p OAuthParams, code, verifier string) (*Tokens, error) {
	return exchangeCode(ctx, client, p, code, verifier, "")
}

func exchangeCode(ctx context.Context, client *http.Client, p OAuthParams, code, verifier, state string) (*Tokens, error) {
	if code == "" || verifier == "" || p.ClientID == "" || p.RedirectURI == "" {
		return nil, errors.New("code exchange requires code, verifier, client id and redirect URI")
	}
	fields := map[string]any{"grant_type": "authorization_code", "client_id": p.ClientID, "code": code, "code_verifier": verifier, "redirect_uri": p.RedirectURI}
	if err := addGrantParams(fields, p.TokenParams, map[string]string{"{state}": state, "{scope}": p.Scope}); err != nil {
		return nil, fmt.Errorf("token_params: %w", err)
	}
	tokens, err := exchange(ctx, client, p, fields)
	if err == nil && tokens.Scope == "" {
		// .
		tokens.Scope = p.Scope
	}
	return tokens, err
}

// .
func refreshWith(ctx context.Context, client *http.Client, p OAuthParams, refreshToken string) (*Tokens, error) {
	if p.ClientID == "" {
		return nil, errors.New("no client id configured for refresh")
	}
	fields := map[string]any{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     p.ClientID,
	}
	if err := addGrantParams(fields, p.RefreshParams, map[string]string{"{scope}": p.Scope}); err != nil {
		return nil, fmt.Errorf("refresh_params: %w", err)
	}
	return exchange(ctx, client, p, fields)
}

// .
// .
func addGrantParams(fields, extra map[string]any, bindings map[string]string) error {
	for key, value := range extra {
		switch key {
		case "grant_type", "client_id", "client_secret", "code", "code_verifier", "redirect_uri", "refresh_token", "device_code":
			return fmt.Errorf("may not replace protocol field %q", key)
		}
		if key == "" {
			return errors.New("parameter names must not be empty")
		}
		if s, ok := value.(string); ok && (s == "{state}" || s == "{scope}") {
			bound := bindings[s]
			if bound == "" {
				return fmt.Errorf("parameter %q requires unavailable %s", key, s)
			}
			value = bound
		}
		fields[key] = value
	}
	return nil
}

func encodeGrant(encoding string, fields map[string]any) ([]byte, string, error) {
	switch encoding {
	case "json":
		raw, err := json.Marshal(fields)
		return raw, "application/json", err
	case "", "form":
		form := url.Values{}
		for key, value := range fields {
			if s, ok := value.(string); ok {
				form.Set(key, s)
				continue
			}
			raw, err := json.Marshal(value)
			if err != nil {
				return nil, "", fmt.Errorf("encode token parameter %q: %w", key, err)
			}
			form.Set(key, string(raw))
		}
		return []byte(form.Encode()), "application/x-www-form-urlencoded", nil
	default:
		return nil, "", fmt.Errorf("unknown token_encoding %q (want form or json)", encoding)
	}
}

// .
// .
func RefreshTokens(ctx context.Context, client *http.Client, p OAuthParams, refreshToken string) (*Tokens, error) {
	return refreshWith(ctx, client, p, refreshToken)
}

// .
// .
func WriteTokenFile(path string, t *Tokens) error {
	tokenFileMu.Lock()
	defer tokenFileMu.Unlock()
	return writeTokenFile(path, t)
}

func writeTokenFile(path string, t *Tokens) error {
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
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	s, err := NewOwnedConfigured("file", abs, p)
	if err != nil {
		return nil, err
	}
	s.SetHTTPClient(client)
	return s, nil
}
