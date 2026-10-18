package pluginhost

import (
	"path/filepath"
	"testing"
)

// WHAT LIES IN THE STORE IS NAMED BY ITS ENTRY: a root's or a view's
// directory directly under r/ or v/, and nothing else — not a file inside
// one, not a blob, not a path outside the store.
func TestAStorePathIsNamedByItsEntryAlone(t *testing.T) {
	store := filepath.Join(t.TempDir(), "plugins")
	for path, want := range map[string]string{
		filepath.Join(store, "r", "0123456789abcdef"):                   "r/0123456789abcdef",
		filepath.Join(store, "v", "fedcba9876543210"):                   "v/fedcba9876543210",
		filepath.Join(store, "v", "fedcba9876543210", "m.bin"):          "",
		filepath.Join(store, "b", "fedcba9876543210"):                   "",
		filepath.Join(store, "r", "NOT-A-KEY"):                          "",
		filepath.Join(t.TempDir(), "r", "0123456789abcdef"):             "",
		filepath.Join(store, "..", "plugins2", "r", "0123456789abcdef"): "",
	} {
		e, ok := storeEntryAt(store, path)
		got := ""
		if ok {
			got = e.String()
		}
		if got != want {
			t.Errorf("%s: %q, want %q", path, got, want)
		}
	}
	if _, ok := storeEntryAt("", filepath.Join(store, "r", "0123456789abcdef")); ok {
		t.Error("a host with no store names an entry")
	}
}
