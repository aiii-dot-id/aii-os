package app

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAVendorsPublishedWindowNarrowsTheEntrysAtItsOwnAddressOnly(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"vendor/new-model","context_length":131072}]}`))
	}))
	t.Cleanup(srv.Close)
	addr := srv.Listener.Addr().String()
	previous := httpDiscoveryClient.Transport
	everywhere := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // the test server's own certificate, on loopback
	}
	httpDiscoveryClient.Transport = everywhere
	t.Cleanup(func() {
		httpDiscoveryClient.Transport = previous
		everywhere.CloseIdleConnections()
	})
	shipped := entryNamed(embeddedRegistry(), "OpenRouter")
	if shipped == nil || shipped.Local {
		t.Fatal("the shipped registry names no hosted OpenRouter entry")
	}
	resolve := func(url string, probeAnyway bool) (*App, providerEntry) {
		t.Helper()
		dir := t.TempDir()
		writeTestProviders(t, dir, providerEntry{Name: "OpenRouter", APIType: "openai", URL: url, APIKey: "k",
			DefaultModel: "vendor/new-model", ContextLength: 1_000_000, MaxOutputTokens: 32_000})
		block := LLMConfig{Provider: "OpenRouter", Model: "vendor/new-model"}
		a := resolverApp(dir, block)
		reg, err := a.loadProviders()
		if err != nil {
			t.Fatal(err)
		}
		a.askWindowIfUndeclaredIn(reg, block)
		if probeAnyway {
			a.probeProviders(reg)
		}
		cc, entry, err := a.resolveLLMConfig(Config{LLM: block}, reg)
		if err != nil {
			t.Fatal(err)
		}
		client := a.newLLMClient(cc, 0)
		a.adoptRuntime(entry, client)
		a.llmSwap = newSwappableLLM(client)
		if !a.providerRuntimeMatches(reg) {
			t.Fatalf("at %s the running client no longer matches the unchanged file: every reload would rebuild it", url)
		}
		return a, entry
	}

	if _, home := resolve(shipped.URL, false); home.ContextLength != 131072 {
		t.Fatalf("at the vendor's own address the window it publishes for the model must hold the entry's: %d", home.ContextLength)
	}
	away, entry := resolve("https://relay.example/api/v1", true)
	if m, found, _, _ := away.discoveredMeta("OpenRouter", "vendor/new-model"); !found || m.Context != 131072 {
		t.Fatalf("the list at the other address was not read, so this shows nothing: %+v %v", m, found)
	}
	if entry.ContextLength != 1_000_000 {
		t.Fatalf("at another address what a server publishes is only its word: the entry's window must stand, got %d", entry.ContextLength)
	}
}
