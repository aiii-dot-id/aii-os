//go:build !windows

package app

import (
	"os"
	"path/filepath"
	"testing"
)

// ONE PLUGIN'S DIRECTORY THAT CANNOT BE LISTED IS THE LISTING'S FAILURE,
// never that plugin's absence: read as absent, the facility withdrew the
// plugin it holds. A directory that is simply gone
// between the listing and the look at it is no plugin, as before.
func TestAPluginDirectoryThatCannotBeReadIsAFailedListing(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads a directory at mode 0")
	}
	t.Chdir(t.TempDir())
	locked := filepath.Join("plugins", "org.example.locked")
	if err := os.MkdirAll(locked, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "org.example.locked-1.0.0.aiiospkg"), []byte("p"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o750) })
	if d := (&App{}).discoverPlugins(); d.Err == nil {
		t.Fatalf("A PLUGIN'S DIRECTORY THAT COULD NOT BE READ WAS TAKEN FOR ITS ABSENCE: %+v", d)
	}
}
