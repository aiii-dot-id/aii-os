//go:build (linux && !android) || (darwin && !ios) || windows

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// lateRig is a running plugin with one model every value needs, already
// on disk, and one pack that only French needs, served by a fetcher the
// test can hold.
type lateRig struct {
	a        *App
	ap       *pluginhost.ActivePlugin
	store    *testStore
	dir      string
	base, fr pluginhost.ModelDecl
	mu       sync.Mutex
	asked    []string
	hold     chan struct{}
	fail     error // every fetch answers this, while it is set
	frBytes  []byte
}

const lateID = "org.example.speaks"

func newLateRig(t *testing.T) *lateRig {
	t.Helper()
	r := &lateRig{a: newVoiceApp(t), store: newTestStore(t)}
	decl := func(name string, size int, when *pluginhost.ModelWhen) (pluginhost.ModelDecl, []byte) {
		data := []byte(strings.Repeat(name[:1], size))
		sum := sha256.Sum256(data)
		return pluginhost.ModelDecl{Name: name, URL: "https://example.invalid/" + name, SHA256: hex.EncodeToString(sum[:]), Size: int64(size), When: when}, data
	}
	var baseBytes []byte
	r.base, baseBytes = decl("base", 512, nil)
	r.fr, r.frBytes = decl("fr-pack", 2048, &pluginhost.ModelWhen{Setting: "tts_language", Values: []string{"fr"}})
	started := pluginhost.ModelMaterialForTest(r.store.w.Dir, lateID, "0.1.0", r.base)
	serveBase := func(_ context.Context, _ string, offset int64, w io.Writer) (int64, error) {
		n, err := w.Write(baseBytes[offset:])
		return int64(n), err
	}
	if err := r.store.materialize(serveBase)(t.Context(), started, nil); err != nil {
		t.Fatal(err)
	}
	r.dir = started.ModelsDir
	fetch := func(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
		r.mu.Lock()
		r.asked = append(r.asked, url)
		hold, fail := r.hold, r.fail
		r.mu.Unlock()
		if fail != nil {
			return 0, fail
		}
		if hold != nil {
			select {
			case <-hold:
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}
		if url != r.fr.URL {
			return 0, fmt.Errorf("nothing is served at %s", url)
		}
		n, err := w.Write(r.frBytes[offset:])
		return int64(n), err
	}
	r.a.lateModels = r.store.acquirer(fetch, pluginhost.AcquirerConfig{Ready: r.a.lateModelsReady, Logf: func(string, ...any) {}})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r.a.lateModels.Attach(ctx)
	r.ap = &pluginhost.ActivePlugin{ID: lateID, Version: "0.1.0", ModelsDir: r.dir,
		Settings: []pluginhost.SettingDecl{
			{Key: "tts_language", Type: pluginhost.SettingEnum, Title: "Speaking language", Default: "en", Values: []string{"en", "fr"}},
			{Key: "speed", Type: pluginhost.SettingNumber, Title: "Speed"},
		},
		ModelDecls: []pluginhost.ModelDecl{r.base, r.fr}, Models: []pluginhost.ModelDecl{r.base}}
	r.ap.RunsFromForTest(started)
	setPlugins(r.a, []*pluginhost.ActivePlugin{r.ap})
	return r
}

func (r *lateRig) fetches() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.asked)
}

func (r *lateRig) packOnDisk() bool {
	got, err := os.ReadFile(filepath.Join(r.dir, r.fr.Name))
	return err == nil && string(got) == string(r.frBytes)
}

func (r *lateRig) state(t *testing.T) *dashboard.SettingModelsView {
	t.Helper()
	cfg := r.a.configSnapshot()
	return r.a.settingModelsView(&cfg, r.ap, "tts_language")
}

func TestAModelARunningPluginComesToNeedIsFetchedWhenItsSettingChanges(t *testing.T) {
	r := newLateRig(t)
	if st := r.state(t); st == nil || st.State != dashboard.SettingModelsReady || st.FilesTotal != 0 {
		t.Fatalf("under the default, which needs no pack: %+v", st)
	}
	cfg := r.a.configSnapshot()
	if other := r.a.settingModelsView(&cfg, r.ap, "speed"); other != nil {
		t.Fatalf("a setting no model depends on was given a models state: %+v", other)
	}

	r.mu.Lock()
	r.hold = make(chan struct{})
	held := r.hold
	r.mu.Unlock()
	if _, err := r.a.applyConfigChange(map[string]any{"plugins.settings." + lateID + ".tts_language": "fr"}); err != nil {
		t.Fatalf("choosing French: %v", err)
	}
	eventually(t, "the pack's fetch to begin", func() bool { return r.fetches() > 0 })
	if st := r.state(t); st == nil || st.State != dashboard.SettingModelsFetching || st.FilesTotal != 1 || st.FilesPresent != 0 || st.BytesTotal != r.fr.Size {
		t.Fatalf("while the pack is being fetched the line says: %+v", st)
	}
	close(held)
	eventually(t, "the pack on disk", r.packOnDisk)
	eventually(t, "the line to say ready", func() bool {
		st := r.state(t)
		return st != nil && st.State == dashboard.SettingModelsReady && st.FilesPresent == 1 && st.BytesPresent == r.fr.Size
	})
	// Done is done: the fetch is forgotten, so nothing on the page says
	// the running plugin is being prepared.
	eventually(t, "the finished fetch to be forgotten", func() bool { _, on := r.a.lateModels.Status(lateID); return !on })

	// A second pass with everything present asks for nothing.
	before := r.fetches()
	r.a.reconcileLateModels()
	if _, on := r.a.lateModels.Status(lateID); on || r.fetches() != before {
		t.Fatal("with every file present a fetch was started again")
	}
}

