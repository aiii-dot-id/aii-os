package packagefmt

// .
// .
// .
// .
// .

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginworker"
)

// .
// .
// .
// .
// .
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

	// .
	// .
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
