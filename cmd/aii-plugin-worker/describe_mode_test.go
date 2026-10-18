package main

import (
	"bytes"
	"context"
	"errors"
	"github.com/aiii-dot-id/aii-os/internal/bbb"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginworker"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
)

// Describe mode prints the artifact's own account and nothing else to
// stdout, and refuses by exit code when the module has none.
func TestDescribeModePrintsTheArtifactsOwnAccount(t *testing.T) {
	desc := []byte(`[{"id":"ping","summary":"answers","input":"","output":"","effects":"read.internal","capabilities":[]}]`)
	dir := t.TempDir()
	described := filepath.Join(dir, "described.wasm")
	if err := os.WriteFile(described, wasmgen.DescribingResponder(desc), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(workerBin, "-describe", described).Output()
	if err != nil || !bytes.Equal(out, desc) {
		t.Fatalf("describe mode must print the account exactly: %v %q", err, out)
	}
	plain := filepath.Join(dir, "plain.wasm")
	if err := os.WriteFile(plain, wasmgen.Responder(), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(workerBin, "-describe", plain)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		t.Fatal("a module without the export must refuse with a nonzero exit")
	} else if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 2 || !bytes.Contains(stderr.Bytes(), []byte("exports no")) {
		t.Fatalf("want exit 2 naming the missing export, got %v: %s", err, stderr.String())
	}
}

func TestWorkerDescriptorProofPrecedesReadiness(t *testing.T) {
	digest, err := pluginworker.DescriptorDigest([]byte(`[]`))
	if err != nil {
		t.Fatal(err)
	}

	const answers, never = "2m", "100ms"
	for _, tc := range []struct {
		name   string
		guest  []byte
		code   int
		budget string
	}{
		{"mismatch", wasmgen.DescribingResponder([]byte(`[1]`)), bbb.ExitDescriptorMismatch, answers},
		{"trap", wasmgen.TrappingDescriber(), bbb.ExitDescriptorUnasked, answers},
		{"loop", wasmgen.LoopingDescriber(), bbb.ExitDescriptorUnasked, never},
		{"initializer", wasmgen.LoopingInitializer(), bbb.ExitDescriptorUnasked, never},
		{"match", wasmgen.DescribingResponder([]byte(`[]`)), 0, answers},
		{"absent", wasmgen.Responder(), 0, answers},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "proof.wasm")
			if err := os.WriteFile(path, tc.guest, 0600); err != nil {
				t.Fatal(err)
			}
			// The worker ends itself at its startup budget. The test's own
			// limit only catches a worker that ignores it, and stands well
			// clear of the budget and of a slow first start.
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, workerBin, "-descriptor-proof="+digest, "-startup-timeout="+tc.budget, path)
			out, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("worker ignored its startup deadline of %s: %v", tc.budget, err)
			}
			code := 0
			if err != nil {
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Fatal(err)
				}
				code = exit.ExitCode()
			}
			if code != tc.code {
				t.Fatalf("exit=%d want=%d: %s", code, tc.code, out)
			}
			ready := bytes.Contains(out, []byte("event=ready"))
			if ready != (tc.code == 0) {
				t.Fatalf("readiness escaped proof: %s", out)
			}
		})
	}
}