// NOTHING IS FETCHED THAT MAY NOT BE, AND A FETCH DOES NOT OUTLIVE ITS
// PLUGIN. In SAFE the host makes no download; while a new release of the
// same plugin is being prepared, that preparation writes the directory
// and this one waits; and when the plugin stops, its fetch stops.
func TestALateFetchHoldsToSafeToAnUpdateInFlightAndToItsPlugin(t *testing.T) {
	want := func(r *lateRig) {
		t.Helper()
		r.a.cfgMu.Lock()
		r.a.cfg.Plugins.Settings = map[string]map[string]any{lateID: {"tts_language": "fr"}}
		r.a.cfgMu.Unlock()
	}

	t.Run("SAFE", func(t *testing.T) {
		r := newLateRig(t)
		want(r)
		r.a.mode.mu.Lock()
		r.a.mode.mode, r.a.mode.reason = ModeSafe, "a test of SAFE"
		r.a.mode.mu.Unlock()
		r.a.reconcileLateModels()
		time.Sleep(50 * time.Millisecond)
		if _, on := r.a.lateModels.Status(lateID); on || r.fetches() != 0 {
			t.Fatal("a download was started in SAFE")
		}
		if st := r.state(t); st == nil || st.State != dashboard.SettingModelsAbsent {
			t.Fatalf("in SAFE the line should say the files are absent: %+v", st)
		}
	})

	t.Run("an update of the same plugin being prepared", func(t *testing.T) {
		r := newLateRig(t)
		want(r)
		main := pluginhost.NewAcquirer(pluginhost.AcquirerConfig{After: func(time.Duration) <-chan time.Time { return nil }, Logf: func(string, ...any) {}})
		main.Attach(context.Background())
		main.Want(pluginhost.ModelMaterialForTest(r.store.w.Dir, lateID, "0.2.0",
			pluginhost.ModelDecl{Name: "next.bin", URL: "https://example.invalid/next.bin", SHA256: strings.Repeat("a", 64), Size: 10}))
		eventually(t, "the update's preparation to be waiting", func() bool {
			st, ok := main.Status(lateID)
			return ok && st.Phase == pluginhost.PhaseWaiting
		})
		r.a.pluginOpts = &pluginhost.Options{Acquirer: main}
		r.a.reconcileLateModels()
		time.Sleep(50 * time.Millisecond)
		if _, on := r.a.lateModels.Status(lateID); on || r.fetches() != 0 {
			t.Fatal("a second writer was started on a directory an update is preparing")
		}
		// The update settles; the next pass fetches.
		main.Forget(lateID)
		eventually(t, "the update's preparation to leave", func() bool { _, ok := main.Status(lateID); return !ok })
		r.a.reconcileLateModels()
		eventually(t, "the pack on disk once the update is out of the way", r.packOnDisk)
	})

	t.Run("the plugin stops", func(t *testing.T) {
		r := newLateRig(t)
		want(r)
		r.mu.Lock()
		r.hold = make(chan struct{})
		r.mu.Unlock()
		r.a.reconcileLateModels()
		eventually(t, "the fetch to begin", func() bool { return r.fetches() > 0 })
		setPlugins(r.a, nil)
		r.a.reconcileLateModels()
		eventually(t, "the fetch to stop with its plugin", func() bool { _, on := r.a.lateModels.Status(lateID); return !on })
		if r.packOnDisk() {
			t.Fatal("the pack was written for a plugin that had stopped")
		}
	})
}

func TestALateFetchTheRuleEndsIsStoppedOnItsLineUntilTryAgain(t *testing.T) {
	r := newLateRig(t)
	r.mu.Lock()
	r.fail = &pluginhost.FetchStatusError{Status: 404}
	r.mu.Unlock()
	if _, err := r.a.applyConfigChange(map[string]any{"plugins.settings." + lateID + ".tts_language": "fr"}); err != nil {
		t.Fatalf("choosing French: %v", err)
	}
	eventually(t, "the line to say stopped", func() bool {
		st := r.state(t)
		return st != nil && st.State == dashboard.SettingModelsStopped && strings.Contains(st.Remedy, "Nothing is at the address") && st.RetryAt == ""
	})
	before := r.fetches()
	r.a.reconcileLateModels()
	time.Sleep(100 * time.Millisecond)
	if r.fetches() != before {
		t.Fatalf("a sweep reopened a stopped fetch: %d -> %d", before, r.fetches())
	}
	r.mu.Lock()
	r.fail = nil
	r.mu.Unlock()
	r.a.lateModels.Retry(lateID)
	eventually(t, "the pack on disk after Try again", r.packOnDisk)
}
