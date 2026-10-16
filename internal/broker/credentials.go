package broker

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/oauth"
)

type credentialEcho []string

func (e *credentialEcho) add(forms ...string) {
	for _, f := range forms {
		if f == "" || slices.Contains(*e, f) {
			continue
		}
		i := 0
		for i < len(*e) && len((*e)[i]) >= len(f) {
			i++
		}
		*e = slices.Insert(*e, i, f)
	}
}

func (e credentialEcho) scrub(s string) string {
	for _, f := range e {
		s = strings.ReplaceAll(s, f, CredentialPlaceholder)
	}
	return s
}

func (e credentialEcho) body(b []byte) []byte {
	if len(e) == 0 {
		return b
	}
	if json.Valid(b) {
		var out []byte
		done := 0
		for i := 0; i < len(b); i++ {
			if b[i] != '"' {
				continue
			}
			j, escaped := i+1, false
			for j < len(b) && b[j] != '"' {
				if b[j] == '\\' {
					escaped = true
					j++
				}
				j++
			}
			if j >= len(b) {
				break
			}
			var s string
			if escaped && json.Unmarshal(b[i:j+1], &s) == nil {
				if r := e.scrub(s); r != s {
					enc, _ := json.Marshal(r)
					out = append(append(out, b[done:i]...), enc...)
					done = j + 1
				}
			}
			i = j
		}
		if out != nil {
			b = append(out, b[done:]...)
		}
	}
	return []byte(e.scrub(string(b)))
}

func (e credentialEcho) partial(b []byte) int {
	keep := 0
	for _, f := range e {
		for n := min(len(f)-1, len(b)); n > keep; n-- {
			if string(b[len(b)-n:]) == f[:n] {
				keep = n
				break
			}
		}
	}
	return keep
}

type resolvedAuth struct {
	headers    map[string]string
	hosts      []string
	header     string
	pathSecret string
	echo       credentialEcho
	source     *oauth.Source
	gen        uint64
	profile    string
}

func (b *Binding) resolveAuthProfile(ctx context.Context, pol policySnapshot, name string, u *url.URL, rawURL, host string, port int, auth *resolvedAuth, local, plaintextOK bool) *outcome {
	authHeader, pathSecret := &auth.header, &auth.pathSecret
	auth.profile = name

	if !local && !b.tier.ReviewProven() {
		return &outcome{status: statusDenied, reason: reasonAuthNotAdmitted,
			detail: fmt.Sprintf("auth_profile requires a review-proven tier; %s is not", b.tier)}
	}
	admitted := false
	for _, h := range pol.grant(b.pluginID).CredentialHandles {
		if h == name {
			admitted = true
			break
		}
	}
	if !admitted {
		return &outcome{status: statusDenied, reason: reasonAuthNotAdmitted,
			detail: "auth_profile was not admitted for this plugin (plugins.grants credential_handles)"}
	}

	if u.Scheme != "https" && !(local && plaintextOK) {
		detail := "auth_profile requires an https target"
		if local {
			detail = fmt.Sprintf("auth_profile over plain http on the local network needs plugins.grants.%s.plaintext_credentials: true — a device on your network could read it", b.pluginID)
		}
		return &outcome{status: statusDenied, reason: reasonAuthRequiresHTTPS, detail: detail}
	}
	profile, ok := pol.profiles[name]
	if !ok {
		return &outcome{status: statusDenied, reason: reasonAuthUnavailable,
			detail: "no such auth profile in plugins.auth_profiles"}
	}
	if profile.Scheme == SchemeOAuth2 {
		return b.resolveOAuth2(ctx, pol, name, profile, rawURL, host, port, auth)
	}
	if profile.Host == "" || profile.Port == 0 {
		return &outcome{status: statusDenied, reason: reasonAuthInvalid,
			detail: "auth profile must pin host and port"}
	}

	if !strings.EqualFold(profile.Host, host) || profile.Port != port {
		return &outcome{status: statusDenied, reason: reasonAuthScopeMismatch,
			detail: "auth_profile host:port does not match target URL"}
	}

	var secret string
	switch {
	case profile.SecretEnv != "":
		secret = os.Getenv(profile.SecretEnv)
	case profile.SecretFile != "":
		raw, err := os.ReadFile(profile.SecretFile)
		if err != nil {
			return &outcome{status: statusDenied, reason: reasonAuthSecretMissing,
				detail: "auth_profile secret is unavailable"}
		}
		secret = strings.TrimSpace(string(raw))
	}
	if secret == "" {
		return &outcome{status: statusDenied, reason: reasonAuthSecretMissing,
			detail: "auth_profile secret is unavailable"}
	}

	if len(secret)+len(bearerPrefix) > 4096 || strings.ContainsAny(secret, "\x00\r\n") {
		return &outcome{status: statusDenied, reason: reasonNetHeaderInvalid,
			detail: "auth_profile secret is not a valid credential"}
	}
	placeholder := strings.Contains(rawURL, CredentialPlaceholder)
	switch profile.Scheme {
	case "", "bearer":
		if placeholder {
			return &outcome{status: statusDenied, reason: reasonAuthInvalid,
				detail: fmt.Sprintf("the URL names %s but auth profile %q rides as a bearer header; a credential in the path is scheme \"path\"", CredentialPlaceholder, name)}
		}
		*authHeader = bearerPrefix + secret
		auth.echo.add(secret)
	case "basic":
		if placeholder {
			return &outcome{status: statusDenied, reason: reasonAuthInvalid,
				detail: fmt.Sprintf("the URL names %s but auth profile %q rides as a basic header; a credential in the path is scheme \"path\"", CredentialPlaceholder, name)}
		}
		if !strings.Contains(secret, ":") {
			return &outcome{status: statusDenied, reason: reasonAuthInvalid,
				detail: "a basic auth profile's secret is user:password"}
		}
		encoded := base64.StdEncoding.EncodeToString([]byte(secret))
		*authHeader = "Basic " + encoded

		auth.echo.add(secret, encoded)
	case "path":

		if !placeholder {
			return &outcome{status: statusDenied, reason: reasonAuthInvalid,
				detail: fmt.Sprintf("auth profile %q rides in the path, and the URL names no %s", name, CredentialPlaceholder)}
		}
		*pathSecret = secret
		auth.echo.add(secret, url.PathEscape(secret))
	default:
		return &outcome{status: statusDenied, reason: reasonAuthInvalid,
			detail: fmt.Sprintf("auth profile scheme %q is not bearer, basic, path or oauth2", profile.Scheme)}
	}
	return nil
}

