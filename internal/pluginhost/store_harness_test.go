//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

// THE MACHINE SERVICE, IN THE TEST'S PROCESS. Activation reads the account's
// plugin store and asks its one writer for what is absent; in production
// that writer is the machine service across the link (internal/machine).
// These tests give the activation a store of their own and a writer that
// does, in process, what the service does for each entry a material names:
// repair what was reported damaged, then bring the rest (EnsureEntry), with
// the test's fetcher as the download path.

// useStore points opts at a fresh store and hands it the in-process writer:
// Options.Materialize, the inline path an activation without an Acquirer
// takes. It answers the writer, for a test that looks inside the store.
func useStore(t *testing.T, opts *Options, fetch ModelFetcher) *plugstore.Writer {
	t.Helper()
	w := openStore(t)
	opts.StoreDir = w.Dir
	opts.Materialize = func(ctx context.Context, m Material) error {
		return materializeInProcess(ctx, w, m, fetch)
	}
	return w
}

// storeMaterializer is the same writer as an Acquirer's Materialize: what
// a test hands NewAcquirer where production hands the link's need.
func storeMaterializer(w *plugstore.Writer) func(context.Context, Material, ModelFetcher) error {
	return StoreMaterializerForTest(w)
}

// materializeInProcess is the service's work for every entry of m.
func materializeInProcess(ctx context.Context, w *plugstore.Writer, m Material, fetch ModelFetcher) error {
	return StoreMaterializerForTest(w)(ctx, m, fetch)
}

// carrierPackage is a package file whose one install member is the carrier:
// what a root's work reads its carrier from (the walk reads members; it
// verifies no signature, which the identity did).
func carrierPackage(t *testing.T, entry entrypointSpec) (pkg, member string) {
	t.Helper()
	member = "variants/test/" + entry.Name
	spec := packagetest.PackageSpec{Root: "org.example.carrier-0.1.0", Manifest: []byte(`{}`), InstallFiles: map[string][]byte{member: entry.Bytes}}
	return writePkg(t, spec), member
}

// rootWork is the work that publishes d's root with entry as its carrier.
func rootWork(t *testing.T, d *RuntimeDecl, entry entrypointSpec, limits packagefmt.TreeLimits) StoreWork {
	t.Helper()
	pkg, member := carrierPackage(t, entry)
	key := plugstore.RootKey(d.VariantID, d.InventorySHA256, entry.Name, entry.Digest)
	return StoreWork{Entry: "r/" + key, Root: &RootWork{Runtime: *d, Package: pkg, Member: member, Name: entry.Name, Digest: entry.Digest, Limits: limits}}
}

// storeRoot publishes d's root in w through the writer, and answers where
// it stands.
func storeRoot(t *testing.T, w *plugstore.Writer, d *RuntimeDecl, entry entrypointSpec, fetch ModelFetcher, limits packagefmt.TreeLimits) (string, error) {
	t.Helper()
	work := rootWork(t, d, entry, limits)
	if err := EnsureEntry(t.Context(), w, work, fetch, Adoption{}, nil); err != nil {
		return "", err
	}
	return w.Root(work.Entry[2:])
}

// verifyRootIn verifies a published root in w as an activation does, with
// nothing held afterwards.
func verifyRootIn(t *testing.T, w *plugstore.Writer, root string, d *RuntimeDecl, entry entrypointSpec, limits packagefmt.TreeLimits) error {
	t.Helper()
	raw, err := os.ReadFile(root + plugstore.RecordSuffix)
	_, verr := verifyStoreRoot(t.Context(), root, raw, err, d, entry, limits)
	return verr
}

// attachedRecorder is an attached Acquirer whose writer is w, downloading
// through f, that reports each plugin ready on the channel.
func attachedRecorder(t *testing.T, f *fetchRecord, w *plugstore.Writer) (*Acquirer, chan string) {
	t.Helper()
	ready := make(chan string, 8)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	acq := NewAcquirer(AcquirerConfig{Materialize: storeMaterializer(w), Fetcher: f.fetch, Ready: func(id string) { ready <- id },
		After: func(time.Duration) <-chan time.Time { return nil }, Logf: func(string, ...any) {},
		Spawn: func(fn func()) bool { wg.Add(1); go func() { defer wg.Done(); fn() }(); return true }})
	acq.Attach(ctx)
	t.Cleanup(func() { cancel(); wg.Wait() })
	return acq, ready
}

// byURL is one download path that answers each address with its own
// fetcher, as the service's one fetcher serves every address a package
// declares; an address it was not given is a 404.
func byURL(routes map[string]ModelFetcher) ModelFetcher {
	return func(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
		if f, ok := routes[url]; ok && f != nil {
			return f(ctx, url, offset, w)
		}
		return 0, &FetchStatusError{Status: 404}
	}
}

// modelMaterial is a material of these models alone, in their view of w:
// what Prepare names for a package that declares only models.
func modelMaterial(w *plugstore.Writer, id, version string, models ...ModelDecl) Material {
	var names []string
	for _, d := range models {
		if d.When == nil {
			names = append(names, d.Name)
		}
	}
	v := &ViewWork{ModelsSHA256: modelsDigest, Names: names}
	dir, _ := w.View(plugstore.ViewKey(v.ModelsSHA256, v.Names))
	return Material{PluginID: id, Version: version, Models: models, ModelsDir: dir, store: w.Dir, view: v}
}

// storeAcquirer is an Acquirer whose writer is w, downloading through
// fetch: what the machine service runs, in the test's process.
func storeAcquirer(w *plugstore.Writer, fetch ModelFetcher, cfg AcquirerConfig) *Acquirer {
	cfg.Materialize, cfg.Fetcher = storeMaterializer(w), fetch
	return NewAcquirer(cfg)
}

// runtimeMaterial is modelMaterial with the fixture's runtime too: its root
// in w, its carrier in a package of its own.
func runtimeMaterial(t *testing.T, w *plugstore.Writer, id, version string, rt *runtimeFixture, models ...ModelDecl) Material {
	t.Helper()
	m := Material{PluginID: id, Version: version, store: w.Dir}
	if len(models) > 0 {
		m = modelMaterial(w, id, version, models...)
	}
	m.Runtime, m.RuntimeDir, m.entry = &rt.decl, filepath.Join(w.Dir, plugstore.DirRoots), rt.entry
	m.pkg, m.member = carrierPackage(t, rt.entry)
	return m
}

// ensureModelsIn brings models into one view of w through the writer, as
// the machine service does for a need that names them.
func ensureModelsIn(w *plugstore.Writer, fetch ModelFetcher, models ...ModelDecl) error {
	var names []string
	for _, d := range models {
		names = append(names, d.Name)
	}
	return EnsureEntry(context.Background(), w, viewWork(names, models...), fetch, Adoption{}, nil)
}

// blobPartial is where the writer resumes a model's download from.
func blobPartial(w *plugstore.Writer, d ModelDecl) string {
	blob, _ := w.Blob(d.SHA256)
	return blob + plugstore.PartialSuffix
}

// blobOf is a model's blob in w.
func blobOf(w *plugstore.Writer, d ModelDecl) string {
	blob, _ := w.Blob(d.SHA256)
	return blob
}

// Acquire verifies the declared material in the account's plugin store and
// returns its runtime root, holding nothing: what is absent or damaged is
// asked of the store's writer first (acquire). A caller that will run from
// the material holds it instead (Staged.Acquire).
func Acquire(ctx context.Context, mat Material, opts *Options) (string, error) {
	root, held, err := acquire(ctx, mat, opts)
	held.release()
	return root, err
}
