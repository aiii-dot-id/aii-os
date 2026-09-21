package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
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
// .

const webhookTimeout = 30 * time.Second

// .
// .
type webhookResult struct {
	Arrival  *arrival `json:"arrival"`
	Response *struct {
		Status      int    `json:"status"`
		ContentType string `json:"content_type"`
		Body        string `json:"body"`
	} `json:"response"`
}

// .
func (a *App) handleWebhook(w http.ResponseWriter, r *http.Request, pluginID, hookPath string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "webhooks are POST", http.StatusMethodNotAllowed)
		return
	}
	ap := a.activePlugin(pluginID)
	if ap == nil {
		http.NotFound(w, r)
		return
	}
	var decl *pluginhost.WebhookDecl
	for i := range ap.Webhooks {
		if ap.Webhooks[i].Path == hookPath {
			decl = &ap.Webhooks[i]
		}
	}
	if decl == nil {
		http.NotFound(w, r)
		return
	}
	limit := decl.MaxBodyBytes
	if limit <= 0 {
		limit = pluginhost.DefaultWebhookBytes
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, int64(limit)+1))
	if err != nil {
		http.Error(w, "unreadable body", http.StatusBadRequest)
		return
	}
	if len(body) > limit {
		http.Error(w, "body over the declared ceiling", http.StatusRequestEntityTooLarge)
		return
	}
	// .
	// .
	// .
	// .
	secret, why := a.webhookSecret(pluginID, decl.Signature.SecretSetting)
	if secret == "" {
		logsink.Warn("webhook.refusal", "%s/%s: no secret to verify with (%s) — refused", pluginID, hookPath, why)
		http.Error(w, "unverifiable", http.StatusUnauthorized)
		return
	}
	if !verifyWebhook(decl.Signature, secret, r, body) {
		logsink.Warn("webhook.refusal", "%s/%s: signature FAILED — refused, the operation did not run", pluginID, hookPath)
		http.Error(w, "signature failed", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), webhookTimeout)
	defer cancel()
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
	authHeader := ""
	if decl.Signature != nil {
		authHeader = strings.ToLower(strings.TrimSpace(decl.Signature.Header))
	}
	headers := map[string]interface{}{}
	for _, name := range []string{"Content-Type", "User-Agent", "X-Request-Id"} {
		if authHeader != "" && strings.ToLower(name) == authHeader {
			continue
		}
		if v := r.Header.Get(name); v != "" {
			headers[name] = v
		}
	}
	args := map[string]interface{}{
		"method":  r.Method,
		"path":    hookPath,
		"query":   r.URL.RawQuery,
		"headers": headers,
		"body":    string(body),
	}
	res, err := a.toolReg.Execute(ctx, pluginhost.ToolNameFor(pluginID, decl.Operation), args)
	if err != nil || res.Error != "" {
		logsink.Warn("webhook.error", "%s/%s: the operation failed (%v %s) — the sender may retry", pluginID, hookPath, err, res.Error)
		http.Error(w, "the operation failed", http.StatusInternalServerError)
		return
	}
	var out webhookResult
	if strings.TrimSpace(res.Output) != "" {
		if uerr := json.Unmarshal([]byte(res.Output), &out); uerr != nil {
			logsink.Warn("webhook.error", "%s/%s: the operation's result is not a webhook result: %v", pluginID, hookPath, uerr)
			http.Error(w, "the operation answered badly", http.StatusInternalServerError)
			return
		}
	}
	if out.Arrival != nil {
		if out.Arrival.ID == "" || out.Arrival.From == "" {
			logsink.Warn("webhook.refusal", "%s/%s: dropping an arrival with no id or sender", pluginID, hookPath)
		} else {
			route := a.webhookRoute(ctx, pluginID)
			rowID := "in_" + route.Channel + "_" + out.Arrival.ID
			fresh, rerr := a.store.RecordInbound(rowID, route.Channel, out.Arrival.From, out.Arrival.Body)
			if rerr != nil {
				logsink.Warn("webhook.error", "%s/%s: could not record the arrival: %v", pluginID, hookPath, rerr)
				http.Error(w, "the arrival could not be recorded", http.StatusInternalServerError)
				return
			}
			if fresh {
				a.carryInbound(rowID, route, *out.Arrival)
			}
		}
	}
	status, ctype, respBody := http.StatusOK, "text/plain; charset=utf-8", ""
	if out.Response != nil {
		if out.Response.Status >= 200 && out.Response.Status <= 599 {
			status = out.Response.Status
		}
		if out.Response.ContentType != "" {
			ctype = out.Response.ContentType
		}
		respBody = out.Response.Body
	}
	w.Header().Set("Content-Type", ctype)
	w.WriteHeader(status)
	_, _ = io.WriteString(w, respBody)
}

