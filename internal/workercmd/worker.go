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

	"github.com/aiii-dot-id/aii-os/internal/bbb"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker"
)

func Run(args []string) int {
	fs := flag.NewFlagSet("aii-plugin-worker", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	memoryMax := fs.Uint64("memory-max", pluginworker.DefaultMemoryMaxBytes,
		"guest memory ceiling in bytes (default: the 64 MiB envelope)")
	invokeTimeout := fs.Duration("invoke-timeout", bbb.DefaultInvokeTimeout,
		"deadline per plugin-invoke; on expiry the guest is killed and the worker exits for restart")
	forward := fs.Bool("forward", false,
		"forward guest-outgoing aiii:bbb/bbb calls upstream as BBB request frames on stdout (the supervised broker channel) instead of the deny-all stub")
	moduleSHA := fs.String("module-sha256", "",
		"the verified digest of the module (sha256:<hex>); the bytes loaded must hash to it or the worker refuses")
	describe := fs.Bool("describe", false,
		"print the module's own descriptor emission (its aiii-plugin-describe export) to stdout and exit: the packager's read of an artifact's surface without running the author's program on the host")
	descriptorProof := fs.String("descriptor-proof", "", "verify the host's JSON-value descriptor digest before readiness, in a deny-all instance")
	startupTimeout := fs.Duration("startup-timeout", 0, "bound module admission and descriptor proof together; zero uses invoke-timeout")
	if err := fs.Parse(args); err != nil {
		return bbb.ExitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: aii-plugin-worker [-memory-max bytes] [-invoke-timeout dur] [-forward] <module.wasm>")
		return bbb.ExitUsage
	}
	if *invokeTimeout <= 0 || *startupTimeout < 0 {
		fmt.Fprintln(os.Stderr, "worker deadlines must be positive")
		return bbb.ExitUsage
	}
	if *startupTimeout == 0 {
		*startupTimeout = *invokeTimeout
	}
	modulePath := fs.Arg(0)

	wasmBytes, err := os.ReadFile(modulePath)
	if err != nil {
		fatalf("load", "read module: %v", err)
		return bbb.ExitAdmission
	}

	if *moduleSHA != "" {
		sum := sha256.Sum256(wasmBytes)
		if got := "sha256:" + hex.EncodeToString(sum[:]); got != *moduleSHA {
			fatalf("load", "module digest mismatch: loaded %s, host verified %s — the artifact changed between verification and load", got, *moduleSHA)
			return bbb.ExitAdmission
		}
	}

	cfg := pluginworker.Config{MemoryMaxBytes: *memoryMax}
	if *forward {
		cfg.Dispatcher = newForwardDispatcher()
	}

	loadCtx, cancel := context.WithTimeout(context.Background(), *startupTimeout)
	defer cancel()
	if *descriptorProof != "" {
		present, err := pluginworker.ProveDescriptor(loadCtx, wasmBytes, *memoryMax, *descriptorProof)
		if err != nil {
			fatalf("descriptor", "%v", err)
			var mismatch *pluginworker.DescriptorMismatchError
			if errors.As(err, &mismatch) {
				return bbb.ExitDescriptorMismatch
			}
			return bbb.ExitDescriptorUnasked
		}
		if !present {
			fmt.Fprintf(os.Stderr, "aii-plugin-worker: descriptor unproven — no %s export\n", pluginworker.ExportDescribe)
		} else if *descriptorProof == pluginworker.DescriptorMultiple {
			fmt.Fprintln(os.Stderr, "aii-plugin-worker: descriptor unproven — multiple core interfaces and one describe export")
		}
	}
	m, err := pluginworker.Load(loadCtx, wasmBytes, cfg)
	cancel()
	if err != nil {
		fatalf("load", "%v", err)
		return bbb.ExitAdmission
	}
	defer m.Close(context.Background())

	if *describe {

		dctx, dcancel := context.WithTimeout(context.Background(), *invokeTimeout)
		out, ok, derr := m.Describe(dctx)
		dcancel()
		if derr != nil {
			fatalf("describe", "%v", derr)
			return bbb.ExitInvocation
		}
		if !ok {
			fatalf("describe", "the module exports no %s", pluginworker.ExportDescribe)
			return bbb.ExitAdmission
		}
		if _, werr := os.Stdout.Write(out); werr != nil {
			fatalf("describe", "stdout: %v", werr)
			return bbb.ExitStream
		}
		return bbb.ExitClean
	}

	fmt.Fprintf(os.Stderr, "aii-plugin-worker: event=ready module=%s artifact_class=%s memory_max=%d invoke_timeout=%s forward=%t bbb_protocol_version=%d descriptor_proof=%s\n",
		modulePath, m.ArtifactClass(), *memoryMax, *invokeTimeout, *forward, pluginworker.RequiredProtocolVersion, *descriptorProof)

	for {
		frame, err := bbb.ReadFrame(os.Stdin, bbb.MaxControlFrameBytes)
		if errors.Is(err, io.EOF) {

			fmt.Fprintln(os.Stderr, "aii-plugin-worker: event=shutdown reason=stdin-eof")
			return bbb.ExitClean
		}
		if err != nil {

			fatalf("stream", "read frame: %v", err)
			return bbb.ExitStream
		}

		ctx, cancel := context.WithTimeout(context.Background(), *invokeTimeout)
		resp, err := m.Invoke(ctx, frame)
		cancel()
		if err != nil {
			fatalf("invoke", "%v", err)
			return bbb.ExitInvocation
		}
		if len(resp) == 0 {

			fatalf("invoke", "guest returned an empty response frame")
			return bbb.ExitInvocation
		}
		if err := bbb.WriteFrame(os.Stdout, resp, bbb.MaxControlFrameBytes); err != nil {
			fatalf("stream", "write frame: %v", err)
			return bbb.ExitStream
		}
	}
}

func fatalf(stage, format string, args ...any) {
	fmt.Fprintf(os.Stderr, "aii-plugin-worker: event=fatal stage=%s detail=%q\n",
		stage, fmt.Sprintf(format, args...))
}
