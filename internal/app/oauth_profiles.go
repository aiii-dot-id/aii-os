package app

// .
// .
// .

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/oauth"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

const (
	// .
	// .
	profileSignInPrefix = "profile:"
)

var profileNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// .
// .
func (a *App) credentialsDir() (string, error) {
	if a.cfg == nil || a.cfg.Identity.LedgerPath == "" {
		return "", errNoIdentityDir
	}
	dir := filepath.Dir(a.cfg.Identity.LedgerPath)
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return filepath.Join(dir, "credentials", "profiles"), nil
}

func (a *App) authProfile(name string) (broker.AuthProfile, error) {
	prof, ok := a.configSnapshot().Plugins.AuthProfiles[name]
	if !ok {
		return broker.AuthProfile{}, fmt.Errorf("no auth profile %q", name)
	}
	if prof.Scheme != broker.SchemeOAuth2 {
		return broker.AuthProfile{}, fmt.Errorf("auth profile %q is not an oauth2 profile", name)
	}
	return prof, nil
}

// .
// .
func (a *App) authorityClient(endpoint string) (*http.Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("an authority endpoint must be an https URL, got %q", endpoint)
	}
	want := strings.ToLower(u.Host)
	base := a.oauthGuard
	if base == nil {
		base = tools.FetchGuard
	}
	guard := func(ctx context.Context, raw string) error {
		if gerr := base(ctx, raw); gerr != nil {
			return gerr
		}
		gu, perr := url.Parse(raw)
		if perr != nil || !strings.EqualFold(gu.Host, want) {
			return fmt.Errorf("%w: a consent dials only its authority (%s)", tools.ErrEgressBlocked, want)
		}
		return nil
	}
	return tools.GuardedClient(30*time.Second, guard, a.oauthTransport), nil
}

// .
// .
func (a *App) contractFor(name string, prof broker.AuthProfile) (oauth.OAuthParams, oauth.Provider, error) {
	catalog, err := a.oauthContracts()
	if err != nil {
		return oauth.OAuthParams{}, oauth.Provider{}, err
	}
	params, tpl, err := prof.Contract(catalog)
	if err != nil {
		return params, tpl, err
	}
	if strings.TrimSpace(params.Scope) == "" {
		return params, tpl, fmt.Errorf("auth profile %q names no scopes; choose what the consent is for", name)
	}
	switch {
	case prof.ClientSecretEnv != "":
		params.ClientSecret = os.Getenv(prof.ClientSecretEnv)
	case prof.ClientSecretFile != "":
		raw, rerr := os.ReadFile(prof.ClientSecretFile)
		if rerr != nil {
			return params, tpl, fmt.Errorf("auth profile %q: the client secret file cannot be read: %w", name, rerr)
		}
		params.ClientSecret = strings.TrimSpace(string(raw))
	}
	return params, tpl, nil
}

// .
// .
func (a *App) SignInProfile(name string) (string, error) {
	prof, err := a.authProfile(name)
	if err != nil {
		return "", err
	}
	params, tpl, err := a.contractFor(name, prof)
	if err != nil {
		return "", err
	}
	if prof.TokenFile == "" {
		return "", errors.New("profile has no token file")
	}
	return a.startSignIn(profileSignInPrefix+name, profileSignInPrefix+name, prof.TokenFile, tpl, params, &prof)
}

func (a *App) CompleteProfileSignIn(name, input string) error {
	return a.completeSignIn(profileSignInPrefix+name, input)
}

// .
// .
func (a *App) profileChanged() {
	cfg := a.configSnapshot()
	if a.pluginOpts != nil && a.pluginOpts.Broker != nil {
		a.replacePolicy(cfg)
	}
	if a.dashboard != nil {
		a.dashboard.BroadcastConfig()
	}
}

