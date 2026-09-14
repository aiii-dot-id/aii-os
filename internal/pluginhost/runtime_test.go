package pluginhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

func time0() time.Time             { return time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC) }
func timeHour(n int) time.Duration { return time.Duration(n) * time.Hour }

// .
// .
// .
// .
// .
// .

type runtimeFixture struct {
	dir      string
	archive  []byte
	decl     RuntimeDecl
	entry    entrypointSpec
	inv      []byte
	fetches  int
	failWith error
}

func newRuntimeFixture(t *testing.T) *runtimeFixture { return newRuntimeFixtureWith(t, "") }

// .
// .
func newRuntimeFixtureWith(t *testing.T, salt string) *runtimeFixture {
	t.Helper()
	src := t.TempDir()
	files := map[string][]byte{
		"python/python.exe": bytes.Repeat([]byte("py"), 3000),
		"lib/libtorch.dll":  bytes.Repeat([]byte("torch"), 4000),
		"engine/session.py": []byte("print('ready')\n" + salt),
	}
	for rel, content := range files {
		p := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(rel, ".exe") {
			mode = 0o755
		}
		if err := os.WriteFile(p, content, mode); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	inv, sha, err := packagefmt.WriteTree(&buf, src, "cp1", packagefmt.TreeLimits{})
	if err != nil {
		t.Fatal(err)
	}
	invSum := sha256.Sum256(inv)
	carrier := []byte("#!carrier\n")
	csum := sha256.Sum256(carrier)
	var total int64
	for _, c := range files {
		total += int64(len(c))
	}
	return &runtimeFixture{
		dir:     filepath.Join(t.TempDir(), "plugins-runtime", "id.example.voice"),
		archive: buf.Bytes(),
		decl: RuntimeDecl{VariantID: "windows-x86_64-native", URL: "https://example.invalid/cp1.tar.gz", SHA256: strings.TrimPrefix(sha, "sha256:"),
			Size: int64(buf.Len()), InstalledBytes: total, Files: len(files), InventorySHA256: hex.EncodeToString(invSum[:])},
		entry: entrypointSpec{Name: "aii-voice.exe", Bytes: carrier, Digest: "sha256:" + hex.EncodeToString(csum[:])},
		inv:   inv,
	}
}

func (f *runtimeFixture) fetch(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
	f.fetches++
	if f.failWith != nil {
		return 0, f.failWith
	}
	if offset > int64(len(f.archive)) {
		return 0, fmt.Errorf("offset past the end")
	}
	n, err := w.Write(f.archive[offset:])
	return int64(n), err
}

func TestEnsureRuntimeFetchesVerifiesAndPublishes(t *testing.T) {
	fx := newRuntimeFixture(t)
	root, err := EnsureRuntime(context.Background(), "id.example.voice", &fx.decl, fx.dir, fx.fetch, packagefmt.TreeLimits{}, fx.entry, nil)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if filepath.Base(root) != RuntimeRootKey(fx.decl.VariantID, fx.decl.InventorySHA256, fx.entry.Name, fx.entry.Digest) || fx.fetches != 1 {
		t.Fatalf("root %s, fetches %d", root, fx.fetches)
	}
	// .
	// .
	// .
	if got, err := os.ReadFile(filepath.Join(root, "aii-voice.exe")); err != nil || !bytes.Equal(got, fx.entry.Bytes) {
		t.Fatalf("the carrier must be placed at the root: %v", err)
	}
	if _, err := os.Stat(recordPath(root)); err != nil {
		t.Fatalf("the record lives beside the root: %v", err)
	}
	for _, stray := range []string{".inventory.json", ".record.json", "inventory.json", "bin"} {
		if _, err := os.Lstat(filepath.Join(root, stray)); !os.IsNotExist(err) {
			t.Fatalf("the host adds nothing to the tree: %s is there", stray)
		}
	}
	if n := partialDirs(t, fx.dir); n != 0 {
		t.Fatalf("%d partial directories left after publication", n)
	}
	if _, err := os.Stat(filepath.Join(fx.dir, archivesDir, fx.decl.SHA256+archiveSuffix)); err != nil {
		t.Fatalf("the verified archive is kept for a carrier-only release: %v", err)
	}
	if err := VerifyRuntimeRoot(root, &fx.decl, fx.entry, packagefmt.TreeLimits{}); err != nil {
		t.Fatalf("a published root verifies: %v", err)
	}
	if err := runtimeSpawnCheck(root, &fx.decl, fx.entry)(); err != nil {
		t.Fatalf("the per-spawn check passes: %v", err)
	}
	// .
	again, err := EnsureRuntime(context.Background(), "id.example.voice", &fx.decl, fx.dir, fx.fetch, packagefmt.TreeLimits{}, fx.entry, nil)
	if err != nil || again != root || fx.fetches != 1 {
		t.Fatalf("a present root is verified, not refetched: %v %s fetches %d", err, again, fx.fetches)
	}
	// .
	// .
	// .
	carrier2 := []byte("#!carrier two\n")
	c2 := sha256.Sum256(carrier2)
	entry2 := entrypointSpec{Name: fx.entry.Name, Bytes: carrier2, Digest: "sha256:" + hex.EncodeToString(c2[:])}
	root2, err := EnsureRuntime(context.Background(), "id.example.voice", &fx.decl, fx.dir, fx.fetch, packagefmt.TreeLimits{}, entry2, nil)
	if err != nil || root2 == root || fx.fetches != 1 {
		t.Fatalf("a carrier-only release builds its own root from the cache: %v %s fetches %d", err, root2, fx.fetches)
	}
	if err := VerifyRuntimeRoot(root2, &fx.decl, entry2, packagefmt.TreeLimits{}); err != nil {
		t.Fatalf("the second root verifies: %v", err)
	}
	if err := VerifyRuntimeRoot(root2, &fx.decl, fx.entry, packagefmt.TreeLimits{}); err == nil || !strings.Contains(err.Error(), "another release") {
		t.Fatalf("a root is bound to its carrier: %v", err)
	}
	// .
	// .
	if err := os.WriteFile(filepath.Join(root, "engine", "session.py"), []byte("print('evil!')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureRuntime(context.Background(), "id.example.voice", &fx.decl, fx.dir, fx.fetch, packagefmt.TreeLimits{}, fx.entry, nil); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("a tampered root must be refused: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "engine", "session.py"), []byte("print('evil, longer')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runtimeSpawnCheck(root, &fx.decl, fx.entry)(); err == nil {
		t.Fatal("the per-spawn check must see a changed file")
	}
	// .
	if err := os.WriteFile(filepath.Join(root, "engine", "extra.py"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyRuntimeRoot(root, &fx.decl, fx.entry, packagefmt.TreeLimits{}); err == nil || !strings.Contains(err.Error(), "not in the inventory") {
		t.Fatalf("an extra file must be refused: %v", err)
	}
}

func TestEnsureRuntimeRefusesWhatItCannotVerify(t *testing.T) {
	// .
	fx := newRuntimeFixture(t)
	_, err := EnsureRuntime(context.Background(), "id.example.voice", &fx.decl, fx.dir, nil, packagefmt.TreeLimits{}, fx.entry, nil)
	var missing *RuntimeMissingError
	if !errors.As(err, &missing) {
		t.Fatalf("offline must be RuntimeMissingError: %v", err)
	}
	// .
	fx.failWith = errors.New("network unreachable")
	if _, err := EnsureRuntime(context.Background(), "id.example.voice", &fx.decl, fx.dir, fx.fetch, packagefmt.TreeLimits{}, fx.entry, nil); !errors.As(err, &missing) {
		t.Fatalf("a failed fetch must be RuntimeMissingError: %v", err)
	}
	fx.failWith = nil
	if _, err := EnsureRuntime(context.Background(), "id.example.voice", &fx.decl, fx.dir, fx.fetch, packagefmt.TreeLimits{}, fx.entry, nil); err != nil {
		t.Fatalf("the next attempt succeeds: %v", err)
	}
	// .
	// .
	fx2 := newRuntimeFixture(t)
	fx2.decl.SHA256 = strings.Repeat("0", 64)
	if _, err := EnsureRuntime(context.Background(), "id.example.voice", &fx2.decl, fx2.dir, fx2.fetch, packagefmt.TreeLimits{}, fx2.entry, nil); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("a wrong archive digest must be refused: %v", err)
	}
	if n := publishedRoots(t, fx2.dir); n != 0 {
		t.Fatalf("nothing may be published after a refusal: %d roots", n)
	}
	// .
	fx3 := newRuntimeFixture(t)
	fx3.decl.InventorySHA256 = strings.Repeat("1", 64)
	if _, err := EnsureRuntime(context.Background(), "id.example.voice", &fx3.decl, fx3.dir, fx3.fetch, packagefmt.TreeLimits{}, fx3.entry, nil); err == nil || !strings.Contains(err.Error(), "pinned") {
		t.Fatalf("a wrong inventory digest must be refused: %v", err)
	}
	// .
	fx4 := newRuntimeFixture(t)
	fx4.entry.Bytes = []byte("not the carrier")
	if _, err := EnsureRuntime(context.Background(), "id.example.voice", &fx4.decl, fx4.dir, fx4.fetch, packagefmt.TreeLimits{}, fx4.entry, nil); err == nil || !strings.Contains(err.Error(), "carrier") {
		t.Fatalf("a carrier off its digest must be refused: %v", err)
	}
	// .
	fx5 := newRuntimeFixture(t)
	if _, err := EnsureRuntime(context.Background(), "id.example.voice", &fx5.decl, fx5.dir, fx5.fetch, packagefmt.TreeLimits{MaxInstalledBytes: 1000}, fx5.entry, nil); err == nil || !strings.Contains(err.Error(), "installed bytes") {
		t.Fatalf("a tree past the ceiling must be refused: %v", err)
	}
}

// .
func publishedRoots(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() && reHex64.MatchString(e.Name()) {
			n++
		}
	}
	return n
}

// .
// .
func partialDirs(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
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

func TestRuntimeRootsPinAndRetire(t *testing.T) {
	dir := t.TempDir()
	mk := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.Mkdir(p, 0o700); err != nil {
			t.Fatal(err)
		}
		return p
	}
	old := mk(strings.Repeat("a", 64))
	mid := mk(strings.Repeat("b", 64))
	newest := mk(strings.Repeat("c", 64))
	mk("not-a-root")
	// .
	// .
	for root, archive := range map[string]string{old: "x", mid: "y", newest: "z", filepath.Join(dir, strings.Repeat("d", 64)): "w"} {
		raw, _ := json.Marshal(rootRecord{ArchiveSHA256: archive})
		if err := os.WriteFile(recordPath(root), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, archivesDir), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"x", "y", "z", "w"} {
		if err := os.WriteFile(filepath.Join(dir, archivesDir, a+archiveSuffix), []byte(a), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// .
	for i, p := range []string{old, mid, newest} {
		at := time0().Add(timeHour(i))
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	roots := NewRuntimeRoots()
	roots.Pin(old)
	removed, err := roots.Retire(dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != mid {
		t.Fatalf("keep the newest, never the pinned: removed %v", removed)
	}
	if _, err := os.Stat(recordPath(mid)); !os.IsNotExist(err) {
		t.Fatal("a retired root's record goes with it")
	}
	if _, err := os.Stat(recordPath(filepath.Join(dir, strings.Repeat("d", 64)))); !os.IsNotExist(err) {
		t.Fatal("an orphan record is pruned")
	}
	for a, want := range map[string]bool{"x": true, "y": false, "z": true, "w": false} {
		_, err := os.Stat(filepath.Join(dir, archivesDir, a+archiveSuffix))
		if (err == nil) != want {
			t.Fatalf("archive %s kept=%v, want %v", a, err == nil, want)
		}
	}
	roots.Release(old)
	if roots.Refs(old) != 0 {
		t.Fatal("release must count down")
	}
	removed, _ = roots.Retire(dir, 1)
	if len(removed) != 1 || removed[0] != old {
		t.Fatalf("an unpinned old root retires: %v", removed)
	}
	if _, err := os.Stat(filepath.Join(dir, "not-a-root")); err != nil {
		t.Fatal("a directory that is not a root is left alone")
	}
}

func TestParseRuntimesHoldsTheBounds(t *testing.T) {
	good := `{"runtimes":[{"variant_id":"win","url":"https://x/y.tar.gz","sha256":"` + strings.Repeat("a", 64) + `","size":1,"installed_bytes":2,"files":3,"inventory_sha256":"` + strings.Repeat("b", 64) + `"}]}`
	if _, err := ParseRuntimes([]byte(good)); err != nil {
		t.Fatalf("good: %v", err)
	}
	for name, bad := range map[string]string{
		"http url":      strings.Replace(good, "https://", "http://", 1),
		"short digest":  strings.Replace(good, strings.Repeat("a", 64), "abc", 1),
		"zero size":     strings.Replace(good, `"size":1`, `"size":0`, 1),
		"unknown field": strings.Replace(good, `"files":3`, `"files":3,"exec":true`, 1),
		"empty":         `{"runtimes":[]}`,
	} {
		if _, err := ParseRuntimes([]byte(bad)); err == nil {
			t.Fatalf("%s must be refused", name)
		}
	}
}
