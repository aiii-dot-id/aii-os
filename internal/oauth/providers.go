package oauth

import (
	"sort"
	"strings"
)

// .
// .
// .
// .
// .
// .
// .

// .
// .
type ScopeSet struct {
	Read   []string `json:"read,omitempty"`
	Modify []string `json:"modify,omitempty"`
}

// .
type Provider struct {
	Name                    string              `json:"name,omitempty"`
	ClientID                string              `json:"client_id,omitempty"`
	AuthorizeURL            string              `json:"authorize_url,omitempty"`
	TokenURL                string              `json:"token_url,omitempty"`
	RedirectURI             string              `json:"redirect_uri,omitempty"`
	SignIn                  string              `json:"sign_in,omitempty"`
	DeviceExpiresSeconds    int                 `json:"device_expires_seconds,omitempty"`
	DeviceURL               string              `json:"device_url,omitempty"`
	DevicePollURL           string              `json:"device_poll_url,omitempty"`
	VerificationURI         string              `json:"verification_uri,omitempty"`
	DeviceRedirectURI       string              `json:"device_redirect_uri,omitempty"`
	RevokeURL               string              `json:"revoke_url,omitempty"`
	CredentialFile          string              `json:"credential_file,omitempty"`
	AuthorizeParams         map[string]string   `json:"authorize_params,omitempty"`
	TokenEncoding           string              `json:"token_encoding,omitempty"`
	TokenHeaders            map[string]string   `json:"token_headers,omitempty"`
	TokenParams             map[string]any      `json:"token_params,omitempty"`
	RefreshParams           map[string]any      `json:"refresh_params,omitempty"`
	ResourceHeaders         map[string]string   `json:"resource_headers,omitempty"`
	ClaimHeaders            map[string][]string `json:"claim_headers,omitempty"`
	BaseScopes              []string            `json:"scopes,omitempty"`
	Hosts                   []string            `json:"hosts,omitempty"`
	Scopes                  map[string]ScopeSet `json:"services,omitempty"`
	DeviceScopesUnsupported []string            `json:"device_scopes_unsupported,omitempty"`
}

// .
// .
func (p Provider) Params() OAuthParams {
	return OAuthParams{ClientID: p.ClientID, AuthorizeURL: p.AuthorizeURL, TokenURL: p.TokenURL,
		RedirectURI: p.RedirectURI, Scope: strings.Join(p.BaseScopes, " "), AuthorizeParams: copyMap(p.AuthorizeParams),
		TokenEncoding: p.TokenEncoding, TokenHeaders: copyMap(p.TokenHeaders),
		TokenParams: copyParams(p.TokenParams), RefreshParams: copyParams(p.RefreshParams),
		ResourceHeaders: copyMap(p.ResourceHeaders), ClaimHeaders: copyPaths(p.ClaimHeaders)}
}

func copyPaths(in map[string][]string) map[string][]string {
	if in == nil {
		return nil
	}
	out := make(map[string][]string, len(in))
	for k, v := range in {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// .
// .
// .
func ProviderTemplate(name string, catalog map[string]Provider) (Provider, bool) {
	p, ok := catalog[name]
	if !ok {
		return Provider{}, false
	}
	// .
	out := p
	out.AuthorizeParams = copyMap(p.AuthorizeParams)
	out.TokenHeaders = copyMap(p.TokenHeaders)
	out.TokenParams = copyParams(p.TokenParams)
	out.RefreshParams = copyParams(p.RefreshParams)
	out.ResourceHeaders = copyMap(p.ResourceHeaders)
	out.ClaimHeaders = copyPaths(p.ClaimHeaders)
	out.BaseScopes = append([]string(nil), p.BaseScopes...)
	out.Hosts = append([]string(nil), p.Hosts...)
	out.DeviceScopesUnsupported = append([]string(nil), p.DeviceScopesUnsupported...)
	out.Scopes = make(map[string]ScopeSet, len(p.Scopes))
	for k, v := range p.Scopes {
		out.Scopes[k] = ScopeSet{Read: append([]string(nil), v.Read...), Modify: append([]string(nil), v.Modify...)}
	}
	return out, true
}

// .
func ProviderNames(catalog map[string]Provider) []string {
	names := make([]string, 0, len(catalog))
	for n := range catalog {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func copyMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// .
// .
func copyParams(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = copyParam(v)
	}
	return out
}

func copyParam(v any) any {
	switch v := v.(type) {
	case map[string]any:
		return copyParams(v)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = copyParam(item)
		}
		return out
	default:
		return v
	}
}
