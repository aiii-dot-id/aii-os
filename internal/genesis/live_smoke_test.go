package genesis

// The drift check the hermetic suite cannot perform.
//
// Every birth test in this repo verifies fixtures the client itself
// defines: genesistest mints exactly what verifyBundle expects, so
// client and fixtures agree by construction. The one disagreement they
// cannot detect is the live server disagreeing with both — and on
// 2026-08-23 that is what happened. genesis.aiii.id served a Ring 0
// payload the shipped client rejected, a fresh install could not be
// born, and because the bootstrap packet is token-gated the operator's
// log blamed a bootstrap 402 pointing back at genesis.
//
// So this runs the SHIPPED client and the SHIPPED pinned root against
// the production servers, in birth order. Opt-in (LIVE_SMOKE=1)
// because it depends on a third party being reachable: a red run here
// means the world moved, not that a change is bad.
//
// Extends the 2026-08-18 Ring 5 smoke rather than adding a second live
// owner. Being in package genesis, it can hold the live pubkey against
// the embedded pin directly.

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestLiveChainSmoke(t *testing.T) {
	if os.Getenv("LIVE_SMOKE") == "" {
		t.Skip("set LIVE_SMOKE=1")
	}
	client := &http.Client{Timeout: 20 * time.Second}

	// 1. The pin the whole chain hangs from. pinnedroot.go documents
	//    this as a curl-diff anyone anywhere can run; running it is
	//    cheaper than trusting that someone did.
	resp, err := client.Get("https://genesis.aiii.id/genesis/pubkey")
	if err != nil {
		t.Fatalf("fetch live /genesis/pubkey: %v", err)
	}
	live, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read live /genesis/pubkey: %v", err)
	}
	if !bytes.Equal(bytes.TrimSpace(live), bytes.TrimSpace(pinnedRootJSON)) {
		t.Fatalf("embedded pinned root no longer matches live /genesis/pubkey.\n"+
			"Rotation is a release event: a replacement root ships in a new binary, "+
			"cross-signed by this one. If this fires unexpectedly, treat the live key as "+
			"suspect until the cross-signature is reviewed.\nlive:   %s\npinned: %s",
			bytes.TrimSpace(live), bytes.TrimSpace(pinnedRootJSON))
	}

	c := NewClient("https://genesis.aiii.id", "https://firewall.aiii.id", "https://bootstrap.aiii.id")

	// 2. RING0 — the step that broke, and the step that mints the
	//    genesis token everything downstream is gated on.
	r0, err := c.FetchRing0()
	if err != nil {
		t.Fatalf("FetchRing0 failed against the live server — the shipped client can no longer "+
			"verify what genesis.aiii.id serves, so a fresh install cannot be born: %v", err)
	}
	if len(r0.Content) == 0 {
		t.Fatal("RING0 verified but carried no constitution")
	}
	if r0.Token == "" {
		t.Fatal("RING0 verified but minted no X-Genesis-Token — the bootstrap packet is gated on it, " +
			"and without it birth fails downstream with a misleading 402")
	}
	c.SetToken(r0.Token)

	// 3. Ring 5 — the security posture.
	r5, err := c.FetchRing5()
	if err != nil {
		t.Fatalf("FetchRing5 failed: %v", err)
	}
	if len(r5.Content) == 0 {
		t.Fatal("empty ring5 content")
	}

	// 4. Bootstrap — token-gated. Reaching it proves the whole chain,
	//    which is the artifact half of a real birth.
	bs, err := c.FetchBootstrap()
	if err != nil {
		t.Fatalf("FetchBootstrap failed with a valid genesis token: %v", err)
	}
	if len(bs.Content) == 0 {
		t.Fatal("bootstrap packet verified but carried no prompt")
	}

	t.Logf("LIVE CHAIN VERIFIED: pin matches; RING0 %d bytes (token minted); Ring 5 %d bytes; bootstrap %d bytes",
		len(r0.Content), len(r5.Content), len(bs.Content))
}
