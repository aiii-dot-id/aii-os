//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

func waitsForPackage(err error) bool {
	var bound interface{ WaitsOnPackage() bool }
	return errors.As(err, &bound) && bound.WaitsOnPackage()
}

func TestRuntimeRefusalDistinguishesDeclarationFromRepairableBytes(t *testing.T) {
	for _, kind := range []string{"file-count", "byte-count", "inventory-pin", "network", "download-hash", "installed-file", "max-files", "max-file-bytes", "max-installed-bytes", "max-depth", "max-compressed", "max-inventory"} {
		t.Run(kind, func(t *testing.T) {
			fx := newRuntimeFixture(t)
			w := openStore(t)
			limits := packagefmt.TreeLimits{}
			permanent := false
			var damaged error
			switch kind {
			case "file-count":
				fx.decl.Files++
				permanent = true
			case "byte-count":
				fx.decl.InstalledBytes++
				permanent = true
			case "inventory-pin":
				fx.decl.InventorySHA256 = strings.Repeat("1", 64)
				permanent = true
			case "network":
				fx.failWith = errors.New("offline")
			case "download-hash":
				fx.decl.SHA256 = strings.Repeat("0", 64)
			case "installed-file":
				// A published tree a file of which went: its reader refuses
				// it as damage the store's writer mends, never the package's.
				root, err := storeRoot(t, w, &fx.decl, fx.entry, fx.fetch, limits)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(root, "engine", "session.py")); err != nil {
					t.Fatal(err)
				}
				_, held, verr := runtimeMaterial(t, w, "id.example.runtime", "1", fx).verify(t.Context())
				held.release()
				damaged = verr
			case "max-files":
				limits.MaxFiles = 1
			case "max-file-bytes":
				limits.MaxFileBytes = 1
			case "max-installed-bytes":
				limits.MaxInstalledBytes = 1
			case "max-depth":
				limits.MaxDepth = 1
			case "max-compressed":
				limits.MaxCompressedBytes = 1
			case "max-inventory":
				limits.MaxInventoryBytes = 1
			}
			_, err := storeRoot(t, w, &fx.decl, fx.entry, fx.fetch, limits)
			if damaged != nil {
				err = damaged
			}
			if err == nil || waitsForPackage(err) != permanent {
				t.Fatalf("wrong refusal owner: package-bound=%t want=%t error=%v", waitsForPackage(err), permanent, err)
			}
		})
	}
}

// A signed contradiction reaches the activation as its typed refusal rather
// than remaining forever on the preparing card. Explicit retry and a corrected
// declaration still use the same owned worker and directory-exit fence.
func TestAcquisitionReportsPackageContradictionWithoutTimedRedownload(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		fx := newRuntimeFixture(t)
		bad := fx.decl
		bad.Files++
		release := make(chan struct{})
		var once sync.Once
		var q *Acquirer
		w := openStore(t)
		q = storeAcquirer(w, fx.fetch, AcquirerConfig{Logf: func(string, ...any) {}, Changed: func() {
			if st, ok := q.Status("id.example.runtime"); ok && st.Phase == PhaseWaiting && st.RetryAt.IsZero() {
				once.Do(func() {
					select {
					case <-release:
					case <-ctx.Done():
					}
				})
			}
		}})
		q.Attach(ctx)
		m := runtimeMaterial(t, w, "id.example.runtime", "1", fx)
		m.Runtime = &bad
		opts := &Options{Acquirer: q}
		_, _ = Acquire(ctx, m, opts)
		synctest.Wait()
		_, err := Acquire(ctx, m, opts)
		if !waitsForPackage(err) {
			t.Fatalf("activation did not receive the package-bound refusal: %v", err)
		}
		st, _ := q.Status(m.PluginID)
		if !st.RetryAt.IsZero() {
			t.Fatalf("card promises a retry for a package contradiction: %+v", st)
		}
		time.Sleep(24 * time.Hour) // virtual time
		synctest.Wait()
		if fx.fetches != 1 {
			t.Fatalf("a proved bad declaration caused %d downloads", fx.fetches)
		}
		q.Retry(m.PluginID)
		// Retry must clear the old answer synchronously, not after the
		// facility's new attempt has already consumed it again.
		if _, err := Acquire(ctx, m, opts); waitsForPackage(err) {
			t.Fatal("explicit retry immediately replayed the old refusal")
		}
		close(release)
		synctest.Wait()
		if st, _ := q.Status(m.PluginID); st.Attempt != 2 {
			t.Fatalf("explicit retry did not run: %+v", st)
		}
		time.Sleep(24 * time.Hour)
		synctest.Wait()
		if fx.fetches != 2 {
			t.Fatalf("the retried contradiction did not wait without a timer: %d fetches", fx.fetches)
		}
		m.Version, m.Runtime = "2", &fx.decl
		_, _ = Acquire(ctx, m, opts)
		synctest.Wait()
		if _, err := Acquire(ctx, m, opts); err != nil {
			t.Fatalf("corrected declaration failed to recover: %v", err)
		}
	})
}

