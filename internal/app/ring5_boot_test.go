package app

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ring"
)

// startLiveForTest stages content already verified by the genesis package;
// production can populate this field only through FetchRing5.
func startLiveForTest(a *App) error {
	a.ring5Content = "# Verified Ring 5 test fixture"
	return a.startLive()
}

func TestMissingRing5EntersSafeBeforeReplay(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "NoRing5")
	buildPriorProjection(t, ledgerPath, dbPath)
	before, mtime := fileDigest(t, dbPath)

	cfg := safebootConfig(t, dir, "NoRing5", keyPath, ledgerPath, dbPath)
	cfg.Genesis.FirewallURL = ""
	a := New(cfg)
	if err := a.startLive(); err != nil {
		t.Fatalf("missing Ring 5 must enter SAFE: %v", err)
	}
	reason, safe := a.SafeMode()
	if !safe || !strings.Contains(reason, "security_posture.absent") {
		t.Fatalf("SAFE reason = %q", reason)
	}
	if got := a.rings.GetContent(ring.Ring5); got != "" {
		t.Fatalf("missing Ring 5 was replaced with local content: %q", got)
	}
	if a.timeFac != nil || a.live {
		t.Fatal("missing Ring 5 started the live runtime")
	}

	a.Stop()
	after, afterMtime := fileDigest(t, dbPath)
	if after != before || !afterMtime.Equal(mtime) {
		t.Fatal("Ring 5 refusal modified the prior projection")
	}
}