// .
// .
// .
func (a *App) webhookRoute(ctx context.Context, pluginID string) channelRoute {
	for _, r := range a.channelRoutes(ctx) {
		if r.Plugin == pluginID {
			return r
		}
	}
	return channelRoute{Channel: "hook:" + pluginID, Plugin: pluginID, Hook: true}
}

// .
// .
// .
func (a *App) webhookSecret(pluginID, setting string) (string, string) {
	c := a.configSnapshot()
	handle, _ := c.Plugins.Settings[pluginID][setting].(string)
	if handle == "" {
		return "", fmt.Sprintf("setting %s names no credential handle", setting)
	}
	admitted := false
	for _, h := range c.Plugins.Grants[pluginID].CredentialHandles {
		if h == handle {
			admitted = true
		}
	}
	if !admitted {
		return "", fmt.Sprintf("handle %s is not in the plugin's grant", handle)
	}
	prof, ok := c.Plugins.AuthProfiles[handle]
	if !ok {
		return "", fmt.Sprintf("handle %s names no auth profile", handle)
	}
	var secret string
	switch {
	case prof.SecretEnv != "":
		secret = os.Getenv(prof.SecretEnv)
	case prof.SecretFile != "":
		raw, err := os.ReadFile(prof.SecretFile)
		if err != nil {
			return "", "the secret file is unavailable"
		}
		secret = strings.TrimSpace(string(raw))
	}
	if secret == "" {
		return "", "the secret is unavailable"
	}
	return secret, ""
}

// .
func verifyWebhook(sig *pluginhost.WebhookSignature, secret string, r *http.Request, body []byte) bool {
	got := r.Header.Get(sig.Header)
	if got == "" {
		return false
	}
	if sig.Prefix != "" {
		if !strings.HasPrefix(got, sig.Prefix) {
			return false
		}
		got = strings.TrimPrefix(got, sig.Prefix)
	}
	var want string
	switch sig.Scheme {
	case pluginhost.SchemeHMACSHA256Hex:
		m := hmac.New(sha256.New, []byte(secret))
		m.Write(body)
		want = hex.EncodeToString(m.Sum(nil))
	case pluginhost.SchemeHMACSHA256Base64:
		m := hmac.New(sha256.New, []byte(secret))
		m.Write(body)
		want = base64.StdEncoding.EncodeToString(m.Sum(nil))
	case pluginhost.SchemeTwilio:
		// .
		// .
		scheme := "https"
		if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") == "" {
			scheme = "http"
		}
		if fp := r.Header.Get("X-Forwarded-Proto"); fp != "" {
			scheme = fp
		}
		signed := scheme + "://" + r.Host + r.URL.RequestURI()
		if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
			form := parseForm(string(body))
			keys := make([]string, 0, len(form))
			for k := range form {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				for _, v := range form[k] {
					signed += k + v
				}
			}
		}
		m := hmac.New(sha1.New, []byte(secret))
		m.Write([]byte(signed))
		want = base64.StdEncoding.EncodeToString(m.Sum(nil))
	case pluginhost.SchemeToken:
		want = secret
	default:
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.ToLower(got)), []byte(strings.ToLower(want))) == 1 && sig.Scheme != pluginhost.SchemeToken ||
		sig.Scheme == pluginhost.SchemeToken && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func parseForm(body string) map[string][]string {
	out := map[string][]string{}
	for _, pair := range strings.Split(body, "&") {
		if pair == "" {
			continue
		}
		k, v, _ := strings.Cut(pair, "=")
		k, v = unescapeForm(k), unescapeForm(v)
		out[k] = append(out[k], v)
	}
	return out
}

func unescapeForm(s string) string {
	s = strings.ReplaceAll(s, "+", " ")
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if v, err := hex.DecodeString(s[i+1 : i+3]); err == nil {
				b.WriteByte(v[0])
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
