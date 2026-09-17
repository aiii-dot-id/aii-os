package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/oauth"
)

// .
// .
// .

var (
	errNoIdentityDir     = errors.New("this identity has no directory to keep a credential in yet")
	errNoSignIn          = errors.New("no sign-in in progress for this provider")
	errSignInSuperseded  = errors.New("this sign-in was superseded by a newer attempt")
	errSignInClaimed     = errors.New("this sign-in was already completed")
	defaultSignInTimeout = 10 * time.Minute
)

// .
// .
func ownedCredentialFile(kind string, configured ...string) (string, error) {
	if len(configured) > 0 && configured[0] != "" {
		name := configured[0]
		if name == "." || name == ".." || strings.ContainsAny(name, "/\\:") || filepath.Base(name) != name {
			return "", errors.New("credential_file must be a filename, not a path")
		}
		return name, nil
	}
	for _, k := range oauth.Kinds() {
		if k == kind {
			return k + ".json", nil
		}
	}
	return "", fmt.Errorf("credential kind %q has no owned-credential file", kind)
}

// .
// .
func (a *App) ownedCredentialPath(kind string, configured ...string) (string, error) {
	if a.cfg == nil || a.cfg.Identity.LedgerPath == "" {
		return "", errNoIdentityDir
	}
	name, err := ownedCredentialFile(kind, configured...)
	if err != nil {
		return "", err
	}
	// .
	// .
	// .
	// .
	// .
	// .
	dir := filepath.Dir(a.cfg.Identity.LedgerPath)
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return filepath.Join(dir, "credentials", name), nil
}

type ownedState int

const (
	ownedAbsent ownedState = iota
	ownedPresent
)

// .
// .
// .
// .
func (a *App) ownedCredential(kind string, configured ...string) (string, ownedState, error) {
	p, err := a.ownedCredentialPath(kind, configured...)
	if errors.Is(err, errNoIdentityDir) {
		return "", ownedAbsent, nil
	}
	if err != nil {
		if len(configured) > 0 && configured[0] != "" {
			return "", ownedAbsent, err
		}
		return "", ownedAbsent, nil
	}
	_, serr := os.Stat(p)
	switch {
	case serr == nil:
		return p, ownedPresent, nil
	case errors.Is(serr, os.ErrNotExist):
		// .
		// .
		// .
		// .
		// .
		if fi, perr := os.Lstat(filepath.Dir(p)); perr == nil && !fi.IsDir() {
			return p, ownedAbsent, fmt.Errorf("cannot inspect the identity's own %s credential at %s: %s is not a directory", kind, p, filepath.Dir(p))
		}
		return p, ownedAbsent, nil
	default:
		return p, ownedAbsent, fmt.Errorf("cannot inspect the identity's own %s credential at %s: %w", kind, p, serr)
	}
}

// .
// .
// .
type pendingSignIn struct {
	profile *broker.AuthProfile
	login   *oauth.Login
	kind    string
	params  oauth.OAuthParams
	dest    string
	ctx     context.Context
	cancel  context.CancelFunc
	claimed bool
	view    dashboard.SignInView
}

func (a *App) signInBase() context.Context {
	if a.bgCtx != nil {
		return a.bgCtx
	}
	return context.Background()
}

func (a *App) signInDeadline() time.Duration {
	if a.signInTimeout > 0 {
		return a.signInTimeout
	}
	return defaultSignInTimeout
}

// .
// .
func (a *App) SignInProvider(name string) (string, error) {
	e, err := a.providerEntryNamed(name)
	if err != nil {
		return "", err
	}
	if e.Credential == "" {
		return "", fmt.Errorf("provider %q uses an API key", name)
	}
	if e.CredentialOptions["file"] != "" {
		return "", errors.New("this provider is pinned to a borrowed credential file; remove its file override before native sign-in")
	}
	dest, err := a.ownedCredentialPath(e.Credential, e.signIn.CredentialFile)
	if err != nil {
		return "", err
	}
	params := e.signIn.Params()
	params.RequiredScope = e.CredentialOptions["required_scope"]
	return a.startSignIn("provider:"+name, e.Credential, dest, e.signIn, params, nil)
}

