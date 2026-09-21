package genesis

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/genesis/genesislive"
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
func TestParallelServerAttackRefused(t *testing.T) {
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	live, err := genesislive.Fetch()
	if err != nil {
		t.Fatalf("RING0 comes from the real servers: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/genesis/pubkey":
			_, _ = w.Write(live.RootKey)
		case "/genesis/bundle":
			_, _ = w.Write(live.Ring0)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// .
	// .
	victim := NewClient(srv.URL, srv.URL, srv.URL)
	if _, err := victim.FetchRing0(); err != nil {
		t.Fatalf("control: the shipped anchor must accept the platform's own bundle: %v", err)
	}

	// .
	// .
	// .
	other, err := DomainKeyFromBundle(live.Ring5PubkeyBundle, "ring5.pubkey")
	if err != nil {
		t.Fatalf("read the Ring 5 domain key: %v", err)
	}
	defended := NewClient(srv.URL, srv.URL, srv.URL)
	defended.SetTrustRootForTest(other)
	if _, err := defended.FetchRing0(); err == nil {
		t.Fatal("THE ATTACK SUCCEEDED: the client accepted a RING0 bundle its anchor did not sign")
	}
}