func TestRuntimeArchiveClassificationUsesTheActualBytesAndIOCause(t *testing.T) {
	for _, kind := range []string{"bad-signed-archive", "changed-bytes", "extra-bytes", "read-fault", "output-fault", "over-ceiling"} {
		t.Run(kind, func(t *testing.T) {
			fx := newRuntimeFixture(t)
			raw := append([]byte(nil), fx.archive...)
			limits := packagefmt.TreeLimits{}
			dest := t.TempDir()
			var fault error
			wantVerified, wantBound := false, false
			switch kind {
			case "bad-signed-archive":
				raw = []byte("not a gzip archive")
				sum := sha256.Sum256(raw)
				fx.decl.Size, fx.decl.SHA256 = int64(len(raw)), hex.EncodeToString(sum[:])
				wantVerified, wantBound = true, true
			case "changed-bytes":
				raw[0] ^= 1
			case "extra-bytes":
				raw = append(raw, 0)
			case "read-fault":
				fault = errors.New("synthetic source read failed")
			case "output-fault":
				// A non-directory output is deterministic even when the tests
				// run as root; permissions alone would not prove this boundary.
				dest = filepath.Join(dest, "not-a-directory")
				if err := os.WriteFile(dest, []byte("kept"), 0600); err != nil {
					t.Fatal(err)
				}
				wantVerified = true
			case "over-ceiling":
				limits.MaxCompressedBytes = 1
			}
			input := &runtimeFaultReader{data: raw, fault: fault}
			_, verified, err := extractRuntimeArchive(t.Context(), input, &fx.decl, limits, dest)
			if err == nil || verified != wantVerified || (verified && runtimeContentError(err)) != wantBound {
				t.Fatalf("wrong byte/cause proof: verified=%t bound=%t error=%v", verified, verified && runtimeContentError(err), err)
			}
			if fault != nil && !errors.Is(err, fault) {
				t.Fatalf("source I/O cause was lost: %v", err)
			}
			if kind == "output-fault" {
				var pathErr *fs.PathError
				if !errors.As(err, &pathErr) {
					t.Fatalf("output I/O was mislabeled as content: %v", err)
				}
			}
			if kind == "over-ceiling" && len(input.data) == 0 {
				t.Fatal("classification drained an archive beyond the host's ceiling")
			}
		})
	}
}

type runtimeFaultReader struct {
	data  []byte
	fault error
}

func (r *runtimeFaultReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	err := r.fault
	r.fault = nil
	return n, err
}

// A DAMAGED ROOT IS REPAIRED BY THE STORE'S WRITER, NEVER BY ITS READER, AND
// NEVER UNDER A RUNNING ACTIVATION (H1). A file of a published tree is
// removed. The activation refuses the root as damaged and touches nothing;
// the store's writer, told, rebuilds it from the archive the store kept —
// with no download path at all — but only once no activation holds it.
func TestADamagedRootIsRepairedByTheWriterWithoutAnotherDownload(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		fx := newRuntimeFixture(t)
		w := openStore(t)
		root, err := storeRoot(t, w, &fx.decl, fx.entry, fx.fetch, packagefmt.TreeLimits{})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "engine", "session.py")
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		m := runtimeMaterial(t, w, "org.example.engine", "1", fx)
		// Another identity runs from it: refused as damage, the repair
		// waits, and the tree is not touched.
		running, err := w.PinRoot(filepath.Base(root))
		if err != nil {
			t.Fatal(err)
		}
		inline := &Options{Materialize: func(ctx context.Context, m Material) error { return materializeInProcess(ctx, w, m, nil) }}
		if _, err := Acquire(ctx, m, inline); !errors.Is(err, ErrRepairWaits) {
			t.Fatalf("a damaged root an activation holds was not left to wait for it: %v", err)
		}
		if _, err := os.Stat(root); err != nil {
			t.Fatalf("a pinned root was removed: %v", err)
		}
		running.Close()
		q := storeAcquirer(w, nil, AcquirerConfig{Logf: func(string, ...any) {}}) // no download path
		q.Attach(ctx)
		var acquiring *AcquiringError
		if _, err := Acquire(ctx, m, &Options{Acquirer: q}); !errors.As(err, &acquiring) {
			t.Fatalf("the damage was not handed to the writer: %v", err)
		}
		synctest.Wait()
		got, err := Acquire(ctx, m, &Options{Acquirer: q})
		if err != nil || got != root || fx.fetches != 1 {
			t.Fatalf("the damaged root was not rebuilt from the stored archive without a download: %q %v (fetches %d)", got, err, fx.fetches)
		}
		if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, original) {
			t.Fatalf("the rebuilt tree is not the declared one: %v", err)
		}
	})
}

