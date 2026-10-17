//go:build (linux && !android) || (darwin && !ios) || windows

package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/machine"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

// THE ACCOUNT'S PLUGIN STORE IN THE TEST'S PROCESS. An identity reads its
// plugins' material from the store and asks the machine service, its one
// writer, for what is absent. These tests give the identity a development
// service's directory of their own (machine.dir), the store that lies in
// it, and the store's writer running here, downloading through the test's
// fetcher — what the service does for a need.
type testStore struct {
	dir string // the development service's directory the config names
	w   *plugstore.Writer
}

func newTestStore(t *testing.T) *testStore {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "machine")
	store, err := machine.StoreDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	w, err := plugstore.OpenWriter(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		w.Close()
		for path, n := range plugstore.HeldForTest(w.Dir) {
			t.Errorf("%d pin(s) still hold %s after the test's activations ended", n, path)
		}
	})
	return &testStore{dir: dir, w: w}
}

// name points the identity's config at this store's service.
func (s *testStore) name(cfg *Config) { cfg.Machine.Dir = s.dir }

// materialize is the writer, downloading through fetch, as the identity's
// Acquirer asks it.
func (s *testStore) materialize(fetch pluginhost.ModelFetcher) func(context.Context, pluginhost.Material, pluginhost.ModelFetcher) error {
	write := pluginhost.StoreMaterializerForTest(s.w)
	return func(ctx context.Context, m pluginhost.Material, _ pluginhost.ModelFetcher) error {
		return write(ctx, m, fetch)
	}
}

// acquirer is an Acquirer over this store's writer, downloading through
// fetch.
func (s *testStore) acquirer(fetch pluginhost.ModelFetcher, cfg pluginhost.AcquirerConfig) *pluginhost.Acquirer {
	cfg.Materialize = s.materialize(fetch)
	return pluginhost.NewAcquirer(cfg)
}
