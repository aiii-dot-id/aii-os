package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// startLiveForTest starts a host from an empty data directory that already
// has the schema in place (store.SeedForTest): creating it is most of a second
// under the race detector and a hundred and forty tests start a host. A path
// that cannot be seeded is left to the start to refuse in its own words. A
// test whose subject is whether boot creates the database starts unseeded.
func startLiveForTest(a *App) error {
	if path := a.configSnapshot().Identity.DBPath; path != "" {
		_ = store.SeedForTest(path)
	}
	return startLiveUnseededForTest(a)
}

func startLiveUnseededForTest(a *App) error {
	if err := startLiveUnsettled(a); err != nil {
		return err
	}
	if !settlePlugins(a, 30*time.Second) {
		stuck := inFlight(a)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return errors.Join(fmt.Errorf("the plugins never settled: %s", strings.Join(stuck, "; ")), a.Shutdown(ctx))
	}
	return nil
}

// startLiveUnsettled boots under the live Ring 5 without waiting for the
// plugins to settle.
func startLiveUnsettled(a *App) error {
	ring5, err := genesistest.LiveRing5()
	if err != nil {
		return err
	}
	a.ring5Content = ring5
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
	if a.timeFac != nil || a.live.Load() {
		t.Fatal("missing Ring 5 started the live runtime")
	}

	stopApp(t, a)
	after, afterMtime := fileDigest(t, dbPath)
	if after != before || !afterMtime.Equal(mtime) {
		t.Fatal("Ring 5 refusal modified the prior projection")
	}
}
