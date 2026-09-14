package pluginhost

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

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// .
const WebhooksFile = "webhooks.json"

const (
	MaxWebhooks         = 16
	DefaultWebhookBytes = 64 << 10
	MaxWebhookBytes     = 256 << 10
)

// .
// .
const (
	// .
	// .
	SchemeHMACSHA256Hex = "hmac-sha256-hex"
	// .
	SchemeHMACSHA256Base64 = "hmac-sha256-base64"
	// .
	// .
	SchemeTwilio = "twilio"
	// .
	// .
	SchemeToken = "token"
)

var (
	reWebhookPath = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}(/[a-z0-9][a-z0-9_-]{0,63}){0,3}$`)
	reHeaderName  = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
)

// .
type WebhookSignature struct {
	Scheme        string `json:"scheme"`
	Header        string `json:"header"`
	Prefix        string `json:"prefix,omitempty"`
	SecretSetting string `json:"secret_setting"`
}

// .
// .
// .
// .
type WebhookDecl struct {
	Path         string            `json:"path"`
	Operation    string            `json:"operation"`
	Signature    *WebhookSignature `json:"signature"`
	MaxBodyBytes int               `json:"max_body_bytes,omitempty"`
}

// .
// .
type WebhooksError struct {
	PluginID string
	Detail   string
}

func (e *WebhooksError) Error() string {
	return fmt.Sprintf("pluginhost: %s: %s is not a webhook declaration the host honors: %s", e.PluginID, WebhooksFile, e.Detail)
}

// .
// .
// .
func ParseWebhooks(raw []byte, methods []string, settings []SettingDecl) ([]WebhookDecl, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var decls []WebhookDecl
	if err := dec.Decode(&decls); err != nil {
		return nil, fmt.Errorf("not a list of webhooks: %v", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("trailing content after the list")
	}
	if len(decls) > MaxWebhooks {
		return nil, fmt.Errorf("%d webhooks; at most %d", len(decls), MaxWebhooks)
	}
	known := map[string]bool{}
	for _, m := range methods {
		known[m] = true
	}
	secrets := map[string]bool{}
	for _, s := range settings {
		if s.Type == SettingSecret {
			secrets[s.Key] = true
		}
	}
	paths := map[string]bool{}
	for i, d := range decls {
		if !reWebhookPath.MatchString(d.Path) {
			return nil, fmt.Errorf("webhook %d: path %q is not lowercase segments (at most four, 64 bytes each)", i, d.Path)
		}
		if paths[d.Path] {
			return nil, fmt.Errorf("webhook path %q declared twice", d.Path)
		}
		paths[d.Path] = true
		if !known[d.Operation] {
			return nil, fmt.Errorf("webhook %q: operation %q is not a method this package declares", d.Path, d.Operation)
		}
		if d.Signature == nil {
			return nil, fmt.Errorf("webhook %q: a signature is required; the public origin is public", d.Path)
		}
		switch d.Signature.Scheme {
		case SchemeHMACSHA256Hex, SchemeHMACSHA256Base64, SchemeTwilio, SchemeToken:
		default:
			return nil, fmt.Errorf("webhook %q: scheme %q is not hmac-sha256-hex, hmac-sha256-base64, twilio or token", d.Path, d.Signature.Scheme)
		}
		if !reHeaderName.MatchString(d.Signature.Header) {
			return nil, fmt.Errorf("webhook %q: header %q is not a header name", d.Path, d.Signature.Header)
		}
		if len(d.Signature.Prefix) > 32 {
			return nil, fmt.Errorf("webhook %q: prefix over 32 bytes", d.Path)
		}
		if !secrets[d.Signature.SecretSetting] {
			return nil, fmt.Errorf("webhook %q: secret_setting %q is not a secret-typed setting this package declares", d.Path, d.Signature.SecretSetting)
		}
		if d.MaxBodyBytes < 0 || d.MaxBodyBytes > MaxWebhookBytes {
			return nil, fmt.Errorf("webhook %q: max_body_bytes must be 0..%d", d.Path, MaxWebhookBytes)
		}
	}
	return decls, nil
}

// .
func loadWebhooks(pkgPath string, res *packagefmt.Result, m *packagefmt.Manifest, settings []SettingDecl) ([]WebhookDecl, error) {
	if _, present := res.FileDigests[WebhooksFile]; !present {
		return nil, nil
	}
	raw, err := loadVerifiedMember(pkgPath, res, WebhooksFile)
	if err != nil {
		return nil, err
	}
	var methods []string
	for _, decl := range append(append([]packagefmt.InterfaceDecl{}, m.Interfaces.Core...), m.Interfaces.Optional...) {
		methods = append(methods, decl.Methods...)
	}
	decls, err := ParseWebhooks(raw, methods, settings)
	if err != nil {
		return nil, &WebhooksError{PluginID: m.ID, Detail: err.Error()}
	}
	return decls, nil
}

// .
// .
func ToolNameFor(pluginID, method string) string {
	name, _ := toolName(pluginID, method)
	return name
}