// .
func (a *App) DeviceSignInProfile(name string) (*dashboard.DeviceCodeView, error) {
	prof, err := a.authProfile(name)
	if err != nil {
		return nil, err
	}
	params, tpl, err := a.contractFor(name, prof)
	if err != nil {
		return nil, err
	}
	if tpl.DeviceURL == "" {
		return nil, errors.New("this authority offers no device-code sign-in")
	}
	if svc := deviceExcluded(tpl, prof.Scopes); svc != "" {
		return nil, fmt.Errorf("%s does not serve %s through device sign-in", providerLabel(prof), svc)
	}
	if tpl.SignIn != "openai_device" {
		tpl.SignIn = "device"
	}
	key := profileSignInPrefix + name
	if _, err := a.startSignIn(key, key, prof.TokenFile, tpl, params, &prof); err != nil {
		return nil, err
	}
	v := a.signInView(key)
	if v == nil {
		return nil, errNoSignIn
	}
	return v.Device, nil
}

func providerLabel(prof broker.AuthProfile) string {
	if prof.Provider == "" || prof.Provider == "custom" {
		return "this authority"
	}
	return prof.Provider
}

// .
// .
func deviceExcluded(tpl oauth.Provider, scopes []string) string {
	for _, svc := range tpl.DeviceScopesUnsupported {
		set := tpl.Scopes[svc]
		for _, s := range scopes {
			for _, x := range append(append([]string(nil), set.Read...), set.Modify...) {
				if s == x {
					return svc
				}
			}
		}
	}
	return ""
}

// .
// .
// .
func (a *App) DisconnectProfile(name string) error {
	prof, err := a.authProfile(name)
	if err != nil {
		return err
	}
	dest := ""
	if prof.TokenFile != "" {
		dest, _ = filepath.Abs(prof.TokenFile)
	}
	a.signInMu.Lock()
	for key, p := range a.signIns {
		if key == profileSignInPrefix+name || (dest != "" && p.dest == dest) {
			p.cancel()
			delete(a.signIns, key)
		}
	}
	// .
	var revokeRaw []byte
	if prof.TokenFile != "" {
		revokeRaw, _ = os.ReadFile(prof.TokenFile)
	}
	var removeErr error
	if prof.TokenFile != "" {
		removeErr = oauth.RemoveTokenFile(prof.TokenFile)
	}
	a.signInMu.Unlock()
	if removeErr != nil {
		return removeErr
	}
	if params, tpl, cerr := a.contractFor(name, prof); cerr == nil && tpl.RevokeURL != "" && prof.TokenFile != "" {
		if raw := revokeRaw; len(raw) > 0 {
			if tok := refreshTokenOf(raw); tok != "" {
				if client, cerr := a.authorityClient(tpl.RevokeURL); cerr == nil {
					ctx, cancel := context.WithTimeout(a.signInBase(), 20*time.Second)
					if rerr := oauth.Revoke(ctx, client, tpl.RevokeURL, tok, params); rerr != nil {
						logsink.Warn("oauth.error", "%s: revocation at the authority failed (the file is removed regardless): %v", name, rerr)
					}
					cancel()
				}
			}
		}
	}
	logsink.Info("oauth.end", "%s: disconnected", name)
	a.profileChanged()
	return nil
}

// .
// .
func refreshTokenOf(raw []byte) string {
	var f struct {
		Refresh string `json:"refresh_token"`
		Access  string `json:"access_token"`
	}
	if err := jsonUnmarshal(raw, &f); err != nil {
		return ""
	}
	if f.Refresh != "" {
		return f.Refresh
	}
	return f.Access
}

