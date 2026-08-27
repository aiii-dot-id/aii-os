package genesis

import (
	"os"
	"testing"
)

func TestFetchRing0FromProduction(t *testing.T) {
	if os.Getenv("LIVE_SMOKE") != "1" {
		t.Skip("set LIVE_SMOKE=1 to probe production genesis")
	}
	client := NewClient(
		"https://genesis.aiii.id",
		"https://firewall.aiii.id",
		"https://bootstrap.aiii.id",
	)

	result, err := client.FetchRing0()
	if err != nil {
		t.Fatalf("fetch production Ring 0: %v", err)
	}

	if result.Content == "" {
		t.Error("RING0 content is empty")
	}
	if len(result.Content) < 100 {
		t.Errorf("RING0 content suspiciously short: %d bytes", len(result.Content))
	}

	t.Logf("RING0 verified: %d bytes; genesis token present: %t", len(result.Content), result.Token != "")
}
