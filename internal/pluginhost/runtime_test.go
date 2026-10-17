//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

func TestAStoreRootIsFetchedVerifiedAndPublished(t *testing.T) {
	fx := newRuntimeFixture(t)
	w := openStore(t)
	root, err := storeRoot(t, w, &fx.decl, fx.entry, fx.fetch, packagefmt.TreeLimits{})
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	key := plugstore.RootKey(fx.decl.VariantID, fx.decl.InventorySHA256, fx.entry.Name, fx.entry.Digest)
	if filepath.Base(root) != key || fx.fetches != 1 {
		t.Fatalf("root %s, fetches %d", root, fx.fetches)
	}
	// The carrier sits at the root — its parent IS the runtime root, the
	// rule the voice carrier is frozen with — and the record sits beside
	// the root, never inside the tree the engine checks.
	if got, err := os.ReadFile(filepath.Join(root, "aii-voice.exe")); err != nil || !bytes.Equal(got, fx.entry.Bytes) {
		t.Fatalf("the carrier must be placed at the root: %v", err)
	}
	if _, err := os.Stat(root + plugstore.RecordSuffix); err != nil {
		t.Fatalf("the record lives beside the root: %v", err)
	}
	for _, stray := range []string{".inventory.json", ".record.json", "inventory.json", "bin"} {
		if _, err := os.Lstat(filepath.Join(root, stray)); !os.IsNotExist(err) {
			t.Fatalf("the writer adds nothing to the tree: %s is there", stray)
		}
	}
	if n := storePartials(t, w); n != 0 {
		t.Fatalf("%d partial directories left after publication", n)
	}
	if archive, _ := w.Archive(fx.decl.SHA256); !isRegular(archive) {
		t.Fatal("the verified archive is kept for a carrier-only release")
	}
	if err := verifyRootIn(t, w, root, &fx.decl, fx.entry, packagefmt.TreeLimits{}); err != nil {
		t.Fatalf("a published root verifies: %v", err)
	}
	pin, err := w.PinRoot(key)
	if err != nil {
		t.Fatal(err)
	}
	defer pin.Close()
	if err := runtimeSpawnCheck(pin, root, &fx.decl, fx.entry)(); err != nil {
		t.Fatalf("the per-spawn check passes: %v", err)
	}
	// A second ensure finds the root present and fetches nothing.
	if again, err := storeRoot(t, w, &fx.decl, fx.entry, fx.fetch, packagefmt.TreeLimits{}); err != nil || again != root || fx.fetches != 1 {
		t.Fatalf("a present root is not refetched: %v %s fetches %d", err, again, fx.fetches)
	}
	// A release that changes only its carrier gets its own root — the two
	// never alias one directory — built from the stored archive without a
	// download; both roots verify.
	carrier2 := []byte("#!carrier two\n")
	c2 := sha256.Sum256(carrier2)
	entry2 := entrypointSpec{Name: fx.entry.Name, Bytes: carrier2, Digest: "sha256:" + hex.EncodeToString(c2[:])}
	root2, err := storeRoot(t, w, &fx.decl, entry2, fx.fetch, packagefmt.TreeLimits{})
	if err != nil || root2 == root || fx.fetches != 1 {
		t.Fatalf("a carrier-only release builds its own root from the store: %v %s fetches %d", err, root2, fx.fetches)
	}
	if err := verifyRootIn(t, w, root2, &fx.decl, entry2, packagefmt.TreeLimits{}); err != nil {
		t.Fatalf("the second root verifies: %v", err)
	}
	if err := verifyRootIn(t, w, root2, &fx.decl, fx.entry, packagefmt.TreeLimits{}); err == nil || !strings.Contains(err.Error(), "another release") {
		t.Fatalf("a root is bound to its carrier: %v", err)
	}
	// Tampering after publication is refused at the next activation and at
	// the next spawn.
	if err := os.WriteFile(filepath.Join(root, "engine", "session.py"), []byte("print('evil!')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyRootIn(t, w, root, &fx.decl, fx.entry, packagefmt.TreeLimits{}); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("a tampered root must be refused: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "engine", "session.py"), []byte("print('evil, longer')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runtimeSpawnCheck(pin, root, &fx.decl, fx.entry)(); err == nil {
		t.Fatal("the per-spawn check must see a changed file")
	}
	// An extra file in the root is refused too.
	if err := os.WriteFile(filepath.Join(root, "engine", "extra.py"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyRootIn(t, w, root, &fx.decl, fx.entry, packagefmt.TreeLimits{}); err == nil || !strings.Contains(err.Error(), "not in the inventory") {
		t.Fatalf("an extra file must be refused: %v", err)
	}
}

func TestTheStoreWriterRefusesARootItCannotVerify(t *testing.T) {
	// Offline, nothing in the store: the honest refusal names itself.
	fx := newRuntimeFixture(t)
	w := openStore(t)
	_, err := storeRoot(t, w, &fx.decl, fx.entry, nil, packagefmt.TreeLimits{})
	var missing *RuntimeMissingError
	if !errors.As(err, &missing) {
		t.Fatalf("offline must be RuntimeMissingError: %v", err)
	}
	// A download that fails leaves nothing published and can resume.
	fx.failWith = errors.New("network unreachable")
	if _, err := storeRoot(t, w, &fx.decl, fx.entry, fx.fetch, packagefmt.TreeLimits{}); !errors.As(err, &missing) {
		t.Fatalf("a failed fetch must be RuntimeMissingError: %v", err)
	}
	fx.failWith = nil
	if _, err := storeRoot(t, w, &fx.decl, fx.entry, fx.fetch, packagefmt.TreeLimits{}); err != nil {
		t.Fatalf("the next attempt succeeds: %v", err)
	}
	// A declaration whose digest the archive does not meet is refused,
	// and nothing is published.
	fx2 := newRuntimeFixture(t)
	w2 := openStore(t)
	fx2.decl.SHA256 = strings.Repeat("0", 64)
	if _, err := storeRoot(t, w2, &fx2.decl, fx2.entry, fx2.fetch, packagefmt.TreeLimits{}); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("a wrong archive digest must be refused: %v", err)
	}
	if n := storeRoots(t, w2); n != 0 {
		t.Fatalf("nothing may be published after a refusal: %d roots", n)
	}
	// A declaration whose inventory digest differs from the archive's.
	fx3 := newRuntimeFixture(t)
	fx3.decl.InventorySHA256 = strings.Repeat("1", 64)
	if _, err := storeRoot(t, openStore(t), &fx3.decl, fx3.entry, fx3.fetch, packagefmt.TreeLimits{}); err == nil || !strings.Contains(err.Error(), "pinned") {
		t.Fatalf("a wrong inventory digest must be refused: %v", err)
	}
	// A carrier whose bytes do not match the bundle's digest.
	fx4 := newRuntimeFixture(t)
	fx4.entry.Bytes = []byte("not the carrier")
	if _, err := storeRoot(t, openStore(t), &fx4.decl, fx4.entry, fx4.fetch, packagefmt.TreeLimits{}); err == nil || !strings.Contains(err.Error(), "carrier") {
		t.Fatalf("a carrier off its digest must be refused: %v", err)
	}
	// The operator's limits apply: a tree past them is refused.
	fx5 := newRuntimeFixture(t)
	if _, err := storeRoot(t, openStore(t), &fx5.decl, fx5.entry, fx5.fetch, packagefmt.TreeLimits{MaxInstalledBytes: 1000}); err == nil || !strings.Contains(err.Error(), "installed bytes") {
		t.Fatalf("a tree past the ceiling must be refused: %v", err)
	}
}

// storeRoots counts the roots published in w.
func storeRoots(t *testing.T, w *plugstore.Writer) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(w.Dir, plugstore.DirRoots))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() && plugstore.ValidKey(e.Name()) {
			n++
		}
	}
	return n
}

// storePartials counts the partial trees left in w's roots.
func storePartials(t *testing.T, w *plugstore.Writer) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(w.Dir, plugstore.DirRoots))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() && strings.Contains(e.Name(), ".partial") {
			n++
		}
	}
	return n
}
