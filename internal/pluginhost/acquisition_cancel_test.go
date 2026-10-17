//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

func TestCancelledModelsDoNotCertifyOrMutateCache(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "cached"}[cached], func(t *testing.T) {
			data := []byte("the complete model")
			w := openStore(t)
			decl := ModelDecl{Name: "model", URL: "https://example.invalid/model", Size: int64(len(data)), SHA256: sha(data)}
			view, _ := w.View(plugstore.ViewKey(modelsDigest, []string{decl.Name}))
			if cached {
				if err := os.WriteFile(blobOf(w, decl), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			calls := 0
			fetch := func(context.Context, string, int64, io.Writer) (int64, error) { calls++; return 0, context.Canceled }
			err := EnsureEntry(ctx, w, viewWork([]string{decl.Name}, decl), fetch, Adoption{}, nil)
			if !errors.Is(err, context.Canceled) || calls != 0 {
				t.Fatalf("cancelled acquisition must stop before cache/read/fetch: err=%v fetches=%d", err, calls)
			}
			if _, err := os.Lstat(view); !os.IsNotExist(err) {
				t.Fatalf("cancelled acquisition made the view: %v", err)
			}
		})
	}
}

func TestCancelledModelDownloadKeepsUnpublishedCompletePartial(t *testing.T) {
	data := bytes.Repeat([]byte("weights"), 1000)
	w := openStore(t)
	decl := ModelDecl{Name: "model", URL: "https://example.invalid/model", Size: int64(len(data)), SHA256: sha(data)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fetch := func(_ context.Context, _ string, offset int64, w io.Writer) (int64, error) {
		n, err := w.Write(data[offset:])
		cancel()
		return int64(n), err
	}
	err := EnsureEntry(ctx, w, viewWork([]string{decl.Name}, decl), fetch, Adoption{}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled complete download was certified: %v", err)
	}
	final := blobOf(w, decl)
	if _, err := os.Stat(final); !os.IsNotExist(err) {
		t.Fatalf("cancelled download published: %v", err)
	}
	if got, err := os.ReadFile(blobPartial(w, decl)); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("complete partial lost: %v", err)
	}
	// A full partial has no suffix to request. Verify it locally; HTTP range
	// bytes=size- is unsatisfiable at a conforming server.
	noFetch := func(context.Context, string, int64, io.Writer) (int64, error) {
		t.Error("full partial was fetched again")
		return 0, io.EOF
	}
	if err := ensureModelsIn(w, noFetch, decl); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(final); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("resume changed model: %v", err)
	}
}

func TestCancelledRuntimeDoesNotCertifyExistingRoot(t *testing.T) {
	fx := newRuntimeFixture(t)
	w := openStore(t)
	root, err := storeRoot(t, w, &fx.decl, fx.entry, fx.fetch, packagefmt.TreeLimits{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := EnsureEntry(ctx, w, rootWork(t, &fx.decl, fx.entry, packagefmt.TreeLimits{}), fx.fetch, Adoption{}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled runtime was certified: %v", err)
	}
	if err := verifyRootIn(t, w, root, &fx.decl, fx.entry, packagefmt.TreeLimits{}); err != nil {
		t.Fatalf("previous root damaged: %v", err)
	}
}

func TestCancelledRuntimeDownloadPreservesArchiveAndDoesNotInstall(t *testing.T) {
	fx := newRuntimeFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fetch := func(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
		n, err := fx.fetch(ctx, url, offset, w)
		cancel()
		return n, err
	}
	w := openStore(t)
	work := rootWork(t, &fx.decl, fx.entry, packagefmt.TreeLimits{})
	err := EnsureEntry(ctx, w, work, fetch, Adoption{}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled runtime download installed: %v", err)
	}
	key := plugstore.RootKey(fx.decl.VariantID, fx.decl.InventorySHA256, fx.entry.Name, fx.entry.Digest)
	root, _ := w.Root(key)
	for _, path := range []string{root, root + plugstore.RecordSuffix} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("cancelled runtime published %s: %v", path, err)
		}
	}
	archive, _ := w.Archive(fx.decl.SHA256)
	if got, err := os.ReadFile(archive + plugstore.PartialSuffix); err != nil || !bytes.Equal(got, fx.archive) {
		t.Fatalf("archive partial lost: %v", err)
	}
	if err := EnsureEntry(context.Background(), w, work, fx.fetch, Adoption{}, nil); err != nil {
		t.Fatal(err)
	}
	if fx.fetches != 1 {
		t.Fatalf("complete runtime archive fetched again: %d", fx.fetches)
	}
}

type cancellingAcquisitionSource struct {
	cancel context.CancelFunc
	calls  int
}

func (r *cancellingAcquisitionSource) Read(p []byte) (int, error) {
	r.calls++
	for i := range p {
		p[i] = 'x'
	}
	r.cancel()
	return len(p), nil
}

func TestAcquisitionReaderStopsBetweenBoundedReads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &cancellingAcquisitionSource{cancel: cancel}
	var out bytes.Buffer
	n, err := io.CopyBuffer(&out, acquisitionReader{ctx, source}, make([]byte, 1<<20))
	if !errors.Is(err, context.Canceled) || source.calls != 1 || n <= 0 || n > 32<<10 {
		t.Fatalf("cancelled local read kept consuming: n=%d calls=%d err=%v", n, source.calls, err)
	}
	// A previously-cancelled context must not even touch the underlying file.
	if _, err := (acquisitionReader{ctx, source}).Read(make([]byte, 100)); !errors.Is(err, context.Canceled) || source.calls != 1 {
		t.Fatalf("cancelled reader touched source: calls=%d err=%v", source.calls, err)
	}
}

