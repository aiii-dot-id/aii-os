// Command gen writes the pluginworker fixture guests into -out.
// Invoked by `go generate ./internal/pluginworker`; the fixtures'
// source of truth is the wasmgen package.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
)

func main() {
	out := flag.String("out", "testdata", "directory to write fixture .wasm files into")
	flag.Parse()

	for name, bytes := range wasmgen.Fixtures() {
		path := filepath.Join(*out, name)
		if err := os.WriteFile(path, bytes, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "gen: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("gen: wrote %s (%d bytes)\n", path, len(bytes))
	}
}
