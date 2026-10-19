package app

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

func TestCandidateKeepsNativeMetadataOnlyForTheAcceptedEntry(t *testing.T) {
	entry := providerEntry{Name: "fixture-provider", URL: "https://provider.example/v1", APIKey: "synthetic-key"}
	images := true
	probe := providerProbe{
		state: probeOK, models: []string{"fixture-model"},
		meta:      map[string]modelMeta{"fixture-model": {Context: 65536, MaxOut: 8192, Images: &images}},
		checkedAt: time.Unix(1000, 0), key: probeKey(entry),
	}
	a := &App{}
	a.providers.runtime.provStatus = map[string]providerProbe{
		entry.Name: probe,
		"other":    probe,
	}
	a.retainProviderStatusFor(entry)
	if got, ok := a.providers.runtime.provStatus[entry.Name]; !ok || !reflect.DeepEqual(got, probe) {
		t.Fatalf("accepted candidate lost its native metadata: %+v", got)
	}
	entry.URL = "https://different.example/v1"
	a.retainProviderStatusFor(entry)
	if got, ok := a.providers.runtime.provStatus[entry.Name]; ok {
		t.Fatalf("different endpoint retained the previous provider's evidence: %+v", got)
	}
	if got, ok := a.providers.runtime.provStatus["other"]; !ok || !reflect.DeepEqual(got, probe) {
		t.Fatalf("candidate invalidation discarded another provider's evidence: %+v", got)
	}
}

func TestAProviderEditKeepsItsAcceptedNativeImageEvidence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	entry := providerEntry{
		Name: "fixture", APIType: "openai", URL: "http://127.0.0.1:1/v1", Local: true,
		DefaultModel: "fixture-model", Default: true, ContextLength: 65536, MaxOutputTokens: 8192,
	}
	writeTestProviders(t, dir, entry)
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.LLM = LLMConfig{Provider: entry.Name, Model: entry.DefaultModel, TimeoutSeconds: 5, Retries: -1}
	stream := false
	cfg.LLM.Stream = &stream
	a := New(cfg)
	t.Cleanup(func() { stopApp(t, a) })
	a.llmSwap = newSwappableLLM(llm.New(&llm.ClientConfig{Endpoint: entry.URL, Model: entry.DefaultModel}))
	entry.URL = server.URL
	a.providers.runtime.provStatus = map[string]providerProbe{entry.Name: {
		state: probeOK, key: probeKey(entry), checkedAt: time.Now(), models: []string{entry.DefaultModel},
		meta: map[string]modelMeta{entry.DefaultModel: {Images: boolp(true)}},
	}}
	if err := a.changeProviders(entry.Name, func(reg *providerRegistry) error {
		reg.Providers[0].URL = entry.URL
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := a.imagePolicyFor(entry.Name, entry.DefaultModel).takes; got != capYes {
		t.Fatalf("provider edit discarded its accepted native image evidence: %s", got)
	}
}

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
