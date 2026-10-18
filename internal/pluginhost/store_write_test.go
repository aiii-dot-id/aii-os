//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

// storeFetch serves declared bytes by URL and counts what it was asked.
type storeFetch struct {
	mu    sync.Mutex
	files map[string][]byte
	asked map[string]int
}

func newStoreFetch() *storeFetch {
	return &storeFetch{files: map[string][]byte{}, asked: map[string]int{}}
}

func (f *storeFetch) serve(url string, b []byte) { f.files[url] = b }

func (f *storeFetch) fetch(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
	f.mu.Lock()
	f.asked[url]++
	b, ok := f.files[url]
	f.mu.Unlock()
	if !ok {
		return 0, &FetchStatusError{Status: 404}
	}
	n, err := w.Write(b[offset:])
	return int64(n), err
}

func (f *storeFetch) count(url string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.asked[url]
}

func (f *storeFetch) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.asked {
		n += c
	}
	return n
}

// declOf is a model declaration of these bytes at this path.
func declOf(name, path string, b []byte) ModelDecl {
	sum := sha256.Sum256(b)
	return ModelDecl{Name: name, Path: path, URL: "https://example.invalid/" + name, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(b))}
}

const modelsDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

func viewWork(names []string, models ...ModelDecl) StoreWork {
	key := plugstore.ViewKey(modelsDigest, names)
	return StoreWork{Entry: "v/" + key, View: &ViewWork{ModelsSHA256: modelsDigest, Names: names, Models: models}}
}

func openStore(t *testing.T) *plugstore.Writer {
	t.Helper()
	w, err := plugstore.OpenWriter(filepath.Join(t.TempDir(), "plugins"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		w.Close()
		noPinLeft(t, w.Dir)
	})
	return w
}

// noPinLeft fails the test if an activation's pin on the store at dir is
// still open once everything the test started has stopped: a pin never let
// go keeps its record from being removed on Windows.
func noPinLeft(t *testing.T, dir string) {
	t.Helper()
	for path, n := range plugstore.HeldForTest(dir) {
		t.Errorf("%d pin(s) still hold %s after the test's activations ended", n, path)
	}
}

func fileSum(t *testing.T, path string) string {
	t.Helper()
	sum, _, err := hashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sum
}

// ADOPTION COPIES, NEVER LINKS (design v2.9: a write through a link between
// a slot and the store would corrupt the store's entry). The slot's own
// verified copy is brought in by digest with no download; the store's blob
// is not the slot's file, the slot's file gains no link, and a write to
// the slot's file afterwards leaves the store's bytes as they were. The
// view's file IS its blob: one file, two names.
func TestAdoptionCopiesNeverLinksAndTheViewIsItsBlob(t *testing.T) {
	w := openStore(t)
	bytesOf := bytes.Repeat([]byte("weights"), 4096)
	d := declOf("stt", "stt/model.bin", bytesOf)
	slot := filepath.Join(t.TempDir(), "plugins-models", ".by-id", "abc")
	src := modelFile(slot, d)
	if err := os.MkdirAll(filepath.Dir(src), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, bytesOf, 0o600); err != nil {
		t.Fatal(err)
	}
	linksBefore := linkCount(t, src)
	work := viewWork([]string{"stt"}, d)
	if err := EnsureEntry(context.Background(), w, work, nil, Adoption{ModelDirs: []string{slot}}, nil); err != nil {
		t.Fatalf("adoption with no download path: %v", err)
	}
	blob, _ := w.Blob(d.SHA256)
	sfi, _ := os.Stat(src)
	bfi, err := os.Stat(blob)
	if err != nil {
		t.Fatalf("the blob was not published: %v", err)
	}
	if os.SameFile(sfi, bfi) {
		t.Fatal("the store's blob is the slot's own file: adoption linked instead of copying")
	}
	if got := linkCount(t, src); got != linksBefore {
		t.Fatalf("the slot's file went from %d links to %d", linksBefore, got)
	}
	if err := os.WriteFile(src, bytes.Repeat([]byte("X"), len(bytesOf)), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := fileSum(t, blob); got != d.SHA256 {
		t.Fatalf("a write to the slot's file changed the store's blob: %s", got)
	}
	view, _ := w.View(plugstore.ViewKey(modelsDigest, []string{"stt"}))
	vfi, err := os.Stat(modelFile(view, d))
	if err != nil || !os.SameFile(vfi, bfi) {
		t.Fatalf("the view's file is not its blob: %v", err)
	}
	record, _ := w.ViewRecord(plugstore.ViewKey(modelsDigest, []string{"stt"}))
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("the view has no record: %v", err)
	}
	if _, err := plugstore.DecodeViewRecord(record, raw); err != nil {
		t.Fatalf("the view's record does not read: %v", err)
	}
	if !EntryPresent(w, work) {
		t.Fatal("a published view is not present")
	}
	if left, _ := filepath.Glob(filepath.Join(w.Dir, "b", "*.partial")); len(left) != 0 {
		t.Fatalf("partials left: %v", left)
	}
}

