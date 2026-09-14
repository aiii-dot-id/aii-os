package pluginworker

import (
	"bytes"
	"context"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
)

// .
// .
// .
func TestDescribeExportIsLiftedLikeAnInvoke(t *testing.T) {
	desc := []byte(`[{"id":"ping","summary":"answers","input":"","output":"","effects":"read.internal","capabilities":[]}]`)
	m, err := Load(context.Background(), wasmgen.DescribingResponder(desc), Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(context.Background())
	got, ok, err := m.Describe(context.Background())
	if err != nil || !ok || !bytes.Equal(got, desc) {
		t.Fatalf("describe = %q ok=%v err=%v, want the artifact's own bytes", got, ok, err)
	}
	// .
	if _, err := m.Invoke(context.Background(), []byte(`{"jsonrpc":"2.0","id":"h1","method":"invoke.call","params":{}}`)); err != nil {
		t.Fatalf("invoke after describe: %v", err)
	}

	plain, err := Load(context.Background(), wasmgen.Responder(), Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close(context.Background())
	if got, ok, err := plain.Describe(context.Background()); ok || err != nil || got != nil {
		t.Fatalf("a module without the export must answer ok=false, got %q %v %v", got, ok, err)
	}
}