// .
// .
// .
// .
func (a *App) SetAuthProfile(edit dashboard.AuthProfileEdit) error {
	name := strings.TrimSpace(edit.Name)
	if !profileNameRe.MatchString(name) {
		return fmt.Errorf("a profile name is a token: lowercase letters, digits, dots, dashes, underscores (got %q)", edit.Name)
	}
	dir, err := a.credentialsDir()
	if err != nil {
		return err
	}
	catalog, err := a.oauthContracts()
	if err != nil {
		return err
	}
	provider := strings.TrimSpace(edit.Provider)
	var tpl oauth.Provider
	if provider != "custom" {
		t, ok := oauth.ProviderTemplate(provider, catalog)
		if !ok {
			return fmt.Errorf("provider %q is not one this host knows (%s) — choose custom and name the endpoints", provider, strings.Join(oauth.ProviderNames(catalog), ", "))
		}
		tpl = t
	} else if edit.AuthorizeURL == "" || edit.TokenURL == "" {
		return errors.New("a custom provider needs its authorize and token endpoints")
	}
	clientID := strings.TrimSpace(edit.ClientID)
	if clientID == "" && tpl.ClientID == "" {
		return errors.New("the client id is the registration the authority knows you by; it is required")
	}
	var scopes []string
	svcNames := make([]string, 0, len(edit.Services))
	for svc := range edit.Services {
		svcNames = append(svcNames, svc)
	}
	sort.Strings(svcNames)
	for _, svc := range svcNames {
		set, ok := tpl.Scopes[svc]
		if !ok {
			return fmt.Errorf("%s has no service %q", provider, svc)
		}
		switch edit.Services[svc] {
		case "read":
			scopes = append(scopes, set.Read...)
		case "modify":
			scopes = append(scopes, set.Read...)
			scopes = append(scopes, set.Modify...)
		default:
			return fmt.Errorf("service %q: want read or modify, got %q", svc, edit.Services[svc])
		}
	}
	for _, s := range edit.Scopes {
		if s = strings.TrimSpace(s); s != "" {
			scopes = append(scopes, s)
		}
	}
	scopes = dedupe(scopes)
	if len(scopes) == 0 && len(tpl.BaseScopes) == 0 {
		return errors.New("choose at least one service, or name a scope")
	}
	// .
	// .
	// .
	// .
	hosts := dedupe(trimAll(edit.Hosts))
	for _, h := range hosts {
		if _, _, err := splitHostPort(h); err != nil {
			return fmt.Errorf("host %q: want host:port", h)
		}
	}
	if provider != "custom" && sameSet(hosts, tpl.Hosts) {
		hosts = nil
	}
	if len(hosts) == 0 && len(tpl.Hosts) == 0 {
		return errors.New("name at least one host the access token may ride to")
	}
	existing := a.configSnapshot().Plugins.AuthProfiles[name]
	if existing.Scheme != "" && existing.Scheme != broker.SchemeOAuth2 {
		return fmt.Errorf("auth profile %q is a %s profile, edited in the config file", name, existing.Scheme)
	}
	prof := broker.AuthProfile{Scheme: broker.SchemeOAuth2, Provider: provider, ClientID: clientID, Scopes: scopes, Hosts: hosts,
		TokenFile: existing.TokenFile, ClientSecretFile: existing.ClientSecretFile, ClientSecretEnv: existing.ClientSecretEnv, RedirectURI: strings.TrimSpace(edit.RedirectURI)}
	if prof.RedirectURI == "" {
		prof.RedirectURI = existing.RedirectURI
	}
	if prof.RedirectURI == tpl.RedirectURI {
		prof.RedirectURI = ""
	}
	if prof.TokenFile == "" {
		prof.TokenFile = filepath.Join(dir, name+".json")
	}
	if provider == "custom" {
		prof.AuthorizeURL, prof.TokenURL, prof.DeviceURL, prof.RevokeURL = strings.TrimSpace(edit.AuthorizeURL), strings.TrimSpace(edit.TokenURL), strings.TrimSpace(edit.DeviceURL), strings.TrimSpace(edit.RevokeURL)
		for _, u := range []string{prof.AuthorizeURL, prof.TokenURL, prof.DeviceURL, prof.RevokeURL} {
			if u == "" {
				continue
			}
			if pu, perr := url.Parse(u); perr != nil || pu.Scheme != "https" || pu.Host == "" {
				return fmt.Errorf("endpoint %q must be an https URL", u)
			}
		}
	}
	if secret := strings.TrimSpace(edit.ClientSecret); secret != "" {
		path := filepath.Join(dir, name+".client")
		if err := oauth.WritePrivateFile(path, []byte(secret+"\n")); err != nil {
			return fmt.Errorf("store the client secret: %w", err)
		}
		prof.ClientSecretFile, prof.ClientSecretEnv = path, ""
	}
	if _, _, err := prof.Contract(catalog); err != nil {
		return err
	}
	if err := a.commitAuthProfiles(name, func(m map[string]broker.AuthProfile) { m[name] = prof }); err != nil {
		return err
	}
	logsink.Info("oauth.decision", "%s: %s (%s, %d scope(s), %d host(s))", name, map[bool]string{true: "updated", false: "created"}[existing.Scheme != ""], provider, len(scopes), len(hosts))
	return nil
}