func TestAWrongCandidateIsDiscardedAndTheFileFetched(t *testing.T) {
	w := openStore(t)
	good := bytes.Repeat([]byte("w"), 3000)
	d := declOf("tts", "tts.bin", good)
	slot := t.TempDir()
	wrong := bytes.Repeat([]byte("z"), len(good))
	if err := os.WriteFile(modelFile(slot, d), wrong, 0o600); err != nil {
		t.Fatal(err)
	}
	f := newStoreFetch()
	f.serve(d.URL, good)
	if err := EnsureEntry(context.Background(), w, viewWork([]string{"tts"}, d), f.fetch, Adoption{ModelDirs: []string{slot}}, nil); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if n := f.count(d.URL); n != 1 {
		t.Fatalf("the model was fetched %d times; once is expected", n)
	}
	blob, _ := w.Blob(d.SHA256)
	if got := fileSum(t, blob); got != d.SHA256 {
		t.Fatalf("the blob holds %s", got)
	}
	if got, _ := os.ReadFile(modelFile(slot, d)); !bytes.Equal(got, wrong) {
		t.Fatal("the candidate was changed")
	}
}

// TWO VIEWS, ONE BLOB, ONE FETCH: the second view links what the first
// brought, and both of its names are the one file.
func TestTwoViewsShareOneBlobAndOneFetch(t *testing.T) {
	w := openStore(t)
	shared := bytes.Repeat([]byte("s"), 2048)
	d := declOf("vad", "vad.bin", shared)
	other := declOf("stt", "stt.bin", []byte("stt bytes"))
	f := newStoreFetch()
	f.serve(d.URL, shared)
	f.serve(other.URL, []byte("stt bytes"))
	a := viewWork([]string{"vad"}, d)
	b := viewWork([]string{"vad", "stt"}, d, other)
	for _, work := range []StoreWork{a, b} {
		if err := EnsureEntry(context.Background(), w, work, f.fetch, Adoption{}, nil); err != nil {
			t.Fatalf("ensure %s: %v", work.Entry, err)
		}
	}
	if n := f.count(d.URL); n != 1 {
		t.Fatalf("the shared model was fetched %d times", n)
	}
	va, _ := w.View(plugstore.ViewKey(modelsDigest, a.View.Names))
	vb, _ := w.View(plugstore.ViewKey(modelsDigest, b.View.Names))
	fa, _ := os.Stat(modelFile(va, d))
	fb, _ := os.Stat(modelFile(vb, d))
	if fa == nil || fb == nil || !os.SameFile(fa, fb) {
		t.Fatal("two views of one blob hold two files")
	}
}

