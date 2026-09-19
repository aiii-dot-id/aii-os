package pluginhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// .
// .
// .
// .
// .
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
	dir := t.TempDir()
	ready := make(chan string, 4)
	sink := &logSink{}
	logf := log.New(sink, "", 0).Printf
	q := NewAcquirer(AcquirerConfig{ModelFetcher: fx.fetch, RuntimeFetcher: rt.fetch, Backoff: noBackoff, Logf: logf, Ready: func(id string) { ready <- id }})
	q.Attach(context.Background())
	opts := supervisedOpts(Options{Roots: roots, PluginModelsDir: filepath.Join(dir, "models"), PluginRuntimeDir: filepath.Join(dir, "runtime"),
		RuntimeRoots: NewRuntimeRoots(), Acquirer: q, Log: log.New(sink, "", 0)})

	actCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	started := time.Now()
	_, err = Activate(actCtx, pkg, newRegistry(t), opts)
	cancel()
	var acquiring *AcquiringError
	if !errors.As(err, &acquiring) {
		t.Fatalf("an activation without its models must refuse typed, got: %v\n%s", err, sink.String())
	}
	if took := time.Since(started); took > 5*time.Second {
		t.Fatalf("the activation waited on the download: %s", took)
	}
	if st := acquiring.Status; st.FilesTotal != 1 || st.FilesPresent != 0 || !st.RuntimeDeclared || st.RuntimePresent || st.Version != "0.1.0" {
		t.Fatalf("status: %+v", st)
	}
	// .
	// .
	waitFor(t, "the acquirer's fetch", func() bool { c, _ := fx.seen(); return c == 1 })
	close(fx.block)
	select {
	case got := <-ready:
		if got != id {
			t.Fatalf("ready for %q", got)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("the acquirer never said ready; log:\n%s", sink.String())
	}
	// .
	actCtx, cancel = context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ap, err := Activate(actCtx, pkg, newRegistry(t), opts)
	if err != nil {
		if strings.Contains(err.Error(), "native lane unavailable") || strings.Contains(err.Error(), "cannot contain") {
			t.Skipf("the launch itself needs a host that can contain a native child: %v", err)
		}
		t.Fatalf("activation after ready: %v\n%s", err, sink.String())
	}
	defer func() { _ = ap.Deactivate(context.Background()) }()
	if ap.ModelsDir == "" || ap.RuntimeRoot == "" {
		t.Fatal("the activation lost the models directory or the runtime root")
	}
	if got, rerr := os.ReadFile(filepath.Join(ap.ModelsDir, "stt.bin")); rerr != nil || !bytes.Equal(got, fx.data) {
		t.Fatalf("the model on disk: %v", rerr)
	}
	if c, _ := fx.seen(); c != 1 {
		t.Fatalf("the activation fetched on its own: %d calls", c)
	}
}
