package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/continuity"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
)

// The restore uses the production embedded entry, restart request, checked
// teardown and next boot. Only the signed Ring 5 fixture is supplied locally.
// Native transport and the mobile handoff itself have separate shell tests.
func TestEmbeddedRestartRestoresAndPutsBackTheSameIdentity(t *testing.T) {
	r := newRestoreRig(t)
	t.Chdir(r.dir)
	r.cfg.Genesis = GenesisConfig{ServerURL: "http://127.0.0.1:1", FirewallURL: "http://127.0.0.1:1", BootstrapURL: "http://127.0.0.1:1"}
	r.cfg.Certificate.ServerURL = "none"
	r.cfg.Dashboard.Host = "127.0.0.1"
	through, snap := r.lived(t, 1, "before", true)
	later, _ := r.lived(t, 1, "after", false)
	key, err := os.ReadFile(r.keyPath)
	if err != nil {
		t.Fatal(err)
	}
	boot := func() (*App, error) {
		a := New(r.cfg)
		a.ring5Content = genesistest.NewRoot(t).Ring5(t)
		if err := a.StartAuthenticatedEmbedded(); err != nil {
			_ = a.Shutdown(context.Background())
			return nil, err
		}
		return a, nil
	}
	a, err := boot()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := a.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	fingerprint := a.keyPair.Fingerprint()
	token := a.dashboardToken()
	handoff := func(source, name string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		old := a
		type result struct {
			next *App
			err  error
		}
		finished := make(chan result, 1)
		go func() {
			if !old.WaitForRestart() {
				finished <- result{err: context.Canceled}
				return
			}
			if err := old.Shutdown(ctx); err != nil {
				finished <- result{err: err}
				return
			}
			next, err := boot()
			finished <- result{next, err}
		}()
		if err := askRestore(t, old, source, name); err != nil {
			t.Fatal(err)
		}
		if err := old.Restart(); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-finished:
			if got.err != nil {
				t.Fatal(got.err)
			}
			a = got.next
		case <-ctx.Done():
			t.Fatal("requested handoff did not finish")
		}
		if old.store != nil || old.ledger != nil {
			t.Fatal("successor overlaps predecessor resources")
		}
		if a.keyPair.Fingerprint() != fingerprint || a.dashboardToken() != token {
			t.Fatal("identity or credential changed")
		}
		if reason, safe := a.SafeMode(); safe {
			t.Fatalf("valid restore manufactured SAFE: %s", reason)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(a.configSnapshot().Identity.LedgerPath), continuity.RestoreRequestName)); !os.IsNotExist(err) {
			t.Fatal("request outlived completed restore", err)
		}
	}
	handoff(continuity.SourceSnapshot, snap)
	if a.ledger.LastSeq() != through {
		t.Fatalf("restore ends at %d, want %d", a.ledger.LastSeq(), through)
	}
	sets := continuityDoor(a).View().SetAside
	if len(sets) != 1 {
		t.Fatalf("newer history not preserved: %+v", sets)
	}
	handoff(continuity.SourceSetAside, sets[0].Name)
	if a.ledger.LastSeq() != later {
		t.Fatalf("put-back ends at %d, want %d", a.ledger.LastSeq(), later)
	}
	now, err := os.ReadFile(r.keyPath)
	if err != nil || !bytes.Equal(key, now) {
		t.Fatal("restart/restore changed the signing key", err)
	}
}
