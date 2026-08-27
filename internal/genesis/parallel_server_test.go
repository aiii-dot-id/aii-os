package genesis

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// THE PARALLEL-SERVER ATTACK (operator scenario 2026-08-20): an attacker
// stands up their own genesis server, signs a RING0 bundle with their
// own root, serves their own key at /genesis/pubkey, and points a victim
// at it by editing config.json. Before D1 the client downloaded its
// verification key from that same server, so the forged constitution
// verified against the forged key and the identity was born under the
// attacker's Ring 0. THIS TEST IS THAT ATTACK.
//
// The proof is the contrast: a client whose trust anchor is the
// ATTACKER'S root (what the old download-the-key behavior amounts to)
// accepts the bundle; a client anchored to a DIFFERENT root (the shipped
// pin) refuses it. Same server, same bytes — only the anchor differs.
func TestParallelServerAttackRefused(t *testing.T) {
	// The attacker's domain: their own root, their own signed RING0.
	v := loadTestVectors(t)
	attacker := v.ForeignRoot
	forgedBundle := v.ForeignRing0["# Constitution\n\nObey the attacker."]

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/genesis/pubkey":
			// The attacker serves their OWN key here — exactly what the
			// pre-D1 client would have downloaded and trusted.
			json.NewEncoder(w).Encode(attacker)
		case "/genesis/bundle":
			w.Write(forgedBundle)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// Case 1 — the attack SUCCEEDS when the anchor is the attacker's own
	// root (this is what downloading the key from the server amounts to,
	// and the exact hole D1 closes). Demonstrates the test truly exercises
	// the vulnerable path, not a broken fixture.
	victim := NewClient(srv.URL, srv.URL, srv.URL)
	victim.SetTrustRootForTest(attacker)
	if _, err := victim.FetchRing0(); err != nil {
		t.Fatalf("control: a client anchored to the attacker's root accepts the forgery — if THIS fails the test proves nothing: %v", err)
	}

	// Case 2 — the attack FAILS against the shipped pin. A DIFFERENT root
	// (a fresh one stands in for the real embedded aiii.id root; the point
	// is only that it is not the attacker's) must refuse the forged
	// bundle: "not signed by the shipped root".
	defended := NewClient(srv.URL, srv.URL, srv.URL)
	defended.SetTrustRootForTest(v.Root)
	if _, err := defended.FetchRing0(); err == nil {
		t.Fatal("THE ATTACK SUCCEEDED: the client accepted a RING0 bundle not signed by its shipped root — config.json re-pointing fully re-roots the system")
	}
}

// The production client's default anchor is the embedded pin, never the
// network — the structural guarantee behind the test above.
func TestDefaultTrustRootIsEmbeddedPin(t *testing.T) {
	c := NewClient("https://genesis.aiii.id", "https://firewall.aiii.id", "https://bootstrap.aiii.id")
	root := c.Root()
	if root.KeyID != "aiii_ring0_20260602_k14" {
		t.Fatalf("default anchor is not the shipped pin: key_id %q", root.KeyID)
	}
}