// A WORK THAT DOES NOT MAKE ITS OWN KEY IS REFUSED, before anything is
// touched: a view whose models are not the ones its key names, a model
// whose path climbs out, a root whose carrier is not the one its key was
// made of.
func TestAWorkIsHeldToItsOwnKey(t *testing.T) {
	w := openStore(t)
	d := declOf("stt", "stt.bin", []byte("x"))
	work := viewWork([]string{"stt"}, d)
	work.View.Names = []string{"stt", "tts"}
	if _, err := work.Check(); err == nil {
		t.Error("a view whose names make another key was admitted")
	}
	bad := d
	bad.Path = "../escape.bin"
	if err := EnsureEntry(context.Background(), w, viewWork([]string{"stt"}, bad), nil, Adoption{}, nil); err == nil || !materialWaitsOnPackage(err) {
		t.Errorf("a model path that climbs out: want a refusal that waits on the package, got %v", err)
	}
	fx := newRuntimeFixture(t)
	rw := RootWork{Runtime: fx.decl, Package: filepath.Join(t.TempDir(), "x.aiiospkg"), Member: "variants/x/child", Name: fx.entry.Name, Digest: fx.entry.Digest}
	key := plugstore.RootKey(fx.decl.VariantID, fx.decl.InventorySHA256, fx.entry.Name, fx.entry.Digest)
	if _, err := (StoreWork{Entry: "r/" + key, Root: &rw}).Check(); err != nil {
		t.Fatalf("a root's own work: %v", err)
	}
	rw.Digest = "sha256:" + d.SHA256
	if _, err := (StoreWork{Entry: "r/" + key, Root: &rw}).Check(); err == nil {
		t.Error("a root whose carrier makes another key was admitted")
	}
	if _, err := (StoreWork{Entry: "b/" + d.SHA256}).Check(); err == nil {
		t.Error("a blob asked for by itself was admitted")
	}
}

func TestARootIsPublishedFromAnAdoptedArchiveAndTheAskersCarrier(t *testing.T) {
	skipWhereNativeIsRefused(t)
	w := openStore(t)
	fx := newRuntimeFixture(t)
	fx.decl.VariantID = hostVariantID()
	_, role := platformRootsForTest(t)
	pkg := nativePackage(t, "id.example.store", fx.entry.Bytes, role, nil)
	cache := filepath.Join(t.TempDir(), "archives")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, fx.decl.SHA256+archiveSuffix), fx.archive, 0o600); err != nil {
		t.Fatal(err)
	}
	key := plugstore.RootKey(fx.decl.VariantID, fx.decl.InventorySHA256, fx.entry.Name, fx.entry.Digest)
	work := StoreWork{Entry: "r/" + key, Root: &RootWork{Runtime: fx.decl, Package: pkg, Member: "variants/" + hostVariantID() + "/child", Name: fx.entry.Name, Digest: fx.entry.Digest}}
	f := newStoreFetch()
	if err := EnsureEntry(context.Background(), w, work, f.fetch, Adoption{ArchiveDirs: []string{cache}}, nil); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if n := f.total(); n != 0 {
		t.Fatalf("%d downloads for an archive the slot held", n)
	}
	root, _ := w.Root(key)
	record, _ := w.RootRecord(key)
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifyStoreRoot(context.Background(), root, raw, nil, &fx.decl, fx.entry, packagefmt.TreeLimits{}); err != nil {
		t.Fatalf("the published root does not verify: %v", err)
	}
	if !EntryPresent(w, work) {
		t.Fatal("a published root is not present")
	}
	if err := EnsureEntry(context.Background(), w, work, f.fetch, Adoption{}, nil); err != nil || f.total() != 0 {
		t.Fatalf("asked again: %v, %d downloads", err, f.total())
	}
	work.Root.Digest = "sha256:" + fx.decl.SHA256
	work.Entry = "r/" + plugstore.RootKey(fx.decl.VariantID, fx.decl.InventorySHA256, fx.entry.Name, work.Root.Digest)
	if err := EnsureEntry(context.Background(), w, work, f.fetch, Adoption{}, nil); err == nil {
		t.Fatal("a carrier whose bytes are not its digest was placed")
	}
}

