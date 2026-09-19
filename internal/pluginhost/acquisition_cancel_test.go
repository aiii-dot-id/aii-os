package pluginhost

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

func TestCancelledModelsDoNotCertifyOrMutateCache(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "cached"}[cached], func(t *testing.T) {
			data := []byte("the complete model")
			dir := filepath.Join(t.TempDir(), "models")
			decl := ModelDecl{Name: "model", URL: "https://example.invalid/model", Size: int64(len(data)), SHA256: sha(data)}
			if cached {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, decl.Name), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			calls := 0
			fetch := func(context.Context, string, int64, io.Writer) (int64, error) { calls++; return 0, context.Canceled }
			err := EnsureModels(ctx, "voice", []ModelDecl{decl}, dir, fetch, nil)
			if !errors.Is(err, context.Canceled) || calls != 0 {
				t.Fatalf("cancelled acquisition must stop before cache/read/fetch: err=%v fetches=%d", err, calls)
			}
			if !cached {
				if _, err := os.Lstat(dir); !os.IsNotExist(err) {
					t.Fatalf("cancelled acquisition created cache: %v", err)
				}
			}
		})
	}
}

func TestCancelledModelDownloadKeepsUnpublishedCompletePartial(t *testing.T) {
	data := bytes.Repeat([]byte("weights"), 1000)
	dir := t.TempDir()
	decl := ModelDecl{Name: "model", URL: "https://example.invalid/model", Size: int64(len(data)), SHA256: sha(data)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fetch := func(_ context.Context, _ string, offset int64, w io.Writer) (int64, error) {
		n, err := w.Write(data[offset:])
		cancel()
		return int64(n), err
	}
	err := EnsureModels(ctx, "voice", []ModelDecl{decl}, dir, fetch, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled complete download was certified: %v", err)
	}
	final := filepath.Join(dir, decl.Name)
	if _, err := os.Stat(final); !os.IsNotExist(err) {
		t.Fatalf("cancelled download published: %v", err)
	}
	if got, err := os.ReadFile(final + modelPartialSuffx); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("complete partial lost: %v", err)
	}
	// .
	// .
	noFetch := func(context.Context, string, int64, io.Writer) (int64, error) {
		t.Error("full partial was fetched again")
		return 0, io.EOF
	}
	if err := EnsureModels(context.Background(), "voice", []ModelDecl{decl}, dir, noFetch, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(final); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("resume changed model: %v", err)
	}
}

