package app

import (
	"fmt"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// THE RUNNING PROVIDER NEVER WAITS ON A SIGN-IN OR ON A CREDENTIAL'S FIRST
// OPEN. The providers' state keeps three locks because their places in the
// order differ (providerState): a sign-in's custody is held across
// token-file writes and the config persist, and a credential source's
// across its first open, on darwin a keychain dialog of up to 10 s. The
// running turn reads currentProvider, activeRunTarget reads the budget
// holding cfgMu, and an activation writes all of it holding cfgMu; one
// lock shared with either would queue the turn, and every configSnapshot
// behind it, on a dialog. Both are held here as a slow consent and a slow
// first open would hold them, and every runtime call returns what it was
// given. The control half: the same reader does wait on the runtime's own
// lock, so the deadline would see a wait.
func TestTheRunningProviderNeverWaitsOnCredentialCustody(t *testing.T) {
	a := &App{}
	entry := providerEntry{Name: "fixture-provider"}
	a.providers.runtime.provStatus = map[string]providerProbe{entry.Name: {
		state: "ok", models: []string{"fixture-model"}, meta: map[string]modelMeta{"fixture-model": {}},
	}}

	a.providers.custody.mu.Lock()
	a.providers.sources.mu.Lock()
	done := make(chan error, 1)
	go func() {
		done <- func() error {
			a.adoptProvider(entry)
			a.rememberPromptBudget(entry, 24000)
			a.setSubstrateCapability(substrateCapability{provider: entry.Name, model: "fixture-model", toolCalls: capYes})
			if got := a.currentProvider().Name; got != entry.Name {
				return fmt.Errorf("currentProvider = %q, want %q", got, entry.Name)
			}
			if budget, source := a.currentPromptBudget(); budget != 24000 || source != budgetDeclared {
				return fmt.Errorf("currentPromptBudget = %d %q, want 24000 declared", budget, source)
			}
			if c := a.substrateCapabilityFor(entry.Name, "fixture-model"); c.toolCalls != capYes {
				return fmt.Errorf("substrateCapabilityFor = %v, want yes", c.toolCalls)
			}
			if _, found, listed, named := a.discoveredMeta(entry.Name, "fixture-model"); !found || listed != 1 || !named {
				return fmt.Errorf("discoveredMeta before the clear = %v %d %v, want the probe's model", found, listed, named)
			}
			a.clearProviderStatus(entry.Name)
			if _, found, listed, _ := a.discoveredMeta(entry.Name, "fixture-model"); found || listed != 0 {
				return fmt.Errorf("discoveredMeta after the clear = %v %d, want nothing", found, listed)
			}
			return nil
		}()
	}()
	select {
	case err := <-done:
		a.providers.sources.mu.Unlock()
		a.providers.custody.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		a.providers.sources.mu.Unlock()
		a.providers.custody.mu.Unlock()
		t.Fatal("the runtime waited on a sign-in's custody or a credential's first open: a turn would queue behind the dialog")
	}

	// Control: the reader waits on the runtime's own lock while it is
	// held, and returns once it is let go.
	a.providers.runtime.mu.Lock()
	read := make(chan string, 1)
	go func() { read <- a.currentProvider().Name }()
	select {
	case <-read:
		a.providers.runtime.mu.Unlock()
		t.Fatal("currentProvider returned while the runtime's lock was held: the deadline above proves nothing")
	case <-time.After(50 * time.Millisecond):
	}
	a.providers.runtime.mu.Unlock()
	select {
	case got := <-read:
		if got != entry.Name {
			t.Fatalf("currentProvider = %q after the wait, want %q", got, entry.Name)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("currentProvider never returned once the runtime's lock was let go")
	}
}

// A SIGN-IN'S VIEW NEVER WAITS ON A CREDENTIAL'S FIRST OPEN. Every page
// that lists the providers or the auth profiles reads each one's sign-in
// (providerInfos, authProfileViews), and a source opened for the first
// time holds its lock across the keychain. The custody a view reads is not
// that lock, so the page answers while the dialog is up.
func TestASignInsViewNeverWaitsOnACredentialsFirstOpen(t *testing.T) {
	a := &App{}
	a.providers.custody.signIns = map[string]*pendingSignIn{
		"provider:fixture": {view: dashboard.SignInView{Status: "pending", URL: "https://authority.example/consent"}},
	}
	a.providers.sources.mu.Lock()
	got := make(chan *dashboard.SignInView, 1)
	go func() { got <- a.signInView("provider:fixture") }()
	select {
	case v := <-got:
		a.providers.sources.mu.Unlock()
		if v == nil || v.Status != "pending" || v.URL != "https://authority.example/consent" {
			t.Fatalf("signInView = %+v, want the pending attempt", v)
		}
	case <-time.After(5 * time.Second):
		a.providers.sources.mu.Unlock()
		t.Fatal("a sign-in's view waited on a credential's first open: every providers page would hang behind the dialog")
	}
}