func TestCancelledRuntimeWaitDoesNotWaitForOwner(t *testing.T) {
	key := filepath.Join(t.TempDir(), "root")
	release, err := lockRoot(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		unlock, err := lockRoot(ctx, key)
		if unlock != nil {
			unlock()
		}
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled waiter acquired root: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled runtime install waited for another install's lock")
	}
}

func TestCompleteModelPartialIsVerifiedNotRefetched(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid", true: "corrupt"}[corrupt], func(t *testing.T) {
			data := []byte("model bytes")
			decl := ModelDecl{Name: "weights", URL: "https://example.invalid/model", Size: int64(len(data)), SHA256: sha(data)}
			w := openStore(t)
			path := blobOf(w, decl)
			partial := append([]byte{}, data...)
			if corrupt {
				partial[0] ^= 1
			}
			if err := os.WriteFile(blobPartial(w, decl), partial, 0600); err != nil {
				t.Fatal(err)
			}
			// A COMPLETE PARTIAL THAT DOES NOT HASH WAS A RESUMED DOWNLOAD: it
			// is discarded and fetched once whole (the download rule), never
			// certified; a valid one is admitted with no request at all.
			var offsets []int64
			fetch := func(_ context.Context, _ string, offset int64, w io.Writer) (int64, error) {
				offsets = append(offsets, offset)
				if !corrupt {
					t.Error("complete partial was refetched")
					return 0, io.EOF
				}
				n, err := w.Write(data[offset:])
				return int64(n), err
			}
			err := ensureModelsIn(w, fetch, decl)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != string(data) {
				t.Fatalf("the model is not the declared bytes: %q %v", got, err)
			}
			if corrupt && fmt.Sprint(offsets) != "[0]" {
				t.Fatalf("the corrupt partial was not fetched once whole: offsets %v", offsets)
			}
		})
	}
}

// Cancel at a deterministic context observation, not after a machine-specific
// sleep. The embedded real context preserves Done/Err semantics after that
// point. This exercises mid-hash cancellation without production test hooks.
type acquisitionCancelContext struct {
	context.Context
	cancel context.CancelFunc
	after  int32
	checks atomic.Int32
}

func (c *acquisitionCancelContext) Err() error {
	if c.checks.Add(1) == c.after {
		c.cancel()
	}
	return c.Context.Err()
}

// A VERIFICATION CUT SHORT IS NOT DAMAGE: an activation whose context ends
// while it hashes a model in the store answers its context's error, never
// a damaged entry, and the model is as it was.
func TestAcquisitionHashCancellationIsNotACorruptFile(t *testing.T) {
	data := bytes.Repeat([]byte("model"), 50000)
	w := openStore(t)
	decl := ModelDecl{Name: "weights", URL: "https://example.invalid/weights", Size: int64(len(data)), SHA256: sha(data)}
	m := modelMaterial(w, "voice", "1", decl)
	if err := materializeInProcess(t.Context(), w, m, func(_ context.Context, _ string, offset int64, out io.Writer) (int64, error) {
		n, err := out.Write(data[offset:])
		return int64(n), err
	}); err != nil {
		t.Fatal(err)
	}
	path := modelFile(m.ModelsDir, decl)
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &acquisitionCancelContext{Context: base, cancel: cancel, after: 7}
	_, held, err := m.verify(ctx)
	held.release()
	var damaged *MaterialDamageError
	if !errors.Is(err, context.Canceled) || errors.As(err, &damaged) {
		t.Fatalf("a cancelled hash was taken for damage, or accepted: %v checks=%d", err, ctx.checks.Load())
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("interrupted verification destroyed existing model: %v", err)
	}
}

func TestRuntimeExtractionCancellationKeepsVerifiedArchive(t *testing.T) {
	// Walk the cached operation's cancellation observations to cover
	// observation, including between gzip reads and before publication.
	// Each trial has its own cache/root and never reuses a prior result.
	covered := 0
	for at := int32(1); at <= 40; at++ {
		fx := newRuntimeFixture(t)
		w := openStore(t)
		archive, _ := w.Archive(fx.decl.SHA256)
		if err := os.WriteFile(archive, fx.archive, 0600); err != nil {
			t.Fatal(err)
		}
		work := rootWork(t, &fx.decl, fx.entry, packagefmt.TreeLimits{})
		base, cancel := context.WithCancel(context.Background())
		ctx := &acquisitionCancelContext{Context: base, cancel: cancel, after: at}
		err := EnsureEntry(ctx, w, work, fx.fetch, Adoption{}, nil)
		triggered := base.Err() != nil
		cancel()
		if !triggered {
			if err != nil {
				t.Fatalf("uncancelled run: %v", err)
			}
			break
		}
		covered++
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel point %d installed: %v", at, err)
		}
		if got, err := os.ReadFile(archive); err != nil || !bytes.Equal(got, fx.archive) {
			t.Fatalf("cancel point %d lost verified archive: %v", at, err)
		}
		if storePartials(t, w) != 0 {
			t.Fatalf("cancel point %d left partial installation", at)
		}
		root, _ := w.Root(work.Entry[2:])
		for _, p := range []string{root, root + plugstore.RecordSuffix} {
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Fatalf("cancel point %d left published state: %s %v", at, p, err)
			}
		}
	}

	if covered < 6 {
		t.Fatalf("cancellation coverage disappeared: %d observations", covered)
	}
}