// Like acquisitionCancelContext, inject at a deterministic context observation
// in the real stream. The partial directory exists only after archive
// verification; no timer, root-dependent permission trick or retry loop.
type runtimeOutputFaultContext struct {
	context.Context
	pattern  string
	injected bool
	fault    error
}

func (c *runtimeOutputFaultContext) Err() error {
	if !c.injected {
		paths, err := filepath.Glob(c.pattern)
		if err == nil && len(paths) == 1 {
			c.injected = true
			c.fault = os.WriteFile(filepath.Join(paths[0], "engine"), []byte("blocked output"), 0600)
		}
	}
	return c.Context.Err()
}

func TestRuntimeOutputFailureKeepsVerifiedArchiveForOfflineRecovery(t *testing.T) {
	fx := newRuntimeFixture(t)
	w := openStore(t)
	key := plugstore.RootKey(fx.decl.VariantID, fx.decl.InventorySHA256, fx.entry.Name, fx.entry.Digest)
	ctx := &runtimeOutputFaultContext{Context: t.Context(), pattern: filepath.Join(w.Dir, plugstore.DirRoots, key+".partial-*")}
	err := EnsureEntry(ctx, w, rootWork(t, &fx.decl, fx.entry, packagefmt.TreeLimits{}), fx.fetch, Adoption{}, nil)
	var pathErr *fs.PathError
	if !ctx.injected || ctx.fault != nil || !errors.As(err, &pathErr) || waitsForPackage(err) {
		t.Fatalf("disk failure was not retained as repairable: injected=%v fault=%v err=%v", ctx.injected, ctx.fault, err)
	}
	fx.failWith = errors.New("origin now offline")
	root, err := storeRoot(t, w, &fx.decl, fx.entry, fx.fetch, packagefmt.TreeLimits{})
	if err != nil || root == "" || fx.fetches != 1 {
		t.Fatalf("verified input was lost after output failure: root=%q fetches=%d err=%v", root, fx.fetches, err)
	}
}

func TestAcquisitionRetryKeepsTransientCauseUntilTheNewOutcome(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		fx := newModelFixture(t, "weights", 1000)
		fx.failWith = &NetworkError{Cause: errors.New("offline, kept on the card")}
		w := openStore(t)
		q := storeAcquirer(w, fx.fetch, AcquirerConfig{Logf: func(string, ...any) {}})
		q.Attach(ctx)
		m := modelMaterial(w, "id.example.cause", "", fx.decl)
		q.Want(m)
		synctest.Wait()
		before, _ := q.Status(m.PluginID)
		fx.mu.Lock()
		fx.failWith, fx.block = nil, make(chan struct{})
		fx.mu.Unlock()
		q.Retry(m.PluginID)
		synctest.Wait()
		if now, _ := q.Status(m.PluginID); now.Phase != PhaseFetching || now.LastError != before.LastError || now.LastError == "" {
			t.Fatalf("the card lost its last cause before a new outcome: %+v -> %+v", before, now)
		}
		close(fx.block)
		synctest.Wait()
		if now, _ := q.Status(m.PluginID); now.Phase != PhaseReady || now.LastError != "" {
			t.Fatalf("successful recovery retained the old failure: %+v", now)
		}
	})
}

// A ceiling only extraction can meet, the per-file size or the depth of a
// runtime that declares no extent, refuses after the download; correcting it
// reuses the verified archive. (A declared total, or a declared extent, is
// refused before any download: TestEnsureRuntimeRefusesOverItsCeilingsBeforeAnyFetch.)
func TestRuntimeCeilingChangeReusesCachedInputOffline(t *testing.T) {
	for _, limits := range []packagefmt.TreeLimits{{MaxFileBytes: 1}, {MaxDepth: 1}} {
		fx := newRuntimeFixture(t)
		w := openStore(t)
		_, err := storeRoot(t, w, &fx.decl, fx.entry, fx.fetch, limits)
		if err == nil || waitsForPackage(err) {
			t.Fatalf("host ceiling was not a retryable refusal: %v", err)
		}
		fx.failWith = errors.New("origin now offline")
		root, err := storeRoot(t, w, &fx.decl, fx.entry, fx.fetch, packagefmt.TreeLimits{})
		if err != nil || root == "" || fx.fetches != 1 {
			t.Fatalf("correcting a ceiling required another download: %q %v fetches=%d", root, err, fx.fetches)
		}
	}
}
