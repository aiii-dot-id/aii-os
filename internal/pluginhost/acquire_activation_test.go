//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// The native lane with an Acquirer: an activation whose models are not
// on disk returns typed at once — it does not sit in a download — and
// the same activation succeeds after the Acquirer has said ready. The
// options carry NO fetcher: the store's writer fetches, never the
// activation, and this way it never could.
func TestActivationHandsAbsentMaterialToTheAcquirerAndFollowsIt(t *testing.T) {
	skipWhereNativeIsRefused(t)
	skipWhereTheSandboxCannotBeEstablished(t)
	roots, role := platformRootsForTest(t)
	child, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	const id = "org.example.acquired"
	fx := newModelFixture(t, "stt.bin", 65536)
	fx.block = make(chan struct{})
	rt := newRuntimeFixtureWith(t, "# acquired\n")
	models, err := json.Marshal([]ModelDecl{fx.decl})
	if err != nil {
		t.Fatal(err)
	}
	pkg := nativePackageWithModels(t, id, child, role, runtimesJSON(t, rt), models)
	store := openStore(t)
	ready := make(chan string, 4)
	capture := logsink.CaptureForTest(t)
	q := NewAcquirer(AcquirerConfig{Materialize: storeMaterializer(store), Fetcher: byURL(map[string]ModelFetcher{fx.decl.URL: fx.fetch, rt.decl.URL: rt.fetch}),
		After: nowAfter, Ready: func(id string) { ready <- id }})
	q.Attach(context.Background())
	opts := supervisedOpts(Options{Roots: roots, StoreDir: store.Dir, Acquirer: q})

	actCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	started := time.Now()
	_, err = Activate(actCtx, pkg, newRegistry(t), opts)
	cancel()
	var acquiring *AcquiringError
	if !errors.As(err, &acquiring) {
		t.Fatalf("an activation without its models must refuse typed, got: %v\n%s", err, capture.String())
	}
	// The download is held open (fx.block) for the whole activation, so one
	// that waited on it would run to its 30 s context. The bound is half of
	// that: 5 s was a timing claim a loaded host broke, not a claim about
	// waiting.
	if took := time.Since(started); took > 15*time.Second {
		t.Fatalf("the activation waited on the download: %s", took)
	}
	// Two files: the model and the runtime archive, counted as one (G2).
	if st := acquiring.Status; st.FilesTotal != 2 || st.FilesPresent != 0 || !st.RuntimeDeclared || st.RuntimePresent || st.Version != "0.1.0" {
		t.Fatalf("status: %+v", st)
	}
	// The download is the Acquirer's, running now: release it and the
	// word comes.
	waitFor(t, "the acquirer's fetch", func() bool { c, _ := fx.seen(); return c == 1 })
	close(fx.block)
	select {
	case got := <-ready:
		if got != id {
			t.Fatalf("ready for %q", got)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("the acquirer never said ready; log:\n%s", capture.String())
	}
	// The same activation now finds everything present and verified.
	actCtx, cancel = context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ap, err := Activate(actCtx, pkg, newRegistry(t), opts)
	if err != nil {
		if strings.Contains(err.Error(), "native lane unavailable") || strings.Contains(err.Error(), "cannot contain") {
			t.Skipf("the launch itself needs a host that can contain a native child: %v", err)
		}
		t.Fatalf("activation after ready: %v\n%s", err, capture.String())
	}
	defer func() { _ = ap.Deactivate(context.Background()) }()
	if ap.ModelsDir == "" || ap.RuntimeRoot == "" {
		t.Fatal("the activation lost the models directory or the runtime root")
	}

	if len(ap.ModelDecls) != 1 || ap.ModelDecls[0].Name != fx.decl.Name {
		t.Fatalf("the activation did not keep the package's model declarations: %+v", ap.ModelDecls)
	}
	if got, rerr := os.ReadFile(filepath.Join(ap.ModelsDir, "stt.bin")); rerr != nil || !bytes.Equal(got, fx.data) {
		t.Fatalf("the model on disk: %v", rerr)
	}
	if c, _ := fx.seen(); c != 1 {
		t.Fatalf("the activation fetched on its own: %d calls", c)
	}
}
