package packagefmt

// The composition seam test: every layer here is hostile-tested alone,
// but a package that VERIFIES must also RUN — steps 2 and 3 composed.
// This is the earned lesson from the 2026-08-18 review pass (two bugs,
// each subsystem correct alone, wrong composed): when a new layer
// lands, exercise the seam, not just the layer.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginworker"
)

// TestPipelineVerifyThenLoadInvoke builds a T0 package whose variant
// artifact is the real echo guest, verifies it exactly as an installer
// would, then hands the SAME verified bytes to the worker wall and
// round-trips a frame. A drift between what packagefmt admits and what
// pluginworker runs fails here and nowhere else.
func TestPipelineVerifyThenLoadInvoke(t *testing.T) {
	echoWasm, err := os.ReadFile(filepath.Join("..", "pluginworker", "testdata", "echo.wasm"))
	if err != nil {
		t.Fatalf("echo guest fixture: %v", err)
	}

	files := map[string][]byte{
		"interfaces/channel.control.v1.schema.json": []byte(`{"interface":"channel.control","v":1}`),
		"variants/linux-x86_64-wasm/plugin.wasm":    echoWasm,
	}
	manifest := buildManifestJSON("org.example.echo", "0.1.0",
		[]variantSpec{{
			id: "linux-x86_64-wasm", platform: "linux", arch: "x86_64",
			topology: "full_identity_host", runtime: "wasm_component", profile: "wasm_sandbox",
			entrypoint: "variants/linux-x86_64-wasm/plugin.wasm",
		}},
		files, nil)
	pkg := buildPkg(t, pkgSpec{
		root: "org.example.echo-0.1.0", manifest: manifest, installFiles: files,
	})

	result := mustVerify(t, pkg, TrustRoots{})
	if result.Tier != TierT0 {
		t.Fatalf("tier = %s, want T0", result.Tier)
	}

	// The verified artifact bytes — the exact member the digest covered
	// — are what the worker loads. No re-read, no second source.
	mod, err := pluginworker.Load(context.Background(), echoWasm, pluginworker.Config{})
	if err != nil {
		t.Fatalf("verified artifact failed the worker wall: %v", err)
	}
	defer mod.Close(context.Background())

	frame := []byte(`{"jsonrpc":"2.0","id":1,"method":"invoke.call","params":{"operation":"echo"}}`)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	reply, err := mod.Invoke(ctx, frame)
	if err != nil {
		t.Fatalf("invoke through the composed pipeline: %v", err)
	}
	if string(reply) != string(frame) {
		t.Fatalf("echo drifted across the seam:\n got %q\nwant %q", reply, frame)
	}
}
