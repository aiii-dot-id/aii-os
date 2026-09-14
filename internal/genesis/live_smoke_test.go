package genesis

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
// .

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

	// .
	// .
	// .
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

	// .
	// .
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

	// .
	r5, err := c.FetchRing5()
	if err != nil {
		t.Fatalf("FetchRing5 failed: %v", err)
	}
	if len(r5.Content) == 0 {
		t.Fatal("empty ring5 content")
	}

	// .
	// .
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