// .
// .
func (a *App) DeleteAuthProfile(name string) error {
	if err := a.DisconnectProfile(name); err != nil {
		return err
	}
	// .
	// .
	// .
	if err := a.commitAuthProfiles(name, func(m map[string]broker.AuthProfile) { delete(m, name) }); err != nil {
		return err
	}
	logsink.Info("oauth.end", "%s: deleted", name)
	return nil
}

// .
// .
// .
// .
func (a *App) commitAuthProfiles(editedName string, mutate func(map[string]broker.AuthProfile)) error {
	orig := a.configSnapshot()
	candidate := orig
	next := make(map[string]broker.AuthProfile, len(orig.Plugins.AuthProfiles)+1)
	for k, v := range orig.Plugins.AuthProfiles {
		next[k] = v
	}
	mutate(next)
	if len(next) == 0 {
		next = nil
	}
	candidate.Plugins.AuthProfiles = next
	published := false
	err := func() error {
		// .
		// .
		a.signInMu.Lock()
		defer a.signInMu.Unlock()
		a.cfgMu.Lock()
		defer a.cfgMu.Unlock()
		if !reflectEqualConfig(*a.cfg, orig) {
			return errors.New("config changed while the profile was checked; retry")
		}
		for name, prof := range orig.Plugins.AuthProfiles {
			nextProf, exists := next[name]
			if name != editedName && exists && reflect.DeepEqual(prof, nextProf) {
				continue
			}
			dest := ""
			if prof.TokenFile != "" {
				dest, _ = filepath.Abs(prof.TokenFile)
			}
			for key, p := range a.signIns {
				if key == profileSignInPrefix+name || (dest != "" && p.dest == dest) {
					p.cancel()
					delete(a.signIns, key)
				}
			}
			if !exists {
				if prof.TokenFile != "" {
					if err := oauth.RemoveTokenFile(prof.TokenFile); err != nil {
						return err
					}
				}
				if prof.ClientSecretFile != "" {
					if err := os.Remove(prof.ClientSecretFile); err != nil && !errors.Is(err, os.ErrNotExist) {
						return fmt.Errorf("remove the client secret file: %w", err)
					}
				}
			}
		}
		var perr error
		published, perr = saveConfig(&candidate)
		if perr != nil && !published {
			return fmt.Errorf("persist config: %w", perr)
		}
		*a.cfg = candidate
		if perr != nil {
			return fmt.Errorf("config was published and applied live, but directory durability is unconfirmed: %w", perr)
		}
		return nil
	}()
	if published {
		a.profileChanged()
	}
	return err
}

