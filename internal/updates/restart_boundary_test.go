package updates

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestInstalledAwaitingRestartRefusesAnotherSwapWithoutDurableBareState(t *testing.T) {
	for _, bundled := range []bool{false, true} {
		name := "bare"
		if bundled {
			name = "bundle"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			exe := filepath.Join(dir, "aii")
			if bundled {
				exe = filepath.Join(dir, "AII OS.app", "Contents", "MacOS", "aii")
			}
			if err := os.MkdirAll(filepath.Dir(exe), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(exe, []byte("already installed candidate"), 0755); err != nil {
				t.Fatal(err)
			}
			backup := filepath.Join(dir, "retained-bundle-proof")
			if err := os.WriteFile(backup, []byte("original working image"), 0600); err != nil {
				t.Fatal(err)
			}
			c, hits := applyOwnerFixture(t, dir)
			c.state.setInstalled("9.9.9")
			// Check re-arms availability against the still-running old version.
			c.state.setAvailable("9.9.9")
			if unretiredSwap(dir) {
				t.Fatal("fixture accidentally supplied the bare-binary guard")
			}
			before := hits.Load()
			if err := c.applyTo(t.Context(), exe); !errors.Is(err, ErrUpdatePending) {
				t.Fatalf("unrestarted apply was not held: %v", err)
			}
			if hits.Load() != before {
				t.Fatal("unrestarted apply downloaded assets")
			}
			assertApplyFile(t, exe, "already installed candidate")
			assertApplyFile(t, backup, "original working image")
			if !c.state.Snapshot("").NeedsRestart {
				t.Fatal("refusal hid the required restart")
			}
		})
	}
}
