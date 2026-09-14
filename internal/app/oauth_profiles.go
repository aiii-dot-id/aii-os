package app

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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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
	// .
	// .
	// .
	// .
	defaultProfileRedirect = "http://127.0.0.1:8187/oauth/callback"
	// .
	// .
	deviceSignInMax = 20 * time.Minute
)

var profileNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// .
type pendingDevice struct {
	view   dashboard.DeviceCodeView
	cancel context.CancelFunc
}

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
	params, tpl, err := prof.Contract()
	if err != nil {
		return params, tpl, err
	}
	if params.RedirectURI == "" {
		params.RedirectURI = defaultProfileRedirect
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
// .
// .
func (a *App) SignInProfile(name string) (string, error) {
	prof, err := a.authProfile(name)
	if err != nil {
		return "", err
	}
	params, _, err := a.contractFor(name, prof)
	if err != nil {
		return "", err
	}
	login, err := oauth.NewLogin(params)
	if err != nil {
		return "", err
	}
	key := profileSignInPrefix + name
	ctx, cancel := context.WithTimeout(a.signInBase(), a.signInDeadline())
	p := &pendingSignIn{login: login, kind: key, params: params, cancel: cancel}
	a.signInMu.Lock()
	if a.signIns == nil {
		a.signIns = map[string]*pendingSignIn{}
	}
	if prev, ok := a.signIns[key]; ok && prev.cancel != nil {
		prev.cancel()
	}
	a.signIns[key] = p
	a.signInMu.Unlock()
	go func() {
		code, cerr := login.ServeCallback(ctx)
		if cerr == nil {
			if ferr := a.finishProfileSignIn(name, login, code, login.State()); ferr != nil {
				log.Printf("auth profile %s: callback completion failed: %v", name, ferr)
			}
			return
		}
		<-ctx.Done()
		a.dropSignIn(key, login)
	}()
	return login.URL, nil
}

// .
// .
func (a *App) CompleteProfileSignIn(name, input string) error {
	code, state, err := oauth.ParseAuthorizationInput(input)
	if err != nil {
		return err
	}
	a.signInMu.Lock()
	p, ok := a.signIns[profileSignInPrefix+name]
	a.signInMu.Unlock()
	if !ok {
		return errNoSignIn
	}
	return a.finishProfileSignIn(name, p.login, code, state)
}

// .
// .
func (a *App) finishProfileSignIn(name string, login *oauth.Login, code, state string) error {
	p, err := a.claimSignIn(profileSignInPrefix+name, login, state)
	if err != nil {
		return err
	}
	defer p.cancel()
	prof, err := a.authProfile(name)
	if err != nil {
		return err
	}
	client, err := a.authorityClient(p.params.TokenURL)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(a.signInBase(), 60*time.Second)
	defer cancel()
	tokens, err := login.Exchange(ctx, client, code, state)
	if err != nil {
		return err
	}
	return a.storeProfileTokens(name, prof, tokens)
}

// .
func (a *App) storeProfileTokens(name string, prof broker.AuthProfile, tokens *oauth.Tokens) error {
	if prof.TokenFile == "" {
		return fmt.Errorf("auth profile %q names no token file", name)
	}
	if tokens.Refresh == "" {
		log.Printf("auth profile %s: the authority issued no refresh token; the connection lasts as long as the access token", name)
	}
	if err := oauth.WriteTokenFile(prof.TokenFile, tokens); err != nil {
		return fmt.Errorf("store the profile's tokens: %w", err)
	}
	log.Printf("auth profile %s: connected", name)
	a.profileChanged()
	return nil
}

// .
// .
func (a *App) profileChanged() {
	cfg := a.configSnapshot()
	if a.pluginOpts != nil && a.pluginOpts.Broker != nil {
		a.pluginOpts.Broker.ReplacePolicy(cfg.Plugins.Grants, cfg.Plugins.AuthProfiles)
	}
	if a.dashboard != nil {
		a.dashboard.BroadcastConfig()
	}
}

// .
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
		return nil, fmt.Errorf("%s offers no device-code sign-in; connect from a browser instead", providerLabel(prof))
	}
	if svc := deviceExcluded(tpl, prof.Scopes); svc != "" {
		return nil, fmt.Errorf("%s does not serve %s through its device-code sign-in; connect from a browser instead", providerLabel(prof), svc)
	}
	client, err := a.authorityClient(tpl.DeviceURL)
	if err != nil {
		return nil, err
	}
	sctx, scancel := context.WithTimeout(a.signInBase(), 30*time.Second)
	defer scancel()
	d, err := oauth.StartDevice(sctx, client, tpl.DeviceURL, params)
	if err != nil {
		return nil, err
	}
	tokenClient, err := a.authorityClient(params.TokenURL)
	if err != nil {
		return nil, err
	}
	view := dashboard.DeviceCodeView{UserCode: d.UserCode, VerificationURI: d.VerificationURI, VerificationURIComplete: d.VerificationURIComplete, Expires: d.Expires.UTC().Format(time.RFC3339)}
	ctx, cancel := context.WithTimeout(a.signInBase(), deviceSignInMax)
	a.deviceMu.Lock()
	if a.deviceSignIns == nil {
		a.deviceSignIns = map[string]*pendingDevice{}
	}
	if prev, ok := a.deviceSignIns[name]; ok && prev.cancel != nil {
		prev.cancel()
	}
	a.deviceSignIns[name] = &pendingDevice{view: view, cancel: cancel}
	a.deviceMu.Unlock()
	if a.dashboard != nil {
		a.dashboard.BroadcastConfig()
	}
	go func() {
		defer cancel()
		tokens, perr := oauth.PollDevice(ctx, tokenClient, params, d)
		a.deviceMu.Lock()
		if cur, ok := a.deviceSignIns[name]; ok && cur.cancel != nil && cur.view.UserCode == view.UserCode {
			delete(a.deviceSignIns, name)
		}
		a.deviceMu.Unlock()
		if perr != nil {
			log.Printf("auth profile %s: device sign-in ended: %v", name, perr)
			if a.dashboard != nil {
				a.dashboard.BroadcastConfig()
			}
			return
		}
		if serr := a.storeProfileTokens(name, prof, tokens); serr != nil {
			log.Printf("auth profile %s: device sign-in could not be stored: %v", name, serr)
		}
	}()
	return &view, nil
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
	a.signInMu.Lock()
	if p, ok := a.signIns[profileSignInPrefix+name]; ok {
		p.cancel()
		delete(a.signIns, profileSignInPrefix+name)
	}
	a.signInMu.Unlock()
	a.deviceMu.Lock()
	if d, ok := a.deviceSignIns[name]; ok {
		d.cancel()
		delete(a.deviceSignIns, name)
	}
	a.deviceMu.Unlock()
	if params, tpl, cerr := a.contractFor(name, prof); cerr == nil && tpl.RevokeURL != "" && prof.TokenFile != "" {
		if raw, rerr := os.ReadFile(prof.TokenFile); rerr == nil {
			if tok := refreshTokenOf(raw); tok != "" {
				if client, cerr := a.authorityClient(tpl.RevokeURL); cerr == nil {
					ctx, cancel := context.WithTimeout(a.signInBase(), 20*time.Second)
					if rerr := oauth.Revoke(ctx, client, tpl.RevokeURL, tok, params); rerr != nil {
						log.Printf("auth profile %s: revocation at the authority failed (the file is removed regardless): %v", name, rerr)
					}
					cancel()
				}
			}
		}
	}
	if prof.TokenFile != "" {
		if err := os.Remove(prof.TokenFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove the token file: %w", err)
		}
	}
	log.Printf("auth profile %s: disconnected", name)
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
	provider := strings.TrimSpace(edit.Provider)
	var tpl oauth.Provider
	if provider != "custom" {
		t, ok := oauth.ProviderTemplate(provider)
		if !ok {
			return fmt.Errorf("provider %q is not one this host knows (%s) — choose custom and name the endpoints", provider, strings.Join(oauth.ProviderNames(), ", "))
		}
		tpl = t
	} else if edit.AuthorizeURL == "" || edit.TokenURL == "" {
		return errors.New("a custom provider needs its authorize and token endpoints")
	}
	clientID := strings.TrimSpace(edit.ClientID)
	if clientID == "" {
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
	if len(scopes) == 0 {
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
		TokenFile: existing.TokenFile, ClientSecretFile: existing.ClientSecretFile, ClientSecretEnv: existing.ClientSecretEnv, RedirectURI: existing.RedirectURI}
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
	if _, _, err := prof.Contract(); err != nil {
		return err
	}
	if err := a.commitAuthProfiles(func(m map[string]broker.AuthProfile) { m[name] = prof }); err != nil {
		return err
	}
	log.Printf("auth profile %s: %s (%s, %d scope(s), %d host(s))", name, map[bool]string{true: "updated", false: "created"}[existing.Scheme != ""], provider, len(scopes), len(hosts))
	return nil
}

