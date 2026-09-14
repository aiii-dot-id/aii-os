package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
)

// .
// .
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
