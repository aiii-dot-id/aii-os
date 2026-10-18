//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

// rootDigestHex is the whole digest a root's name is cut from.
func rootDigestHex(d RuntimeDecl, entry entrypointSpec) string {
	sum := sha256.Sum256([]byte("aii-runtime-root\n" + d.VariantID + "\n" + d.InventorySHA256 + "\n" + entry.Name + "\n" + entry.Digest + "\n"))
	return hex.EncodeToString(sum[:])
}

// partialWatch notes the partial trees beside the roots each time the
// install asks its context whether to go on.
type partialWatch struct {
	context.Context
	dir  string
	seen map[string]bool
}

func (c *partialWatch) Err() error {
	found, _ := filepath.Glob(filepath.Join(c.dir, "*.partial-*"))
	for _, p := range found {
		c.seen[filepath.Base(p)] = true
	}
	return c.Context.Err()
}

func TestANewRootIsNamedBySixteenDigitsOfItsDigest(t *testing.T) {
	fx := newRuntimeFixture(t)
	w := openStore(t)
	short := rootDigestHex(fx.decl, fx.entry)[:16]
	if plugstore.RootKey(fx.decl.VariantID, fx.decl.InventorySHA256, fx.entry.Name, fx.entry.Digest) != short {
		t.Fatal("the store's root key is not the first 16 digits of the root's digest")
	}
	roots := filepath.Join(w.Dir, plugstore.DirRoots)
	watch := &partialWatch{Context: t.Context(), dir: roots, seen: map[string]bool{}}
	if err := EnsureEntry(watch, w, rootWork(t, &fx.decl, fx.entry, packagefmt.TreeLimits{}), fx.fetch, Adoption{}, nil); err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(watch.seen) == 0 {
		t.Fatal("the install was never seen building a partial tree")
	}
	for name := range watch.seen {
		if !strings.HasPrefix(name, short+".partial-") {
			t.Fatalf("the partial tree %s is not named for its root %s", name, short)
		}
	}
	entries, err := os.ReadDir(roots)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	want := []string{short, short + plugstore.RecordSuffix}
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Fatalf("the store's roots hold %v, want %v", names, want)
	}
}

// A COLLISION OF THE 16 DIGITS IS REFUSED, NEVER SERVED. Two releases whose
// digests begin alike ask for one root; the record beside it names the
// release that published it, and the reader holds that record to the
// release asking. Planted: release A's tree and record under the key
// release B's digest begins with. B's activation is refused, typed, as the
// store's tree and not its package's, with nothing fetched, and A's tree is
// left as it is.
func TestARootUnderAnotherReleasesNameIsRefusedNeverServed(t *testing.T) {
	a := newRuntimeFixtureWith(t, "# release a\n")
	b := newRuntimeFixtureWith(t, "# release b\n")
	w := openStore(t)
	rootA, err := storeRoot(t, w, &a.decl, a.entry, a.fetch, packagefmt.TreeLimits{})
	if err != nil {
		t.Fatal(err)
	}
	planted, _ := w.Root(rootDigestHex(b.decl, b.entry)[:16])
	for from, to := range map[string]string{rootA: planted, rootA + plugstore.RecordSuffix: planted + plugstore.RecordSuffix} {
		if err := os.Rename(from, to); err != nil {
			t.Fatal(err)
		}
	}
	got, held, err := runtimeMaterial(t, w, "id.example.voice", "1", b).verify(t.Context())
	held.release()
	var refused *RuntimeError
	if got != "" || b.fetches != 0 || !errors.As(err, &refused) || refused.WaitsOnPackage() || acquirable(err) || !strings.Contains(err.Error(), "another release") {
		t.Fatalf("release B was given %q (%d fetches) under a record naming release A: %v", got, b.fetches, err)
	}
	if body, err := os.ReadFile(filepath.Join(planted, "engine", "session.py")); err != nil || !strings.HasSuffix(string(body), "# release a\n") {
		t.Fatalf("the other release's tree is not the asker's to change: %q %v", body, err)
	}
}