func (a *App) startSignIn(key, kind, dest string, contract oauth.Provider, params oauth.OAuthParams, profile *broker.AuthProfile) (string, error) {
	if params.TokenURL == "" || params.ClientID == "" {
		return "", errors.New("no sign-in configured for this provider")
	}
	dest, err := filepath.Abs(dest)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(a.signInBase(), a.signInDeadline())
	p := &pendingSignIn{profile: profile, kind: kind, params: params, dest: dest, ctx: ctx, cancel: cancel, view: dashboard.SignInView{Status: "pending"}}
	switch contract.SignIn {
	case "openai_device", "device":
	case "", "callback", "manual":
		if err := validateSignInRedirect(contract.SignIn, params.RedirectURI); err != nil {
			cancel()
			return "", err
		}
		login, err := oauth.NewLogin(params)
		if err != nil {
			cancel()
			return "", err
		}
		p.login = login
		p.view.URL = login.URL
		p.view.Manual = contract.SignIn == "manual"
	default:
		cancel()
		return "", fmt.Errorf("unknown sign_in method %q", contract.SignIn)
	}
	a.signInMu.Lock()
	if profile != nil {
		current, err := a.authProfile(strings.TrimPrefix(key, profileSignInPrefix))
		if err != nil || !reflect.DeepEqual(current, *profile) {
			a.signInMu.Unlock()
			cancel()
			return "", errSignInSuperseded
		}
	}
	if a.signIns == nil {
		a.signIns = map[string]*pendingSignIn{}
	}
	// .
	for previousKey, prev := range a.signIns {
		if previousKey == key || prev.dest == dest {
			prev.view.Status = "cancelled"
			a.settleSignIn(previousKey, prev)
		}
	}
	a.signIns[key] = p
	a.signInMu.Unlock()
	go func() {
		<-ctx.Done()
		a.signInMu.Lock()
		if a.signIns[key] == p && p.view.Status == "pending" {
			p.view.Status = "expired"
			a.settleSignIn(key, p)
		}
		a.signInMu.Unlock()
		a.signInChanged()
	}()
	if p.login != nil {
		a.signInChanged()
		return p.login.URL, nil
	}
	client, err := a.authorityClient(contract.DeviceURL)
	if err != nil {
		a.endSignIn(key, p, err)
		return "", err
	}
	var d *oauth.DeviceAuthorization
	if contract.SignIn == "openai_device" {
		contract.ClientID = params.ClientID
		contract.TokenURL = params.TokenURL
		d, err = oauth.StartOpenAIDevice(ctx, client, contract)
	} else {
		d, err = oauth.StartDevice(ctx, client, contract.DeviceURL, params)
	}
	if err != nil {
		a.endSignIn(key, p, err)
		return "", err
	}
	tokenClient, err := a.authorityClient(params.TokenURL)
	if err != nil {
		a.endSignIn(key, p, err)
		return "", err
	}
	pollClient := tokenClient
	if contract.SignIn == "openai_device" {
		pollClient, err = a.authorityClient(contract.DevicePollURL)
		if err != nil {
			a.endSignIn(key, p, err)
			return "", err
		}
	}
	view := dashboard.DeviceCodeView{UserCode: d.UserCode, VerificationURI: d.VerificationURI, VerificationURIComplete: d.VerificationURIComplete, Expires: d.Expires.UTC().Format(time.RFC3339)}
	a.signInMu.Lock()
	if a.signIns[key] != p || ctx.Err() != nil {
		a.signInMu.Unlock()
		return "", errSignInSuperseded
	}
	p.view.Device = &view
	p.view.URL = d.VerificationURIComplete
	if p.view.URL == "" {
		p.view.URL = d.VerificationURI
	}
	resultURL := p.view.URL
	a.signInMu.Unlock()
	a.signInChanged()
	go func() {
		var tokens *oauth.Tokens
		var err error
		if contract.SignIn == "openai_device" {
			tokens, err = oauth.PollOpenAIDevice(ctx, pollClient, tokenClient, contract, params, d)
		} else {
			tokens, err = oauth.PollDevice(ctx, tokenClient, params, d)
		}
		if err == nil {
			err = a.publishSignIn(key, p, tokens)
		}
		a.endSignIn(key, p, err)
	}()
	return resultURL, nil
}

