package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/oauth"
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
// .
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
// .
func ownedCredentialFile(kind string) (string, error) {
	for _, k := range oauth.Kinds() {
		if k == kind {
			return k + ".json", nil
		}
	}
	return "", fmt.Errorf("credential kind %q has no owned-credential file", kind)
}

// .
// .
func (a *App) ownedCredentialPath(kind string) (string, error) {
	if a.cfg == nil || a.cfg.Identity.LedgerPath == "" {
		return "", errNoIdentityDir
	}
	name, err := ownedCredentialFile(kind)
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
func (a *App) ownedCredential(kind string) (string, ownedState, error) {
	p, err := a.ownedCredentialPath(kind)
	if err != nil {
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
	login   *oauth.Login
	kind    string
	params  oauth.OAuthParams
	cancel  context.CancelFunc
	claimed bool
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
// .
// .
// .
// .
func (a *App) SignInProvider(name string) (string, error) {
	e, err := a.providerEntryNamed(name)
	if err != nil {
		return "", err
	}
	if e.Credential == "" {
		return "", fmt.Errorf("provider %q uses an API key, not a sign-in", name)
	}
	if _, err := ownedCredentialFile(e.Credential); err != nil {
		return "", err
	}
	if _, err := a.ownedCredentialPath(e.Credential); err != nil {
		return "", err
	}
	params, err := oauth.ParamsFromOptions(e.CredentialOptions)
	if err != nil {
		return "", fmt.Errorf("provider %q sign-in contract: %w", name, err)
	}
	if !params.Complete() {
		return "", fmt.Errorf("provider %q has no native sign-in configured (oauth_* options)", name)
	}
	login, err := oauth.NewLogin(params)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(a.signInBase(), a.signInDeadline())
	p := &pendingSignIn{login: login, kind: e.Credential, params: params, cancel: cancel}
	a.signInMu.Lock()
	if a.signIns == nil {
		a.signIns = map[string]*pendingSignIn{}
	}
	if prev, ok := a.signIns[name]; ok && prev.cancel != nil {
		prev.cancel()
	}
	a.signIns[name] = p
	a.signInMu.Unlock()
	go func() {
		code, cerr := login.ServeCallback(ctx)
		if cerr == nil {
			if ferr := a.finishSignIn(name, login, code, login.State()); ferr != nil {
				log.Printf("sign-in %s: callback completion failed: %v", name, ferr)
			}
			return
		}
		// .
		// .
		// .
		// .
		// .
		<-ctx.Done()
		a.dropSignIn(name, login)
	}()
	return login.URL, nil
}

// .
// .
func (a *App) CompleteSignIn(name, input string) error {
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
func (a *App) dropSignIn(name string, login *oauth.Login) {
	a.signInMu.Lock()
	defer a.signInMu.Unlock()
	if p, ok := a.signIns[name]; ok && p.login == login {
		p.cancel()
		delete(a.signIns, name)
	}
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
	defer p.cancel()

	ctx, cancel := context.WithTimeout(a.signInBase(), 60*time.Second)
	defer cancel()
	tokens, err := login.Complete(ctx, code, state)
	if err != nil {
		return err
	}
	dest, err := a.ownedCredentialPath(p.kind)
	if err != nil {
		return err
	}
	if err := oauth.WriteAuthFile(dest, tokens); err != nil {
		return fmt.Errorf("store the signed-in credential: %w", err)
	}
	a.credMu.Lock()
	for k := range a.credSrc {
		if k == p.kind || strings.HasPrefix(k, p.kind+"\x00") {
			delete(a.credSrc, k)
		}
	}
	a.credMu.Unlock()
	if a.dashboard != nil {
		a.dashboard.BroadcastProviders()
	}
	return nil
}

// .
// .
// .
// .
// .
func (a *App) claimSignIn(key string, login *oauth.Login, state string) (*pendingSignIn, error) {
	if state == "" || state != login.State() {
		return nil, errors.New("state mismatch — this response is not for the current sign-in")
	}
	a.signInMu.Lock()
	defer a.signInMu.Unlock()
	p, ok := a.signIns[key]
	if !ok || p.login != login {
		return nil, errSignInSuperseded
	}
	if p.claimed {
		return nil, errSignInClaimed
	}
	p.claimed = true
	delete(a.signIns, key)
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
	if e.Credential == "" {
		return false
	}
	if _, err := ownedCredentialFile(e.Credential); err != nil {
		return false
	}
	p, err := oauth.ParamsFromOptions(e.CredentialOptions)
	return err == nil && p.Complete()
}
