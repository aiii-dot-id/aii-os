//go:build (linux && !android) || (darwin && !ios)

package pluginhost

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/install"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

// wallStore is a store holding two plugins' material, as its writer
// leaves it: each a view linking one blob, and a root with one file.
type wallStore struct {
	dir                                string
	ownView, ownRoot                   string
	ownModel, ownRootFile, ownBlob     string
	otherModel, otherRootFile, otherBl string
	otherView                          string
}

func storeForWalls(t *testing.T) wallStore {
	t.Helper()
	w := openStore(t)
	dir, err := filepath.EvalSymlinks(w.Dir)
	if err != nil {
		t.Fatal(err)
	}
	serve := func(body []byte) ModelFetcher {
		return func(_ context.Context, _ string, offset int64, out io.Writer) (int64, error) {
			n, err := out.Write(body[offset:])
			return int64(n), err
		}
	}
	view := func(name string, body []byte) (string, string, string) {
		d := declOf(name, "", body)
		work := viewWork([]string{name}, d)
		if err := EnsureEntry(t.Context(), w, work, serve(body), Adoption{}, nil); err != nil {
			t.Fatal(err)
		}
		return filepath.Join(dir, work.Entry), filepath.Join(dir, work.Entry, name), filepath.Join(dir, plugstore.DirBlobs, d.SHA256)
	}
	root := func(key string) (string, string) {
		r := filepath.Join(dir, plugstore.DirRoots, key)
		if err := os.MkdirAll(filepath.Join(r, "lib"), 0o700); err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(r, "lib", "engine.so")
		if err := os.WriteFile(f, []byte("code"), 0o500); err != nil {
			t.Fatal(err)
		}
		return r, f
	}
	var s wallStore
	s.dir = dir
	s.ownView, s.ownModel, s.ownBlob = view("own.bin", []byte("this plugin's own model"))
	s.otherView, s.otherModel, s.otherBl = view("other.bin", []byte("another plugin's model"))
	s.ownRoot, s.ownRootFile = root("0123456789abcdef")
	_, s.otherRootFile = root("fedcba9876543210")
	return s
}

// said is what the probe answered for key ("read <path>", "names <dir>").
func said(out, key string) string {
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(line, key+"="); ok {
			return v
		}
	}
	return "(not asked)"
}

