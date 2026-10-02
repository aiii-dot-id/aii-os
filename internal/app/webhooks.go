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

const webhookTimeout = 30 * time.Second

const maxWebhookArrivals = 50

type webhookResult struct {
	Arrival  *arrival  `json:"arrival"`
	Arrivals []arrival `json:"arrivals"`
	Response *struct {
		Status      int    `json:"status"`
		ContentType string `json:"content_type"`
		Body        string `json:"body"`
	} `json:"response"`
}

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
	ctx, cancel := context.WithTimeout(r.Context(), webhookTimeout)
	defer cancel()

	if decl.Signature.Scheme == pluginhost.SchemeGoogleOIDC {
		if err := a.verifyGoogleOIDC(ctx, pluginID, hookPath, decl.Signature, r); err != nil {
			logsink.Warn("webhook.refusal", "%s/%s: %v — refused, the operation did not run", pluginID, hookPath, err)
			http.Error(w, "unverifiable", http.StatusUnauthorized)
			return
		}
	} else {
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
	}

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

	route := a.webhookRoute(ctx, pluginID)
	if route.Acknowledges {
		args["recorded"] = a.heldFor(pluginID)
	}
	res, err := a.toolReg.Execute(ctx, pluginhost.ToolNameFor(pluginID, decl.Operation), args)
	if err != nil || res.Error != "" {
		logsink.Warn("webhook.error", "%s/%s: the operation failed (%v %s) — the sender may retry", pluginID, hookPath, err, res.Error)
		http.Error(w, "the operation failed", http.StatusInternalServerError)
		return
	}
	out, terr := a.takeAnswer(route, pluginID, res.Output)
	if terr != nil {
		logsink.Warn("webhook.error", "%s/%s: %v — the sender may retry", pluginID, hookPath, terr)
		http.Error(w, "the operation's answer could not be taken", http.StatusInternalServerError)
		return
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

func (a *App) takeAnswer(route channelRoute, pluginID, output string) (webhookResult, error) {
	var out webhookResult
	if strings.TrimSpace(output) != "" {
		if err := json.Unmarshal([]byte(output), &out); err != nil {
			return out, fmt.Errorf("the operation's result is not a webhook result: %w", err)
		}
	}
	list := out.Arrivals
	if out.Arrival != nil {
		list = append([]arrival{*out.Arrival}, list...)
	}
	if len(list) > maxWebhookArrivals {
		return out, fmt.Errorf("the operation answered %d arrivals; at most %d", len(list), maxWebhookArrivals)
	}
	_, held, err := a.carryArrivals(route, list)
	if route.Acknowledges && len(held) > 0 {

		a.noteHeld(pluginID, held)
	}
	return out, err
}

func (a *App) heldFor(pluginID string) []string {
	a.channels.mu.Lock()
	defer a.channels.mu.Unlock()
	return append([]string{}, a.channels.held[pluginID]...)
}

func (a *App) noteHeld(pluginID string, held []string) {
	a.channels.mu.Lock()
	defer a.channels.mu.Unlock()
	if a.channels.held == nil {
		a.channels.held = map[string][]string{}
	}
	a.channels.held[pluginID] = held
}

func (a *App) webhookRoute(ctx context.Context, pluginID string) channelRoute {
	for _, r := range a.channelRoutes(ctx) {
		if r.Plugin == pluginID {
			return r
		}
	}
	return channelRoute{Channel: "hook:" + pluginID, Plugin: pluginID, Hook: true}
}

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

type oidcVerifier interface {
	Verify(ctx context.Context, token, audience, email string) error
}

func (a *App) verifyGoogleOIDC(ctx context.Context, pluginID, hookPath string, sig *pluginhost.WebhookSignature, r *http.Request) error {
	email, _ := a.configSnapshot().Plugins.Settings[pluginID][sig.EmailSetting].(string)
	if strings.TrimSpace(email) == "" {
		return fmt.Errorf("setting %s names no service account to verify the token against", sig.EmailSetting)
	}
	audience := a.webhookURL(pluginID, hookPath)
	if audience == "" {
		return fmt.Errorf("no advertised origin to verify the token's audience against")
	}
	if a.googleOIDC == nil {
		return fmt.Errorf("no verifier for Google's tokens")
	}
	token, ok := strings.CutPrefix(r.Header.Get(sig.Header), sig.Prefix)
	if !ok || token == "" {
		return fmt.Errorf("no bearer token")
	}
	return a.googleOIDC.Verify(ctx, token, audience, strings.TrimSpace(email))
}

func (a *App) webhookURL(pluginID, hookPath string) string {
	origin := a.advertisedOrigin()
	if origin == "" {
		return ""
	}
	return origin + "/hooks/" + pluginID + "/" + hookPath
}

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
	if sig.Scheme == pluginhost.SchemeHMACSHA256Hex {
		got = strings.ToLower(got)
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
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