func TestCancelledRuntimeDoesNotCertifyExistingRoot(t *testing.T) {
	fx := newRuntimeFixture(t)
	root, err := EnsureRuntime(context.Background(), "voice", &fx.decl, fx.dir, fx.fetch, packagefmt.TreeLimits{}, fx.entry, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := EnsureRuntime(ctx, "voice", &fx.decl, fx.dir, fx.fetch, packagefmt.TreeLimits{}, fx.entry, nil)
	if !errors.Is(err, context.Canceled) || got != "" {
		t.Fatalf("cancelled runtime was certified: root=%q err=%v", got, err)
	}
	if err := VerifyRuntimeRoot(root, &fx.decl, fx.entry, packagefmt.TreeLimits{}); err != nil {
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
	root, err := EnsureRuntime(ctx, "voice", &fx.decl, fx.dir, fetch, packagefmt.TreeLimits{}, fx.entry, nil)
	if !errors.Is(err, context.Canceled) || root != "" {
		t.Fatalf("cancelled runtime download installed: root=%q err=%v", root, err)
	}
	key := RuntimeRootKey(fx.decl.VariantID, fx.decl.InventorySHA256, fx.entry.Name, fx.entry.Digest)
	for _, path := range []string{filepath.Join(fx.dir, key), recordPath(filepath.Join(fx.dir, key))} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("cancelled runtime published %s: %v", path, err)
		}
	}
	partial := filepath.Join(fx.dir, archivesDir, fx.decl.SHA256+archiveSuffix+".partial")
	if got, err := os.ReadFile(partial); err != nil || !bytes.Equal(got, fx.archive) {
		t.Fatalf("archive partial lost: %v", err)
	}
	if _, err := EnsureRuntime(context.Background(), "voice", &fx.decl, fx.dir, fx.fetch, packagefmt.TreeLimits{}, fx.entry, nil); err != nil {
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
	// .
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
			dir := t.TempDir()
			path := filepath.Join(dir, decl.Name)
			partial := append([]byte{}, data...)
			if corrupt {
				partial[0] ^= 1
			}
			if err := os.WriteFile(path+modelPartialSuffx, partial, 0600); err != nil {
				t.Fatal(err)
			}
			fetch := func(context.Context, string, int64, io.Writer) (int64, error) {
				t.Error("complete partial was refetched")
				return 0, io.EOF
			}
			err := EnsureModels(context.Background(), "voice", []ModelDecl{decl}, dir, fetch, nil)
			if corrupt {
				if err == nil {
					t.Fatal("corrupt complete partial certified")
				}
				for _, p := range []string{path, path + modelPartialSuffx} {
					if _, err := os.Stat(p); !os.IsNotExist(err) {
						t.Fatalf("bad candidate remains: %v", err)
					}
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

// .
// .
// .
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

func TestAcquisitionHashCancellationIsNotACorruptFile(t *testing.T) {
	data := bytes.Repeat([]byte("model"), 50000)
	dir := t.TempDir()
	path := filepath.Join(dir, "weights")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &acquisitionCancelContext{Context: base, cancel: cancel, after: 7}
	calls := 0
	fetch := func(context.Context, string, int64, io.Writer) (int64, error) { calls++; return 0, io.EOF }
	err := EnsureModels(ctx, "voice", []ModelDecl{{Name: "weights", Size: int64(len(data)), SHA256: sha(data)}}, dir, fetch, nil)
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("cancelled hash accepted or refetched: %v fetches=%d checks=%d", err, calls, ctx.checks.Load())
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("interrupted verification destroyed existing model: %v", err)
	}
}

func TestRuntimeExtractionCancellationKeepsVerifiedArchive(t *testing.T) {
	// .
	// .
	// .
	covered := 0
	for at := int32(1); at <= 40; at++ {
		fx := newRuntimeFixture(t)
		adir := filepath.Join(fx.dir, archivesDir)
		if err := os.MkdirAll(adir, 0700); err != nil {
			t.Fatal(err)
		}
		archive := filepath.Join(adir, fx.decl.SHA256+archiveSuffix)
		if err := os.WriteFile(archive, fx.archive, 0600); err != nil {
			t.Fatal(err)
		}
		base, cancel := context.WithCancel(context.Background())
		ctx := &acquisitionCancelContext{Context: base, cancel: cancel, after: at}
		root, err := EnsureRuntime(ctx, "voice", &fx.decl, fx.dir, fx.fetch, packagefmt.TreeLimits{}, fx.entry, nil)
		triggered := base.Err() != nil
		cancel()
		if !triggered {
			if err != nil || root == "" {
				t.Fatalf("uncancelled run: %v", err)
			}
			break
		}
		covered++
		if !errors.Is(err, context.Canceled) || root != "" {
			t.Fatalf("cancel point %d installed: root=%q err=%v", at, root, err)
		}
		if got, err := os.ReadFile(archive); err != nil || !bytes.Equal(got, fx.archive) {
			t.Fatalf("cancel point %d lost verified archive: %v", at, err)
		}
		if partialDirs(t, fx.dir) != 0 {
			t.Fatalf("cancel point %d left partial installation", at)
		}
		key := RuntimeRootKey(fx.decl.VariantID, fx.decl.InventorySHA256, fx.entry.Name, fx.entry.Digest)
		for _, p := range []string{filepath.Join(fx.dir, key), recordPath(filepath.Join(fx.dir, key))} {
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Fatalf("cancel point %d left published state: %s %v", at, p, err)
			}
		}
	}
	if covered < 10 {
		t.Fatalf("cancellation coverage disappeared: %d observations", covered)
	}
}