// .
func (a *App) authProfileViews(c *Config) []dashboard.AuthProfileView {
	catalog, _ := a.oauthContracts()
	if len(c.Plugins.AuthProfiles) == 0 {
		return nil
	}
	handlesOf := map[string][]string{}
	for pid, g := range c.Plugins.Grants {
		for _, h := range g.CredentialHandles {
			handlesOf[h] = append(handlesOf[h], pid)
		}
	}
	names := make([]string, 0, len(c.Plugins.AuthProfiles))
	for n := range c.Plugins.AuthProfiles {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]dashboard.AuthProfileView, 0, len(names))
	for _, n := range names {
		p := c.Plugins.AuthProfiles[n]
		v := dashboard.AuthProfileView{Name: n, Scheme: p.Scheme, Handles: handlesOf[n]}
		sort.Strings(v.Handles)
		if v.Scheme == "" {
			v.Scheme = "bearer"
		}
		if p.Scheme != broker.SchemeOAuth2 {
			v.State, v.Host, v.Port = "static", p.Host, p.Port
			switch {
			case p.SecretFile != "":
				v.SecretSource = "file"
			case p.SecretEnv != "":
				v.SecretSource = "env"
			}
			out = append(out, v)
			continue
		}
		v.Provider, v.ClientID, v.Scopes = p.Provider, p.ClientID, append([]string(nil), p.Scopes...)
		v.HasClientSecret = p.ClientSecretFile != "" || p.ClientSecretEnv != ""
		v.Custom = p.Provider == "" || p.Provider == "custom"
		params, tpl, cerr := p.Contract(catalog)
		if cerr == nil {
			if v.ClientID == "" {
				v.ClientID = params.ClientID
			}
			v.Hosts = tpl.Hosts
			v.CanDevice = tpl.DeviceURL != "" && deviceExcluded(tpl, p.Scopes) == ""
		} else {
			v.Hosts = append([]string(nil), p.Hosts...)
		}
		st, serr := oauth.ReadProfileState(p.TokenFile)
		switch {
		case serr != nil:
			v.State = "disconnected"
		case !st.Connected:
			v.State = "disconnected"
		case !st.Refreshable && !st.Expires.IsZero() && time.Now().After(st.Expires):
			v.State = "expired"
		default:
			v.State = "connected"
		}
		if !st.Expires.IsZero() {
			v.ExpiresAt = st.Expires.UTC().Format(time.RFC3339)
		}
		v.SignIn = a.signInView(profileSignInPrefix + n)
		if v.SignIn != nil {
			v.Device = v.SignIn.Device
		}
		v.RedirectURI = p.RedirectURI
		out = append(out, v)
	}
	return out
}

// .
func (a *App) providerViews() []dashboard.ProviderView {
	catalog, err := a.oauthContracts()
	if err != nil {
		return nil
	}
	var out []dashboard.ProviderView
	for _, name := range oauth.ProviderNames(catalog) {
		tpl, _ := oauth.ProviderTemplate(name, catalog)
		pv := dashboard.ProviderView{Name: name, SignIn: tpl.SignIn, RedirectURI: tpl.RedirectURI, Hosts: tpl.Hosts, Device: tpl.DeviceURL != "", DeviceExcludes: tpl.DeviceScopesUnsupported}
		svcs := make([]string, 0, len(tpl.Scopes))
		for s := range tpl.Scopes {
			svcs = append(svcs, s)
		}
		sort.Strings(svcs)
		for _, s := range svcs {
			pv.Services = append(pv.Services, dashboard.ServiceView{Name: s, Read: tpl.Scopes[s].Read, Modify: tpl.Scopes[s].Modify})
		}
		out = append(out, pv)
	}
	return out
}

// .
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, x := range a {
		seen[strings.ToLower(x)] = true
	}
	for _, y := range b {
		if !seen[strings.ToLower(y)] {
			return false
		}
	}
	return true
}

func dedupe(in []string) []string {
	var out []string
	for _, s := range in {
		if s == "" {
			continue
		}
		dup := false
		for _, o := range out {
			if o == s {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, s)
		}
	}
	return out
}

func trimAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, strings.TrimSpace(s))
	}
	return out
}

func jsonUnmarshal(raw []byte, v interface{}) error { return json.Unmarshal(raw, v) }

func reflectEqualConfig(a, b Config) bool { return reflect.DeepEqual(a, b) }

func splitHostPort(hp string) (string, string, error) {
	h, p, err := net.SplitHostPort(hp)
	if err != nil {
		return "", "", err
	}
	if h == "" || p == "" {
		return "", "", fmt.Errorf("want host:port")
	}
	return h, p, nil
}