// A REPAIR WAITS FOR EVERY ACTIVATION THAT HOLDS WHAT IT WOULD REPLACE, then
// replaces the damaged blob everywhere it is linked (H1): one view of the
// blob pinned by another identity's activation keeps the repair out; once
// it lets go, the model is fetched once, and every view's file and the
// blob are the new bytes.
func TestARepairSkipsAHeldEntryThenRelinksEveryView(t *testing.T) {
	w := openStore(t)
	good := bytes.Repeat([]byte("m"), 4096)
	d := declOf("stt", "stt.bin", good)
	f := newStoreFetch()
	f.serve(d.URL, good)
	a := viewWork([]string{"stt"}, d)
	b := viewWork([]string{"stt", "other"}, d)
	for _, work := range []StoreWork{a, b} {
		if err := EnsureEntry(context.Background(), w, work, f.fetch, Adoption{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	blob, _ := w.Blob(d.SHA256)
	flipByte(t, blob)
	keyB := plugstore.ViewKey(modelsDigest, b.View.Names)
	pin, err := w.PinView(keyB)
	if err != nil {
		t.Fatal(err)
	}
	if err := RepairEntry(context.Background(), w, a, f.fetch, Adoption{}, nil); !errors.Is(err, ErrRepairWaits) {
		pin.Close()
		t.Fatalf("a repair while another view of the blob is pinned: want ErrRepairWaits, got %v", err)
	}
	if n := f.count(d.URL); n != 1 {
		t.Fatalf("a waiting repair fetched (%d fetches)", n)
	}
	pin.Close()
	if err := RepairEntry(context.Background(), w, a, f.fetch, Adoption{}, nil); err != nil {
		t.Fatalf("repair: %v", err)
	}
	if n := f.count(d.URL); n != 2 {
		t.Fatalf("the repair fetched %d times; once is expected", n-1)
	}
	bfi, _ := os.Stat(blob)
	for _, work := range []StoreWork{a, b} {
		view, _ := w.View(plugstore.ViewKey(modelsDigest, work.View.Names))
		path := modelFile(view, d)
		if got := fileSum(t, path); got != d.SHA256 {
			t.Fatalf("%s still holds the damaged bytes", path)
		}
		if fi, _ := os.Stat(path); !os.SameFile(fi, bfi) {
			t.Fatalf("%s is not the repaired blob", path)
		}
	}
	// A view that verifies is left: nothing more is fetched.
	if err := RepairEntry(context.Background(), w, b, f.fetch, Adoption{}, nil); err != nil || f.count(d.URL) != 2 {
		t.Fatalf("a repair of a sound view: %v, %d fetches", err, f.count(d.URL))
	}
}

// A DAMAGED ROOT IS MOVED ASIDE AND BUILT AGAIN FROM ITS ARCHIVE, with no
// download.
func TestADamagedRootIsRebuiltFromItsArchive(t *testing.T) {
	skipWhereNativeIsRefused(t)
	w := openStore(t)
	fx := newRuntimeFixture(t)
	fx.decl.VariantID = hostVariantID()
	_, role := platformRootsForTest(t)
	pkg := nativePackage(t, "id.example.store", fx.entry.Bytes, role, nil)
	key := plugstore.RootKey(fx.decl.VariantID, fx.decl.InventorySHA256, fx.entry.Name, fx.entry.Digest)
	work := StoreWork{Entry: "r/" + key, Root: &RootWork{Runtime: fx.decl, Package: pkg, Member: "variants/" + hostVariantID() + "/child", Name: fx.entry.Name, Digest: fx.entry.Digest}}
	f := newStoreFetch()
	f.serve(fx.decl.URL, fx.archive)
	if err := EnsureEntry(context.Background(), w, work, f.fetch, Adoption{}, nil); err != nil {
		t.Fatal(err)
	}
	root, _ := w.Root(key)
	victim := filepath.Join(root, "engine", "session.py")
	if err := os.WriteFile(victim, []byte("print('tampered')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RepairEntry(context.Background(), w, work, f.fetch, Adoption{}, nil); err != nil {
		t.Fatalf("repair: %v", err)
	}
	if n := f.count(fx.decl.URL); n != 1 {
		t.Fatalf("the rebuild downloaded (%d fetches)", n)
	}
	record, _ := w.RootRecord(key)
	raw, _ := os.ReadFile(record)
	if _, err := verifyStoreRoot(context.Background(), root, raw, nil, &fx.decl, fx.entry, packagefmt.TreeLimits{}); err != nil {
		t.Fatalf("the rebuilt root does not verify: %v", err)
	}
}

// flipByte changes one byte of a published (read-only) file in place, as a
// failing disk would.
func flipByte(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var one [1]byte
	if _, err := f.ReadAt(one[:], 7); err != nil {
		t.Fatal(err)
	}
	one[0] ^= 0xff
	if _, err := f.WriteAt(one[:], 7); err != nil {
		t.Fatal(err)
	}
}

// linkCount is how many names a file has.
func linkCount(t *testing.T, path string) uint64 {
	t.Helper()
	n, err := nlink(path)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