func (a *App) signInChanged() {
	if a.dashboard != nil {
		a.dashboard.BroadcastConfig()
		a.dashboard.BroadcastProviders()
	}
}
func (a *App) signInView(key string) *dashboard.SignInView {
	a.signInMu.Lock()
	defer a.signInMu.Unlock()
	if p := a.signIns[key]; p != nil {
		v := p.view
		if v.Device != nil {
			d := *v.Device
			v.Device = &d
		}
		return &v
	}
	return nil
}
func (a *App) endSignIn(key string, p *pendingSignIn, err error) {
	a.signInMu.Lock()
	if a.signIns[key] == p {
		if p.view.Status == "pending" {
			if err == nil {
				p.view.Status = "connected"
			} else if p.ctx.Err() != nil {
				p.view.Status = "expired"
			} else {
				p.view.Status = "failed"
			}
		}
		a.settleSignIn(key, p)
	}
	a.signInMu.Unlock()
	if err == nil {
		a.credMu.Lock()
		a.credSrc = nil
		a.credMu.Unlock()
		if strings.HasPrefix(key, profileSignInPrefix) {
			a.profileChanged()
		} else {
			a.provMu.Lock()
			delete(a.provStatus, strings.TrimPrefix(key, "provider:"))
			a.provMu.Unlock()
		}
	}
	a.signInChanged()
}
func (a *App) publishSignIn(key string, p *pendingSignIn, tokens *oauth.Tokens) error {
	if required := p.params.RequiredScope; required != "" {
		granted := false
		for _, scope := range strings.Fields(tokens.Scope) {
			granted = granted || scope == required
		}
		if !granted {
			return fmt.Errorf("sign-in did not grant required scope %q; the existing credential was kept", required)
		}
	}
	a.signInMu.Lock()
	defer a.signInMu.Unlock()
	if a.signIns[key] != p || p.ctx.Err() != nil {
		return errSignInSuperseded
	}
	// .
	// .
	if p.profile != nil {
		a.cfgMu.RLock()
		defer a.cfgMu.RUnlock()
		current, ok := a.cfg.Plugins.AuthProfiles[strings.TrimPrefix(key, profileSignInPrefix)]
		if !ok || !reflect.DeepEqual(current, *p.profile) {
			return errSignInSuperseded
		}
	}
	// .
	// .
	if err := oauth.WriteTokenFile(p.dest, tokens); err != nil {
		return err
	}
	p.view.Status = "connected"
	return nil
}

// .
// .
func (a *App) OAuthCallback(code, state, authorityError string) error {
	if state == "" {
		return errNoSignIn
	}
	a.signInMu.Lock()
	var key string
	var p *pendingSignIn
	for k, v := range a.signIns {
		if v.login != nil && v.login.State() == state {
			key, p = k, v
			break
		}
	}
	a.signInMu.Unlock()
	if p == nil {
		return errNoSignIn
	}
	if authorityError != "" {
		a.endSignIn(key, p, oauth.ErrDeviceDenied)
		return oauth.ErrDeviceDenied
	}
	return a.finishSignIn(key, p.login, code, state)
}

// .
// .
func (a *App) CompleteSignIn(name, input string) error {
	return a.completeSignIn("provider:"+name, input)
}
func (a *App) completeSignIn(name, input string) error {
	code, state, err := oauth.ParseAuthorizationInput(input)
	if err != nil {
		return err
	}
	a.signInMu.Lock()
	p, ok := a.signIns[name]
	a.signInMu.Unlock()
	if !ok {
		return errNoSignIn
	}
	return a.finishSignIn(name, p.login, code, state)
}

