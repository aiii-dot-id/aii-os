// aii-plugin-worker runs ONE untrusted WASM plugin behind the portable
// wall (docs/PLUGIN_FRAMEWORK.md §11, build-order step 3): wazero
// isolates the plugin from this process; the step-5 supervisor's
// process boundary isolates this process from the resident. All logic
// lives in internal/pluginworker; this is the flag-and-streams wrapper.
//
// Transport (DELTA_D1 D1-1, adopted for the WASM worker): framed BBB on
// the standard streams — the host writes request frames to stdin, the
// worker writes response frames to stdout, 4-byte big-endian length
// prefix, 1 MiB ceiling BOTH directions, EOF is disconnect. stderr is
// reserved for out-of-band diagnostics and NEVER carries frames: a
// crashed worker cannot report its own crash in-band, so the startup
// banner and the final fatal line live here for the supervisor
// (PLUGIN_THREAT_MODEL.md §7).
//
// With -forward the pair is fully duplex: guest-outgoing aiii:bbb/bbb
// calls ride upstream as REQUEST frames on stdout and their responses
// return on stdin, nested inside the in-flight downstream invocation
// (forward.go owns the discipline). Without it, guest hostcalls keep
// the deny-all stub — the zero-capability default.
//
// Trust boundary: the HOST verifies the package via internal/packagefmt
// BEFORE launching this worker with a module path; the worker performs
// no verification. Its only filesystem access is reading that one path;
// it opens no sockets and no other files.
//
// Exit codes (the supervisor's restart signal):
//
//	0  clean shutdown (stdin EOF)
//	1  usage error
//	2  module load/admission failure
//	3  fatal invocation failure (trap, timeout, resource kill, ABI or
//	   frame-budget violation) — the module is unusable, restart it
//	4  stream failure (framing violation or broken pipe)
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker"
)

func main() { os.Exit(run()) }

func run() int {
	memoryMax := flag.Uint64("memory-max", pluginworker.DefaultMemoryMaxBytes,
		"guest memory ceiling in bytes (default: the ADR-033 64 MiB envelope)")
	invokeTimeout := flag.Duration("invoke-timeout", 30*time.Second,
		"deadline per plugin-invoke; on expiry the guest is killed and the worker exits for restart (DELTA_D1 N-8)")
	forward := flag.Bool("forward", false,
		"forward guest-outgoing aiii:bbb/bbb calls upstream as BBB request frames on stdout (the supervised broker channel) instead of the deny-all stub")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: aii-plugin-worker [-memory-max bytes] [-invoke-timeout dur] [-forward] <module.wasm>")
		return 1
	}
	modulePath := flag.Arg(0)

	wasmBytes, err := os.ReadFile(modulePath)
	if err != nil {
		fatalf("load", "read module: %v", err)
		return 2
	}

	// Forward mode swaps the deny-all stub for the upstream channel
	// (forward.go). Without the flag, guest hostcalls stay denied —
	// the zero-capability posture is the DEFAULT, an operator's
	// supervisor opts in per launch.
	cfg := pluginworker.Config{MemoryMaxBytes: *memoryMax}
	if *forward {
		cfg.Dispatcher = newForwardDispatcher()
	}

	// Admission runs guest code (protocol-version + smoke), so it gets
	// the same deadline protection as an invoke.
	loadCtx, cancel := context.WithTimeout(context.Background(), *invokeTimeout)
	m, err := pluginworker.Load(loadCtx, wasmBytes, cfg)
	cancel()
	if err != nil {
		fatalf("load", "%v", err)
		return 2
	}
	defer m.Close(context.Background())

	// The banner is the supervisor's readiness mark — out-of-band,
	// never a frame. No key material, ever.
	fmt.Fprintf(os.Stderr, "aii-plugin-worker: event=ready module=%s artifact_class=%s memory_max=%d invoke_timeout=%s forward=%t bbb_protocol_version=%d\n",
		modulePath, m.ArtifactClass(), *memoryMax, *invokeTimeout, *forward, pluginworker.RequiredProtocolVersion)

	for {
		frame, err := bbb.ReadFrame(os.Stdin, bbb.MaxControlFrameBytes)
		if errors.Is(err, io.EOF) {
			// Connection lifetime is process lifetime (D1-1 rule 3):
			// EOF is the clean disconnect.
			fmt.Fprintln(os.Stderr, "aii-plugin-worker: event=shutdown reason=stdin-eof")
			return 0
		}
		if err != nil {
			// Oversize or desynced input: the stream is dead and has
			// no resync (AUDIT §2.2) — die and let the supervisor
			// restart.
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
			// The JSON layer above never produces an empty payload
			// (AUDIT §2.3); a guest emitting one broke the protocol.
			fatalf("invoke", "guest returned an empty response frame")
			return 3
		}
		if err := bbb.WriteFrame(os.Stdout, resp, bbb.MaxControlFrameBytes); err != nil {
			fatalf("stream", "write frame: %v", err)
			return 4
		}
	}
}

// fatalf writes the single out-of-band crash-telemetry line the
// supervisor keys on (threat model §7). One line, no frames, stderr.
func fatalf(stage, format string, args ...any) {
	fmt.Fprintf(os.Stderr, "aii-plugin-worker: event=fatal stage=%s detail=%q\n",
		stage, fmt.Sprintf(format, args...))
}
