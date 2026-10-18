//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestAnActivationReadsAReadOnlyStoreAndChangesNothing(t *testing.T) {
	const id = "org.example.readonly-store"
	rig := newNativeRig(t, id, "ready-real", 20000, nil)
	ap, err := Activate(context.Background(), rig.pkg, newRegistry(t), rig.opts)
	if err != nil {
		t.Fatalf("the activation that populates the store: %v", err)
	}
	if err := ap.Deactivate(context.Background()); err != nil {
		t.Fatal(err)
	}
	readOnly(t, rig.store.Dir)
	before := treeHash(t, rig.store.Dir)
	opts := *rig.opts
	opts.Materialize = nil // nothing may be asked of the writer: everything is there
	ap, err = Activate(context.Background(), rig.pkg, newRegistry(t), &opts)
	if err != nil {
		t.Fatalf("an activation from a read-only store: %v", err)
	}
	if !rig.pinned(t) {
		t.Fatal("the running activation does not hold its root")
	}
	if err := ap.Deactivate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if after := treeHash(t, rig.store.Dir); after != before {
		t.Fatal("the store's tree changed under an activation")
	}
}

// A DAMAGED BLOB IS REFUSED, LEFT IN PLACE, AND REPAIRED ON REPORT (H1). One
// byte of a model's blob changes on disk. The activation refuses the view,
// typed as damage, and writes nothing: the bytes are as they were. Asked of
// the store's writer, the report is one ask naming the view; the writer
// fetches the model once more, and the next verification passes.
func TestADamagedBlobIsRefusedLeftInPlaceAndRepairedOnReport(t *testing.T) {
	w := openStore(t)
	data := []byte(strings.Repeat("model bytes ", 400))
	d := declOf("stt", "stt/model.bin", data)
	f := newStoreFetch()
	f.serve(d.URL, data)
	m := modelMaterial(w, "org.example.damaged", "1", d)
	if err := materializeInProcess(t.Context(), w, m, f.fetch); err != nil {
		t.Fatal(err)
	}
	blob, _ := w.Blob(d.SHA256)
	flipByte(t, blob)
	damaged, _ := os.ReadFile(blob)

	var damage *MaterialDamageError
	_, err := Acquire(t.Context(), m, &Options{})
	if !errors.As(err, &damage) || damage.Entry != "v/"+m.viewKey() || !acquirable(err) || materialWaitsOnPackage(err) {
		t.Fatalf("a damaged blob: want damage of the view, for the writer to mend, got %v", err)
	}
	if now, _ := os.ReadFile(blob); string(now) != string(damaged) {
		t.Fatal("the activation changed the damaged blob")
	}
	bfi, _ := os.Stat(blob)
	if vfi, err := os.Stat(modelFile(m.ModelsDir, d)); err != nil || !os.SameFile(vfi, bfi) {
		t.Fatalf("the activation took the damaged model out of its view: %v", err)
	}
	var asks [][]string
	opts := &Options{Materialize: func(ctx context.Context, m Material) error {
		asks = append(asks, m.Damaged())
		return materializeInProcess(ctx, w, m, f.fetch)
	}}
	if _, err := Acquire(t.Context(), m, opts); err != nil {
		t.Fatalf("after the report: %v", err)
	}
	if len(asks) != 1 || !slices.Equal(asks[0], []string{"v/" + m.viewKey()}) || f.count(d.URL) != 2 {
		t.Fatalf("one ask naming the view, one more fetch: asks %v, fetches %d", asks, f.count(d.URL))
	}
}

func TestTheStoreCountsWhatItHoldsAndTheSlotsCopyIsNotADownload(t *testing.T) {
	s := realShape(t)
	large := s.sets[0]
	modelBytes := s.bodies[large.model.URL]
	w := openStore(t)
	slot := t.TempDir()
	opts := judgeOpts(s, gpuHost(11*gib), 500*gib)
	opts.StoreDir, opts.AdoptModelsDir = w.Dir, slot
	verdict := func() (storage, bandwidth string) {
		t.Helper()
		sel, err := judge(t, s, opts)
		if err != nil {
			t.Fatal(err)
		}
		v := verdictOf(t, sel.Sets, "large")
		return v.Storage, v.Bandwidth
	}
	archive := humanBytes(large.runtime.Size)
	both := humanBytes(large.runtime.Size + large.model.Size)
	if storage, bandwidth := verdict(); !strings.Contains(bandwidth, both+" to download") || !strings.Contains(storage, "models "+humanBytes(large.model.Size)) {
		t.Fatalf("nothing anywhere: storage %q, bandwidth %q", storage, bandwidth)
	}
	// In the slot: disk, not a download.
	own := modelFile(pluginStorageDir(slot, s.id), large.model)
	if err := os.MkdirAll(filepath.Dir(own), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(own, modelBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if storage, bandwidth := verdict(); !strings.Contains(bandwidth, archive+" to download") || strings.Contains(bandwidth, both) || !strings.Contains(storage, "models "+humanBytes(large.model.Size)) {
		t.Fatalf("the slot's own copy: storage %q, bandwidth %q", storage, bandwidth)
	}
	// In the store, brought by another identity: neither.
	blob, _ := w.Blob(large.model.SHA256)
	if err := os.WriteFile(blob, modelBytes, 0o400); err != nil {
		t.Fatal(err)
	}
	if storage, bandwidth := verdict(); !strings.Contains(bandwidth, archive+" to download") || strings.Contains(storage, "models ") {
		t.Fatalf("another identity's blob: storage %q, bandwidth %q", storage, bandwidth)
	}
}

// readOnly takes write from every file and directory under dir (an
// executable stays executable), and gives the directories' back when the
// test ends.
func readOnly(t *testing.T, dir string) {
	t.Helper()
	var dirs []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, path)
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return os.Chmod(path, info.Mode().Perm()&^0o222)
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Chmod(dirs[i], 0o555); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, d := range dirs {
			_ = os.Chmod(d, 0o700)
		}
	})
	if runtime.GOOS == "windows" {
		t.Log("Windows keeps a directory's read-only attribute as advice; the tree's hash is the proof here")
	}
}

// treeHash is every name under dir, with its kind, size and bytes.
func treeHash(t *testing.T, dir string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		info, err := d.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s %v %d\n", filepath.ToSlash(rel), info.Mode().Type(), info.Size())
		if info.Mode().IsRegular() {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			if _, err := io.Copy(h, f); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}