// .
// .
// .
// .
func (a *App) finishSignIn(name string, login *oauth.Login, code, state string) error {
	p, err := a.claimSignIn(name, login, state)
	if err != nil {
		return err
	}
	client, err := a.authorityClient(p.params.TokenURL)
	if err == nil {
		var tokens *oauth.Tokens
		tokens, err = login.Exchange(p.ctx, client, code, state)
		if err == nil {
			err = a.publishSignIn(name, p, tokens)
		}
	}
	a.endSignIn(name, p, err)
	return err
}

// .
// .
// .
// .
// .
func (a *App) claimSignIn(key string, login *oauth.Login, state string) (*pendingSignIn, error) {
	if login == nil || state == "" || state != login.State() {
		return nil, errors.New("state mismatch — this response is not for the current sign-in")
	}
	a.signInMu.Lock()
	defer a.signInMu.Unlock()
	p, ok := a.signIns[key]
	if !ok || p.login != login {
		return nil, errSignInSuperseded
	}
	if p.ctx.Err() != nil {
		return nil, errSignInSuperseded
	}
	if p.claimed {
		return nil, errSignInClaimed
	}
	p.claimed = true
	return p, nil
}

// .
func (a *App) providerEntryNamed(name string) (providerEntry, error) {
	reg, err := a.loadProviders()
	if err != nil {
		return providerEntry{}, err
	}
	for _, e := range reg.Providers {
		if e.Name == name {
			return e, nil
		}
	}
	return providerEntry{}, fmt.Errorf("provider %q not found", name)
}

// .
// .
func canSignIn(e providerEntry) bool {
	if e.Credential == "" || e.CredentialOptions["file"] != "" {
		return false
	}
	if _, err := ownedCredentialFile(e.Credential, e.signIn.CredentialFile); err != nil {
		return false
	}
	p := e.signIn
	if p.ClientID == "" || p.TokenURL == "" {
		return false
	}
	switch p.SignIn {
	case "openai_device":
		return p.DeviceURL != "" && p.DevicePollURL != "" && p.VerificationURI != "" && p.DeviceRedirectURI != "" && p.DeviceExpiresSeconds > 0
	case "device":
		return p.DeviceURL != ""
	case "", "callback", "manual":
		return validateSignInRedirect(p.SignIn, p.RedirectURI) == nil && p.AuthorizeURL != ""
	default:
		return false
	}
}

// .
// .
func validateSignInRedirect(method, raw string) error {
	u, err := url.Parse(raw)
	if err == nil && u.Host != "" && u.User == nil && u.Fragment == "" {
		if method == "manual" && u.Scheme == "https" {
			return nil
		}
		if (method == "" || method == "callback") && (u.Scheme == "http" || u.Scheme == "https") && u.Path == "/oauth/callback" && u.RawQuery == "" {
			return nil
		}
	}
	if method == "manual" {
		return errors.New("configure the authority's registered HTTPS redirect_uri for manual sign-in")
	}
	return errors.New("configure the registered dashboard redirect_uri ending in /oauth/callback")
}

// .
func (a *App) CancelSignIn(name string) error { return a.cancelSignIn("provider:" + name) }
func (a *App) cancelSignIn(name string) error {
	a.signInMu.Lock()
	if p := a.signIns[name]; p != nil && p.view.Status == "pending" {
		p.cancel()
		p.view.Status = "cancelled"
		p.view.URL = ""
		p.view.Device = nil
		a.settleSignIn(name, p)
	}
	a.signInMu.Unlock()
	a.signInChanged()
	return nil
}
func (a *App) CancelProfileSignIn(name string) error {
	return a.cancelSignIn(profileSignInPrefix + name)
}

// .
// .
// .
func (a *App) settleSignIn(key string, p *pendingSignIn) {
	p.cancel()
	v := p.view
	v.URL = ""
	v.Device = nil
	v.Manual = false
	a.signIns[key] = &pendingSignIn{view: v, cancel: p.cancel, ctx: p.ctx}
}
