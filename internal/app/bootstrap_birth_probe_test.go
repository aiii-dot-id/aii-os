package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/ring"
)

// .
// .
// .
// .
// .
// .
// .
func firstbootApp(dir string) *App {
	// .
	// .
	// .
	a := New(&Config{
		Identity:   IdentityConfig{KeyPath: dir + "/id.sec", LedgerPath: dir + "/ledger.jsonl", DBPath: dir + "/aii.db"},
		Dashboard:  DashboardConfig{Port: 0},
		Tools:      ToolsConfig{CWD: dir},
		SourcePath: dir + "/config.json",
	})
	a.rings = ring.NewManager()
	a.ring0Content = "# Constitution\nHonesty."
	a.ring5Content = "# Ring 5\nPosture."
	a.genesisClient = genesis.NewClient("http://127.0.0.1:1", "http://127.0.0.1:1", "http://127.0.0.1:1")
	return a
}

func fetchVerifiedBootstrap(t *testing.T, root *genesistest.Root, content string) (*genesis.GenesisClient, string) {
	t.Helper()
	keyBundle, packet := root.MintBootstrapArtifacts(t, content)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bootstrap/pubkey.bundle":
			_, _ = w.Write(keyBundle)
		case "/bootstrap":
			_, _ = w.Write(packet)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client := genesis.NewClient("", "", server.URL)
	client.SetTrustRootForTest(root.Env)
	result, err := client.FetchBootstrap()
	if err != nil {
		t.Fatal(err)
	}
	return client, result.Content
}

func TestBirthRefusedWithoutVerifiedBootstrap(t *testing.T) {
	a := firstbootApp(t.TempDir())
	// .
	_, err := a.handleGenesis(context.Background(), &dashboard.GenesisRequest{Provider: "Local"})
	if err == nil || !strings.Contains(err.Error(), "bootstrap") {
		t.Fatalf("a birth without the verified bootstrap packet must refuse naming bootstrap, got: %v", err)
	}
}

func TestBirthValidatesProviderFirst(t *testing.T) {
	a := firstbootApp(t.TempDir())
	root := genesistest.NewRoot(t)
	a.genesisClient, a.bootstrapText = fetchVerifiedBootstrap(t, root, "# BOOTSTRAP.md\nSigned Firstboot fixture.")
	// .
	// .
	// .
	// .
	_, err := a.handleGenesis(context.Background(), &dashboard.GenesisRequest{
		Endpoint: "http://127.0.0.1:1", Model: "m", APIKey: "k",
	})
	if err == nil {
		t.Fatal("expected provider validation to refuse (unreachable endpoint) — a nil error means an identity was minted in a unit test")
	}
	if !strings.Contains(err.Error(), "provider validation failed") {
		t.Fatalf("the four steps start with provider validation, got: %v", err)
	}
}

func TestBirthCeremonyStillCompletes(t *testing.T) {
	const bootstrap = "# BOOTSTRAP.md\nSigned Firstboot fixture."
	root := genesistest.NewRoot(t)

	prompt := make(chan string, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer stored-birth-key" {
			http.Error(w, "missing stored provider key", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"birth-model"}]}`)
		case "/v1/chat/completions":
			var request struct {
				Messages []llm.Message `json:"messages"`
			}
			if json.NewDecoder(r.Body).Decode(&request) != nil || len(request.Messages) != 1 || request.Messages[0].Role != "system" {
				http.Error(w, "unexpected birth request", http.StatusBadRequest)
				return
			}
			prompt <- request.Messages[0].Content
			fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"Hello, I am Nova."},"finish_reason":"stop"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()

	dir := t.TempDir()
	t.Chdir(dir)
	cfg := defaultConfig()
	cfg.Identity = IdentityConfig{
		KeyPath: filepath.Join(dir, "identity.sec"), LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	}
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.Dashboard.Port = 0
	cfg.Tools.CWD = dir
	writeTestProviders(t, dir, providerEntry{
		Name: "Local", APIType: "openai", URL: provider.URL + "/v1",
		APIKey: "stored-birth-key", DefaultModel: "birth-model", Default: true,
	})
	a := New(cfg)
	defer a.Stop()
	a.genesisClient, a.bootstrapText = fetchVerifiedBootstrap(t, root, bootstrap)
	a.ring0Content = "# Constitution\nHonesty."
	a.ring0Bundle = root.MintRing0Bundle(t, a.ring0Content)
	a.ring5Content = "# Ring 5\nSecurity posture."

	greeting, err := a.handleGenesis(t.Context(), &dashboard.GenesisRequest{
		Provider: "Local",
		Endpoint: provider.URL + "/v1", Model: "birth-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	if greeting != "Hello, I am Nova." || !a.live || a.ledger == nil {
		t.Fatalf("birth did not reach live unchanged: greeting=%q live=%t ledger=%v", greeting, a.live, a.ledger != nil)
	}
	if got := a.store.IdentityName(); got != "Nova" {
		t.Fatalf("name minted from the founding reply: got %q, want %q", got, "Nova")
	}
	if a.ledger.LastSeq() != 1 {
		t.Fatalf("birth minted %d events, want only ring0.genesis", a.ledger.LastSeq())
	}
	identity, err := a.store.PromptIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if identity.HasOperatorRelationship || identity.Charter != "" {
		t.Fatalf("birth must not mint Ring 1: %+v", identity)
	}
	composed, err := a.composer.Compose("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(composed.Text, "present a Ring 1 proposal for their affirmation or negation") {
		t.Fatal("post-birth prompt is missing the brief Ring 1 proposal reminder")
	}
	if got := <-prompt; got != bootstrap {
		t.Fatalf("verified bootstrap prompt changed: %q", got)
	}
}

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
func TestBootstrapNotFetchedWithoutVerifiedRing0(t *testing.T) {
	var bootstrapHits int
	bootstrapSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bootstrapHits++
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":"Genesis token required — download from genesis.aiii.id first"}`))
	}))
	defer bootstrapSrv.Close()

	// .
	// .
	genesisSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"not":"a signed bundle"}`))
	}))
	defer genesisSrv.Close()

	a := firstbootApp(t.TempDir())
	a.ring0Content = ""
	a.genesisClient = genesis.NewClient(genesisSrv.URL, "http://127.0.0.1:1", bootstrapSrv.URL)

	a.fetchFoundingArtifacts(Config{})

	if a.ring0Content != "" {
		t.Fatal("precondition: RING0 must fail for this test to mean anything")
	}
	if bootstrapHits != 0 {
		t.Fatalf("bootstrap was requested %d time(s) with no genesis token to present — "+
			"the 402 it earns sends the operator to genesis.aiii.id, which is where the real "+
			"failure already happened", bootstrapHits)
	}
}