// .
// .
func (a *App) DeleteAuthProfile(name string) error {
	prof, err := a.authProfile(name)
	if err != nil {
		return err
	}
	if err := a.DisconnectProfile(name); err != nil {
		return err
	}
	if prof.ClientSecretFile != "" {
		if err := os.Remove(prof.ClientSecretFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove the client secret file: %w", err)
		}
	}
	if err := a.commitAuthProfiles(func(m map[string]broker.AuthProfile) { delete(m, name) }); err != nil {
		return err
	}
	log.Printf("auth profile %s: deleted", name)
	return nil
}

// .
// .
// .
func (a *App) commitAuthProfiles(mutate func(map[string]broker.AuthProfile)) error {
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
	a.cfgMu.Lock()
	if !reflectEqualConfig(*a.cfg, orig) {
		a.cfgMu.Unlock()
		return errors.New("config changed while the profile was checked; retry")
	}
	published, perr := saveConfig(&candidate)
	if perr != nil && !published {
		a.cfgMu.Unlock()
		return fmt.Errorf("persist config: %w", perr)
	}
	*a.cfg = candidate
	a.cfgMu.Unlock()
	a.profileChanged()
	if perr != nil {
		return fmt.Errorf("config was published and applied live, but directory durability is unconfirmed: %w", perr)
	}
	return nil
}

// .
func (a *App) authProfileViews(c *Config) []dashboard.AuthProfileView {
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
	a.deviceMu.Lock()
	devices := make(map[string]dashboard.DeviceCodeView, len(a.deviceSignIns))
	for n, d := range a.deviceSignIns {
		devices[n] = d.view
	}
	a.deviceMu.Unlock()
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
		_, tpl, cerr := p.Contract()
		if cerr == nil {
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
		if d, ok := devices[n]; ok {
			dv := d
			v.Device = &dv
		}
		out = append(out, v)
	}
	return out
}

// .
func providerViews() []dashboard.ProviderView {
	var out []dashboard.ProviderView
	for _, name := range oauth.ProviderNames() {
		tpl, _ := oauth.ProviderTemplate(name)
		pv := dashboard.ProviderView{Name: name, Hosts: tpl.Hosts, Device: tpl.DeviceURL != "", DeviceExcludes: tpl.DeviceScopesUnsupported}
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
