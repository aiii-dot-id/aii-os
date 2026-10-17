package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

func TestAWindowsExecRowSaysWhatItsAuthorMustChange(t *testing.T) {
	got := wrongMode("meaning-worker.exe", "exec", "windows").Error()
	for _, said := range []string{"meaning-worker.exe has the wrong mode", `lists it as "exec"`, "Windows keeps no executable mark", `as "file"`, "inventory", "when packing"} {
		if !strings.Contains(got, said) {
			t.Errorf("the refusal does not say %q: %s", said, got)
		}
	}
	for _, c := range []struct{ mode, platform string }{{"exec", "linux"}, {"exec", "macos"}, {"file", "windows"}, {"file", "linux"}} {
		if got := wrongMode("bin/worker", c.mode, c.platform).Error(); got != "bin/worker has the wrong mode" {
			t.Errorf("the %s row on %s: %q, want the words as they were", c.mode, c.platform, got)
		}
	}
}

// The installed root's check says it for the platform it runs on: an
// "exec" row over a file with no executable mark — what every file is on
// Windows — is refused in this platform's words, and on Windows in the
// author's.
func TestTheInstalledRootsWrongModeIsSaidForThisPlatform(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	worker, carrier := []byte("worker"), []byte("carrier")
	for name, b := range map[string][]byte{"meaning-worker.exe": worker, "carrier.exe": carrier} {
		if err := os.WriteFile(filepath.Join(root, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	digest := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
	inv, err := json.Marshal(packagefmt.Inventory{InstalledBytes: int64(len(worker)),
		Files: []packagefmt.InventoryEntry{{Path: "meaning-worker.exe", Size: int64(len(worker)), SHA256: "sha256:" + digest(worker), Mode: "exec"}}})
	if err != nil {
		t.Fatal(err)
	}
	d := &RuntimeDecl{VariantID: "windows-x86_64-cpu", InventorySHA256: digest(inv), Files: 1, InstalledBytes: int64(len(worker))}
	entry := entrypointSpec{Name: "carrier.exe", Digest: "sha256:" + digest(carrier)}
	rec, err := json.Marshal(plugstore.RootRecord{VariantID: d.VariantID, InventorySHA256: d.InventorySHA256, ArchiveSHA256: digest(inv),
		Entrypoint: entry.Name, EntrypointSHA256: entry.Digest, Files: 1, InstalledBytes: int64(len(worker)), Inventory: inv})
	if err != nil {
		t.Fatal(err)
	}
	_, err = verifyStoreRoot(context.Background(), root, rec, nil, d, entry, packagefmt.TreeLimits{})
	want := wrongMode("meaning-worker.exe", "exec", packagefmt.HostPlatform()).Error()
	if err == nil || err.Error() != want {
		t.Fatalf("refused %v, want %q", err, want)
	}
	if runtime.GOOS == "windows" && !strings.Contains(err.Error(), "when packing") {
		t.Fatalf("on Windows the refusal is the author's to act on: %v", err)
	}
}