func (b *Binding) resolveOAuth2(ctx context.Context, pol policySnapshot, name string, profile AuthProfile, rawURL, host string, port int, auth *resolvedAuth) *outcome {
	if strings.Contains(rawURL, CredentialPlaceholder) {
		return &outcome{status: statusDenied, reason: reasonAuthInvalid,
			detail: fmt.Sprintf("the URL names %s but auth profile %q rides as a bearer header (oauth2)", CredentialPlaceholder, name)}
	}
	params, tpl, err := profile.Contract(pol.oauth)
	if err != nil {
		return &outcome{status: statusDenied, reason: reasonAuthInvalid, detail: err.Error()}
	}
	hp := fmt.Sprintf("%s:%d", strings.ToLower(host), port)
	pinned := false
	for _, h := range tpl.Hosts {
		if strings.EqualFold(strings.TrimSpace(h), hp) {
			pinned = true
			break
		}
	}
	if !pinned {
		return &outcome{status: statusDenied, reason: reasonAuthScopeMismatch,
			detail: fmt.Sprintf("auth profile %q rides only to its authority's hosts (%s); %s is not one of them", name, strings.Join(tpl.Hosts, ", "), hp)}
	}
	switch {
	case profile.ClientSecretEnv != "":
		params.ClientSecret = os.Getenv(profile.ClientSecretEnv)
	case profile.ClientSecretFile != "":
		raw, rerr := os.ReadFile(profile.ClientSecretFile)
		if rerr != nil {
			return &outcome{status: statusDenied, reason: reasonAuthSecretMissing, detail: "the client secret is unavailable"}
		}
		params.ClientSecret = strings.TrimSpace(string(raw))
	}
	src, o := b.host.profileSource(name, profile, params)
	if o != nil {
		return o
	}
	cred, cerr := src.Credential(ctx)
	if cerr != nil || cred.Refreshed {
		b.receiptAuthRefresh(name, cerr)
	}
	if cerr != nil {
		return oauthDenial(name, cerr)
	}
	if len(cred.Token)+len(bearerPrefix) > 4096 || strings.ContainsAny(cred.Token, "\x00\r\n") {
		return &outcome{status: statusDenied, reason: reasonNetHeaderInvalid, detail: "the access token is not a valid credential"}
	}
	auth.header = bearerPrefix + cred.Token
	auth.echo.add(cred.Token)
	auth.headers = cred.Headers
	auth.hosts = tpl.Hosts
	auth.source, auth.gen = src, cred.Gen
	return nil
}

func oauthDenial(name string, err error) *outcome {
	switch {
	case errors.Is(err, oauth.ErrOwnerRefreshRequired):

		return &outcome{status: statusDenied, reason: reasonAuthDisconnected,
			detail: fmt.Sprintf("auth profile %q reads a borrowed token file whose token is expired or was rejected: the operator refreshes it with its own tool", name)}
	case errors.Is(err, oauth.ErrGrantInvalid), errors.Is(err, os.ErrNotExist):
		return &outcome{status: statusDenied, reason: reasonAuthDisconnected,
			detail: fmt.Sprintf("auth profile %q is not connected (or the authority no longer accepts it): the operator connects it on the Plugins page", name)}
	default:
		return &outcome{status: statusDenied, reason: reasonAuthUnavailable,
			detail: fmt.Sprintf("auth profile %q could not be refreshed just now; try again later", name)}
	}
}