func TestAContainedChildReadsItsOwnMaterialAndNothingElseOfTheStore(t *testing.T) {
	requireWall(t)
	s := storeForWalls(t)
	reads := map[string]bool{s.ownModel: true, s.ownRootFile: true, s.otherModel: false, s.otherRootFile: false, s.ownBlob: false, s.otherBl: false}
	asks := []string{fakechildBin, "reach"}
	for p := range reads {
		asks = append(asks, "read="+p)
	}
	blobs, views := filepath.Join(s.dir, plugstore.DirBlobs), filepath.Join(s.dir, plugstore.DirViews)
	asks = append(asks, "names="+s.dir, "names="+blobs, "names="+views)
	out := runWallProbe(t, asks)
	for p := range reads {
		if said(out, "read "+p) != "read" {
			t.Fatalf("rig: uncontained, every read must succeed or its refusal below proves nothing (%s):\n%s", p, out)
		}
	}
	places := []*firewall.Rule{{ID: "sub.store", Kind: firewall.KindSubstrate, Path: s.dir, Enforced: true}}
	argv, wall, err := containArgv(context.Background(), asks, nil, nativeFiles{places: places, read: []string{s.ownView, s.ownRoot}, own: []string{s.ownRoot}})
	if err != nil {
		t.Fatalf("containment refused on a host that can build it: %v", err)
	}
	out = runContained(t, argv, wall)
	for p, mine := range reads {
		got := said(out, "read "+p)
		if mine && got != "read" {
			t.Errorf("the child could not read its own %s: %s", p, got)
		}
		if !mine && !strings.HasPrefix(got, "refused") {
			t.Errorf("the child read %s, which is not its own: %s", p, got)
		}
	}
	if got := said(out, "names "+blobs); !strings.HasPrefix(got, "refused") {
		t.Errorf("the child listed the store's blobs: %s", got)
	}
	switch runtime.GOOS {
	case "darwin":
		for _, d := range []string{s.dir, views} {
			if got := said(out, "names "+d); !strings.HasPrefix(got, "refused") {
				t.Errorf("the child listed %s: %s", d, got)
			}
		}
	case "linux":
		// The mask is an empty directory with the child's own two bound
		// back into it: the names of their parents are all it shows.
		for d, allowed := range map[string][]string{s.dir: {plugstore.DirRoots, plugstore.DirViews}, views: {filepath.Base(s.ownView)}} {
			got := said(out, "names "+d)
			if strings.HasPrefix(got, "refused") {
				continue
			}
			for _, n := range strings.Split(got, ",") {
				if n != "" && !contains(allowed, n) {
					t.Errorf("listing %s inside the wall shows %q: %s", d, n, got)
				}
			}
		}
	}
	t.Logf("inside the wall: %s", strings.Join(strings.Fields(out), " "))
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ANOTHER IDENTITY'S SLOT IS A PLACE LIKE ANY OTHER (H6): named among the
// masked places, a native child cannot read the keys in it, nor list it.
func TestAContainedChildIsRefusedAnotherIdentitysSlot(t *testing.T) {
	requireWall(t)
	other := filepath.Join(t.TempDir(), "identity-b")
	if err := os.MkdirAll(filepath.Join(other, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	other, _ = filepath.EvalSymlinks(other)
	key := filepath.Join(other, "data", "identity.sec")
	if err := os.WriteFile(key, []byte("another identity's key"), 0o600); err != nil {
		t.Fatal(err)
	}
	asks := []string{fakechildBin, "reach", "read=" + key, "names=" + filepath.Join(other, "data")}
	out := runWallProbe(t, asks)
	if said(out, "read "+key) != "read" {
		t.Fatalf("rig: uncontained, the key must read or its refusal below proves nothing:\n%s", out)
	}
	places := []*firewall.Rule{{ID: "sub.identity", Kind: firewall.KindSubstrate, Path: other, Enforced: true}}
	argv, wall, err := containArgv(context.Background(), asks, nil, nativeFiles{places: places})
	if err != nil {
		t.Fatalf("containment refused on a host that can build it: %v", err)
	}
	out = runContained(t, argv, wall)
	if got := said(out, "read "+key); !strings.HasPrefix(got, "refused") {
		t.Errorf("a native child read another identity's key: %s", got)
	}
	if got := said(out, "names "+filepath.Join(other, "data")); !strings.HasPrefix(got, "refused") && got != "" {
		t.Errorf("a native child listed another identity's data: %s", got)
	}
}

func TestAContainedChildIsRefusedAnUnrecordedSiblingSlot(t *testing.T) {
	requireWall(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	own := filepath.Join(root, "identity-0")
	near := filepath.Join(root, "identity-1")
	volume, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	far := filepath.Join(volume, "slot")
	for _, d := range []string{own, filepath.Join(near, "data"), filepath.Join(far, "data")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	linked := filepath.Join(root, "identity-2")
	if err := os.Symlink(far, linked); err != nil {
		t.Fatal(err)
	}
	keys := []string{filepath.Join(near, "data", "identity.sec"), filepath.Join(far, "data", "identity.sec")}
	for _, k := range keys {
		if err := os.WriteFile(k, []byte("a sibling identity's key"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	keys = append(keys, filepath.Join(linked, "data", "identity.sec")) // the far key, through the link
	asks := []string{fakechildBin, "reach"}
	for _, k := range keys {
		asks = append(asks, "read="+k)
	}
	out := runWallProbe(t, asks)
	for _, k := range keys {
		if said(out, "read "+k) != "read" {
			t.Fatalf("rig: uncontained, %s must read or its refusal below proves nothing:\n%s", k, out)
		}
	}
	siblings, err := install.SiblingSlots(own)
	if err != nil {
		t.Fatal(err)
	}
	var places []*firewall.Rule
	for _, s := range siblings {
		places = append(places, &firewall.Rule{ID: "sub.identity", Kind: firewall.KindSubstrate, Path: firewall.ResolvePath(s), Enforced: true})
	}
	if len(places) != 2 {
		t.Fatalf("the siblings of %s are %v, want identity-1 and identity-2", own, siblings)
	}
	argv, wall, err := containArgv(context.Background(), asks, nil, nativeFiles{places: places})
	if err != nil {
		t.Fatalf("containment refused on a host that can build it: %v", err)
	}
	out = runContained(t, argv, wall)
	for _, k := range keys {
		if got := said(out, "read "+k); !strings.HasPrefix(got, "refused") {
			t.Errorf("a native child read %s, a sibling identity's key: %s", k, got)
		}
	}
}

// SEATBELT JUDGES A HARD-LINKED BLOB BY THE PATH THE CHILD OPENS (Q4's
// question, measured). A view's file is a hard link to its blob, and a
// file with several names may be judged by another of them. While another
// process opens the same blob, again and again, through b/ and through a
// second view the child may not read, the child reads it through its own
// view two hundred times: every read must succeed. (Were Seatbelt to judge
// by the name another process last used, views on APFS would be clonefile
// copies instead — Q4.)
func TestTheChildReadsAHardLinkedBlobThroughItsOwnViewWhateverElseOpensIt(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Seatbelt's question; Linux binds the view back by its own path")
	}
	requireWall(t)
	s := storeForWalls(t)
	second := filepath.Join(s.dir, plugstore.DirViews, "aaaaaaaaaaaaaaaa")
	if err := os.MkdirAll(second, 0o700); err != nil {
		t.Fatal(err)
	}
	secondName := filepath.Join(second, "own.bin")
	if err := os.Link(s.ownBlob, secondName); err != nil {
		t.Fatal(err)
	}
	asks := []string{fakechildBin, "reach", "readloop=200:" + s.ownModel}
	places := []*firewall.Rule{{ID: "sub.store", Kind: firewall.KindSubstrate, Path: s.dir, Enforced: true}}
	argv, wall, err := containArgv(context.Background(), asks, nil, nativeFiles{places: places, read: []string{s.ownView, s.ownRoot}, own: []string{s.ownRoot}})
	if err != nil {
		t.Fatalf("containment refused on a host that can build it: %v", err)
	}
	stop := make(chan struct{})
	var opened atomic.Int64
	var wg sync.WaitGroup
	for _, name := range []string{s.ownBlob, secondName} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := os.ReadFile(name); err == nil {
					opened.Add(1)
				}
			}
		}()
	}
	out := runContained(t, argv, wall)
	close(stop)
	wg.Wait()
	if got := said(out, "readloop"); got != "200 read" {
		t.Fatalf("the child's own view, while %d opens came through the blob's other names: %s", opened.Load(), got)
	}
	if opened.Load() == 0 {
		t.Fatal("rig: nothing opened the blob through its other names meanwhile")
	}
	t.Logf("200 reads through the child's own view while the blob was opened %d times through b/ and another view", opened.Load())
}
