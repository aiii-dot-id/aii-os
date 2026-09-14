package pluginworker

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
var echoFrame = []byte(`{"jsonrpc":"2.0","id":"1","method":"plugin.invoke","params":{"operation":"echo"}}`)

func BenchmarkComponentEchoThroughTheHost(b *testing.B) {
	wasm, err := os.ReadFile(filepath.Join("testdata", "component-echo.wasm"))
	if err != nil {
		b.Fatal(err)
	}
	m, err := Load(context.Background(), wasm, Config{})
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = m.Close(context.Background()) }()
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := m.Invoke(ctx, echoFrame); err != nil {
			b.Fatal(err)
		}
	}
}

// .
// .
func BenchmarkEchoInProcess(b *testing.B) {
	b.ReportAllocs()
	out := make([]byte, len(echoFrame))
	for i := 0; i < b.N; i++ {
		copy(out, echoFrame)
	}
	_ = out
}
