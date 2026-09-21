package genesis

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesislive"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

func TestFetchBundleRejectsOversizeResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte{'x'}, maxBundleBytes+1))
	}))
	t.Cleanup(server.Close)

	client := NewClient(server.URL, "", "")
	_, _, err := client.fetchBundle(server.URL, "ring0")
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversize response returned %v", err)
	}
}

func TestFetchBootstrapRequiresDomainKeyChain(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)

	client := NewClient("", "", server.URL)
	_, err := client.FetchBootstrap()
	if err == nil || !strings.Contains(err.Error(), "bootstrap.pubkey domain-key chain unavailable") {
		t.Fatalf("missing bootstrap key chain returned %v", err)
	}
}

func TestFetchRing5RequiresSignedCurrentManifest(t *testing.T) {
	// .
	// .
	// .
	// .
	live, err := genesislive.Fetch()
	if err != nil {
		t.Fatalf("Ring 5 comes from the real servers: %v", err)
	}
	var serveManifest atomic.Bool
	serveManifest.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		switch r.URL.Path {
		case "/v1/ring5/pubkey.bundle":
			body = live.Ring5PubkeyBundle
		case "/v1/ring5/bundle":
			body = live.Ring5Bundle
		case "/v1/ring5/manifest":
			if !serveManifest.Load() {
				http.NotFound(w, r)
				return
			}
			body = live.Ring5Manifest
		default:
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)

	client := NewClient("", server.URL, "")
	result, err := client.FetchRing5()
	if err != nil {
		t.Fatalf("the live Ring 5 chain must verify against the shipped pin: %v", err)
	}
	if result.Content == "" {
		t.Fatal("Ring 5 verified but carried no posture")
	}

	serveManifest.Store(false)
	if _, err := client.FetchRing5(); err == nil || !strings.Contains(err.Error(), "manifest fetch failed") {
		t.Fatalf("missing manifest returned %v", err)
	}
}

func TestValidateRing5ManifestCurrentnessAndBinding(t *testing.T) {
	now := time.Now().UTC()
	bundle := []byte("bundle bytes")
	key := &publicKeyEnvelope{KeyID: "aiii_ring5_test"}
	valid := ring5Manifest{
		Kind:             "ring5.manifest",
		SchemaVersion:    1,
		ArtifactVersion:  "test-1",
		ArtifactHash:     sigenvelope.SHA256Prefixed(bundle),
		KeyID:            key.KeyID,
		SignatureProfile: crypto.ProfileRoot,
		NotBefore:        now.Add(-time.Hour).Format(time.RFC3339),
		ExpiresAt:        now.Add(time.Hour).Format(time.RFC3339),
		Critical:         true,
	}

	for _, tc := range []struct {
		name   string
		mutate func(*ring5Manifest)
		want   string
	}{
		{"expired", func(m *ring5Manifest) { m.ExpiresAt = now.Format(time.RFC3339) }, "expired"},
		{"wrong bundle", func(m *ring5Manifest) { m.ArtifactHash = "sha256:wrong" }, "does not match"},
		{"revoked bundle", func(m *ring5Manifest) { m.RevokedArtifactHashes = []string{m.ArtifactHash} }, "revokes active"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := valid
			tc.mutate(&m)
			payload, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			err = validateRing5Manifest(payload, bundle, key, now)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}
