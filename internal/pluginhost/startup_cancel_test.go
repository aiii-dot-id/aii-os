package pluginhost

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

// .
// .
// .
// .
// .
// .
// .

func neverReadyNative(t *testing.T, id string) (*packagefmt.Result, packagefmt.Variant, []byte) {
	t.Helper()
	skipWhereTheSandboxCannotBeEstablished(t)
	raw, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	v := packagefmt.Variant{VariantID: "native", Entrypoint: "variants/native/child" + exeSuffix}
	res := &packagefmt.Result{Tier: packagefmt.TierT3, Manifest: &packagefmt.Manifest{ID: id},
		FileDigests: map[string]string{v.Entrypoint: digestOf(raw)}}
	return res, v, raw
}

func assertAbandoned(t *testing.T, err error, took time.Duration, ranSup bool) {
	t.Helper()
	if ranSup {
		t.Fatal("an abandoned start handed back a running supervisor")
	}
	if took > 20*time.Second {
		t.Fatalf("the start served out its allowance anyway: %s", took)
	}
	var cancelled *supervisor.StartCancelledError
	if !errors.As(err, &cancelled) || !errors.Is(err, context.Canceled) {
		t.Fatalf("an abandoned start must refuse as cancelled, got: %v", err)
	}
	// .
	var exit *supervisor.ChildExitError
	if errors.As(err, &exit) {
		t.Fatalf("a cancellation was reported as the child's exit: %v", exit)
	}
}

func TestANativeStartIsAbandonedWhenItsAttemptIs(t *testing.T) {
	const id = "org.example.abandoned-native"
	res, v, raw := neverReadyNative(t, id)
	opts := &Options{ReadyTimeout: map[string]time.Duration{id: 10 * time.Minute}}
	t.Run("during readiness", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		go func() { time.Sleep(300 * time.Millisecond); cancel() }()
		started := time.Now()
		sup, dir, _, err := startSupervisedNativeWith(ctx, res, &v, raw, nil, opts, &AcceleratorProfile{Backend: "cpu"}, "", false, nil)
		if sup != nil {
			defer sup.Close()
		}
		defer os.RemoveAll(dir)
		assertAbandoned(t, err, time.Since(started), sup != nil)
	})
	t.Run("already abandoned: no child at all", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		started := time.Now()
		sup, dir, _, err := startSupervisedNativeWith(ctx, res, &v, raw, nil, opts, &AcceleratorProfile{Backend: "cpu"}, "", false, nil)
		if sup != nil {
			defer sup.Close()
		}
		defer os.RemoveAll(dir)
		if sup != nil || err == nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("a start under a finished context: sup=%v err=%v", sup != nil, err)
		}
		if took := time.Since(started); took > 10*time.Second {
			t.Fatalf("it took %s to refuse a start nobody wanted", took)
		}
	})
}

func TestAWASMStartIsAbandonedWhenItsAttemptIs(t *testing.T) {
	raw, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	const id = "org.example.abandoned-wasm"
	v := packagefmt.Variant{VariantID: "wasm", Entrypoint: "variants/wasm/plugin.wasm"}
	res := &packagefmt.Result{Tier: packagefmt.TierT1, Manifest: &packagefmt.Manifest{ID: id},
		FileDigests: map[string]string{v.Entrypoint: digestOf(raw)}}
	// .
	// .
	opts := &Options{WorkerBinary: fakechildBin, WorkerArgs: []string{"session"}, ReadyTimeout: map[string]time.Duration{id: 10 * time.Minute}}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(300 * time.Millisecond); cancel() }()
	started := time.Now()
	sup, dir, err := startSupervisedWASM(ctx, res, &v, raw, nil, opts)
	if sup != nil {
		defer sup.Close()
	}
	defer os.RemoveAll(dir)
	assertAbandoned(t, err, time.Since(started), sup != nil)
}
