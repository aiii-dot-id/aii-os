package genesistest

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
)

// TestFreshGenesisSigningCeremonies is the one live dual-PQ signing lane behind
// the immutable verifier vectors. It proves newly generated root and domain
// keys can still drive production Birth and bootstrap-chain verification.
func TestFreshGenesisSigningCeremonies(t *testing.T) {
	root, err := packagetest.NewRole("aiii_ring0_fresh_test_k1", packagetest.KeyTypePlatformRelease)
	if err != nil {
		t.Fatal(err)
	}
	ring0, err := root.Sign("ring0.bundle", map[string]any{
		"kind":          "ring0",
		"laws":          defaultConstitution,
		"purpose":       "AIII Core Identity Boot",
		"ring0_version": 1,
		"ordinal":       1,
		"timestamp":     1,
	})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	born, err := genesis.Birth(&genesis.BirthConfig{
		Name:        "FreshCeremony",
		Ring0Bundle: ring0,
		Root:        root.Env,
		KeyPath:     filepath.Join(dir, "identity.sec"),
		LedgerPath:  filepath.Join(dir, "ledger.jsonl"),
		DBPath:      filepath.Join(dir, "aii.db"),
	})
	if err != nil {
		t.Fatalf("fresh Ring 0 ceremony did not birth: %v", err)
	}
	born.Ledger.Close()

	domain, err := packagetest.NewRole("aiii_bootstrap_fresh_test_k1", "bootstrap")
	if err != nil {
		t.Fatal(err)
	}
	keyBundle, err := root.Sign("bootstrap.pubkey", domain.Env)
	if err != nil {
		t.Fatal(err)
	}
	const prompt = "# BOOTSTRAP.md\nFresh signed ceremony."
	packet, err := domain.Sign("bootstrap.packet", map[string]any{
		"kind": "bootstrap", "bootstrap_markdown": prompt,
	})
	if err != nil {
		t.Fatal(err)
	}

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
	got, err := client.FetchBootstrap()
	if err != nil {
		t.Fatalf("fresh bootstrap ceremony did not verify: %v", err)
	}
	if got.Content != prompt {
		t.Fatalf("fresh bootstrap content = %q, want %q", got.Content, prompt)
	}
}
