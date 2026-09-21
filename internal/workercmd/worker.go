// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
package workercmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker"
)

// .
// .
// .
func Run(args []string) int {
	fs := flag.NewFlagSet("aii-plugin-worker", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	memoryMax := fs.Uint64("memory-max", pluginworker.DefaultMemoryMaxBytes,
		"guest memory ceiling in bytes (default: the 64 MiB envelope)")
	invokeTimeout := fs.Duration("invoke-timeout", 30*time.Second,
		"deadline per plugin-invoke; on expiry the guest is killed and the worker exits for restart")
	forward := fs.Bool("forward", false,
		"forward guest-outgoing aiii:bbb/bbb calls upstream as BBB request frames on stdout (the supervised broker channel) instead of the deny-all stub")
	moduleSHA := fs.String("module-sha256", "",
		"the verified digest of the module (sha256:<hex>); the bytes loaded must hash to it or the worker refuses")
	describe := fs.Bool("describe", false,
		"print the module's own descriptor emission (its aiii-plugin-describe export) to stdout and exit: the packager's read of an artifact's surface without running the author's program on the host")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: aii-plugin-worker [-memory-max bytes] [-invoke-timeout dur] [-forward] <module.wasm>")
		return 1
	}
	modulePath := fs.Arg(0)

	wasmBytes, err := os.ReadFile(modulePath)
	if err != nil {
		fatalf("load", "read module: %v", err)
		return 2
	}

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if *moduleSHA != "" {
		sum := sha256.Sum256(wasmBytes)
		if got := "sha256:" + hex.EncodeToString(sum[:]); got != *moduleSHA {
			fatalf("load", "module digest mismatch: loaded %s, host verified %s — the artifact changed between verification and load", got, *moduleSHA)
			return 2
		}
	}

	// .
	// .
	// .
	// .
	cfg := pluginworker.Config{MemoryMaxBytes: *memoryMax}
	if *forward {
		cfg.Dispatcher = newForwardDispatcher()
	}

	// .
	// .
	loadCtx, cancel := context.WithTimeout(context.Background(), *invokeTimeout)
	m, err := pluginworker.Load(loadCtx, wasmBytes, cfg)
	cancel()
	if err != nil {
		fatalf("load", "%v", err)
		return 2
	}
	defer m.Close(context.Background())

	if *describe {
		// .
		// .
		// .
		// .
		dctx, dcancel := context.WithTimeout(context.Background(), *invokeTimeout)
		out, ok, derr := m.Describe(dctx)
		dcancel()
		if derr != nil {
			fatalf("describe", "%v", derr)
			return 3
		}
		if !ok {
			fatalf("describe", "the module exports no %s", pluginworker.ExportDescribe)
			return 2
		}
		if _, werr := os.Stdout.Write(out); werr != nil {
			fatalf("describe", "stdout: %v", werr)
			return 4
		}
		return 0
	}

	// .
	// .
	fmt.Fprintf(os.Stderr, "aii-plugin-worker: event=ready module=%s artifact_class=%s memory_max=%d invoke_timeout=%s forward=%t bbb_protocol_version=%d\n",
		modulePath, m.ArtifactClass(), *memoryMax, *invokeTimeout, *forward, pluginworker.RequiredProtocolVersion)

	for {
		frame, err := bbb.ReadFrame(os.Stdin, bbb.MaxControlFrameBytes)
		if errors.Is(err, io.EOF) {
			// .
			// .
			fmt.Fprintln(os.Stderr, "aii-plugin-worker: event=shutdown reason=stdin-eof")
			return 0
		}
		if err != nil {
			// .
			// .
			// .
			fatalf("stream", "read frame: %v", err)
			return 4
		}

		ctx, cancel := context.WithTimeout(context.Background(), *invokeTimeout)
		resp, err := m.Invoke(ctx, frame)
		cancel()
		if err != nil {
			fatalf("invoke", "%v", err)
			return 3
		}
		if len(resp) == 0 {
			// .
			// .
			fatalf("invoke", "guest returned an empty response frame")
			return 3
		}
		if err := bbb.WriteFrame(os.Stdout, resp, bbb.MaxControlFrameBytes); err != nil {
			fatalf("stream", "write frame: %v", err)
			return 4
		}
	}
}

// .
// .
func fatalf(stage, format string, args ...any) {
	fmt.Fprintf(os.Stderr, "aii-plugin-worker: event=fatal stage=%s detail=%q\n",
		stage, fmt.Sprintf(format, args...))
}