func (p AuthProfile) TokenPath(dir string) (path string, owned bool) {
	path = p.TokenFile
	switch {
	case path == "":
		return "", false
	case oauth.FileName(path):
		if dir == "" {
			return "", false
		}
		path = filepath.Join(dir, path)
	default:
		abs, err := filepath.Abs(path)
		if err != nil {
			return "", false
		}
		path = abs
	}
	path = firewall.ResolvePath(path)
	if dir == "" {
		return path, false
	}
	rel, err := filepath.Rel(firewall.ResolvePath(dir), path)
	owned = err == nil && rel != "." && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	return path, owned
}

func (h *Host) profileSource(name string, profile AuthProfile, params oauth.OAuthParams) (*oauth.Source, *outcome) {
	h.oauthMu.Lock()
	defer h.oauthMu.Unlock()
	rawKey, _ := json.Marshal(struct {
		Profile AuthProfile
		Params  oauth.OAuthParams
	}{profile, params})
	sum := sha256.Sum256(rawKey)
	key := fmt.Sprintf("%s:%x", name, sum)
	if src, ok := h.oauthSrc[key]; ok {
		return src, nil
	}
	tu, err := url.Parse(params.TokenURL)
	if err != nil || tu.Scheme != "https" || tu.Host == "" {
		return nil, &outcome{status: statusDenied, reason: reasonAuthInvalid, detail: "the token endpoint must be an https URL"}
	}
	th, tp := urlHostPort(tu)
	base := h.cfg.Guard
	if base == nil {
		base = firewall.FetchGuard
	}
	guard := func(ctx context.Context, raw string) error {
		if gerr := base(ctx, raw); gerr != nil {
			return gerr
		}
		gu, perr := url.Parse(raw)
		if perr != nil {
			return fmt.Errorf("%w: unparseable token endpoint", firewall.ErrEgressBlocked)
		}
		gh, gp := urlHostPort(gu)
		if !strings.EqualFold(gh, th) || gp != tp {
			return fmt.Errorf("%w: a token refresh dials only its authority (%s:%d)", firewall.ErrEgressBlocked, th, tp)
		}
		return nil
	}
	path, owned := profile.TokenPath(h.cfg.ProfilesDir)
	var src *oauth.Source
	switch {
	case path == "":
		return nil, oauthDenial(name, os.ErrNotExist)
	case owned:
		src, err = oauth.NewProfileSource(path, params, firewall.GuardedClient(30*time.Second, guard, h.cfg.Transport))
	default:
		src, err = oauth.NewConfigured(oauth.KindFilePrefix+path, params)
	}
	if err != nil {
		return nil, oauthDenial(name, err)
	}
	if h.oauthSrc == nil {
		h.oauthSrc = map[string]*oauth.Source{}
	}
	h.oauthSrc[key] = src
	return src, nil
}

func (b *Binding) receiptAuthRefresh(profile string, err error) {
	if b == nil || b.host == nil || b.host.cfg.Store == nil {
		return
	}
	now := time.Now().UTC()
	detail := "refreshed"
	if err != nil {
		switch {
		case errors.Is(err, oauth.ErrGrantInvalid):
			detail = "refresh refused: the authority no longer accepts the grant"
		case errors.Is(err, os.ErrNotExist):
			detail = "not connected: no token file"
		case errors.Is(err, oauth.ErrOwnerRefreshRequired):
			detail = "not refreshed: the token file is borrowed"
		default:
			detail = "refresh failed"
		}
	}
	rec := externalReceipt{
		Success: err == nil, TransportOutcome: true, AuditPersisted: true, HostAuthored: true,
		ID:        fmt.Sprintf("auth-%d-%d", now.UnixNano(), b.host.counter.Add(1)),
		Timestamp: now.Format(time.RFC3339Nano),
		PluginID:  b.pluginID, PluginVersion: b.release.Version, PackageHash: b.release.PackageHash,
		Tier: b.tier.String(), Operation: opAuthRefresh, Target: "auth_profile:" + profile, Detail: detail,
	}

	raw, rerr := json.Marshal(rec)
	if rerr == nil {
		rerr = b.host.cfg.Store.AppendPluginReceipt(rec.ID, b.pluginID, opAuthRefresh, rec.Target, err == nil, raw)
	}
	if rerr != nil {
		logsink.Error("broker.error", "%s: the receipt of the %s token refresh was not recorded: %v", b.pluginID, profile, rerr)
	}
}

const opAuthRefresh = "auth.refresh"
