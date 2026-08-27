package witness

// Pins the platform-envelope contract added by the 2026-08-19
// sigenvelope consolidation (genesis parity, finding-9 analog): the
// platform key envelope — operator path or genesis download — is
// validated (validity window, fingerprint binding of every key entry)
// before any manifest signature is trusted. Before the consolidation an
// expired platform envelope verified manifests silently.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestVerifyManifestPlatformEnvelopeContract(t *testing.T) {
	p := synthPlatform(t)
	fw := newFakeWitness(t)
	manifest := buildManifest(t, p, fw.witnessEnv, false)

	serve := func(manifestBytes []byte) *Client {
		mux := http.NewServeMux()
		mux.HandleFunc("/witness/pubkey/manifest", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(manifestBytes)
		})
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)
		return New(srv.URL, "")
	}

	expectContractRefusal := func(t *testing.T, env *PublicKeyEnvelope, what string) {
		t.Helper()
		pCopy := *p
		pCopy.env = env
		err := serve(manifest).VerifyManifest(fw.witnessEnv, pCopy.writeEnv(t))
		if err == nil {
			t.Fatalf("%s must refuse manifest verification", what)
		}
		if !strings.Contains(err.Error(), "platform pubkey envelope") {
			t.Fatalf("%s must be refused by the envelope contract, not downstream: %v", what, err)
		}
	}

	t.Run("expired platform envelope refused", func(t *testing.T) {
		expired := *p.env
		expired.ExpiresAt = time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
		expectContractRefusal(t, &expired, "expired platform key envelope")
	})

	t.Run("broken fingerprint binding refused", func(t *testing.T) {
		bad := *p.env
		bad.Keys = append([]PublicKeyMaterial(nil), p.env.Keys...)
		bad.Keys[0].PublicKeyFingerprint = sha256Prefixed([]byte("not the binding"))
		expectContractRefusal(t, &bad, "platform key with broken fingerprint binding")
	})

	t.Run("honest platform envelope still verifies", func(t *testing.T) {
		if err := serve(manifest).VerifyManifest(fw.witnessEnv, p.writeEnv(t)); err != nil {
			t.Fatalf("honest manifest must verify: %v", err)
		}
	})
}
