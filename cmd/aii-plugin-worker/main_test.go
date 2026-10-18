package main

// End-to-end: the real binary, real pipes, checked-in fixture guests.
// The library's containment behavior is proven in internal/pluginworker;
// here the contract under test is the WORKER's: framed stdio in both
// directions, stderr strictly out-of-band, and the exit codes the
// step-5 supervisor will key restarts on.

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
)

var workerBin string

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "aii-plugin-worker-test")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	workerBin = filepath.Join(tmp, "aii-plugin-worker")
	if runtime.GOOS == "windows" {
		// Windows will not execute a file without a recognised
		// extension, so the built binary is unreachable without it —
		// the same suffix internal/pluginhost's TestMain already adds.
		workerBin += ".exe"
	}
	code := 0
	out, err := exec.Command("go", "build", "-o", workerBin, ".").CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build worker binary: %v\n%s", err, out)
		code = 1
	}
	if code == 0 {
		code = m.Run()
	}
	if err := os.RemoveAll(tmp); err != nil {
		fmt.Fprintf(os.Stderr, "remove worker test binary: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

// Re-enter the actual TestMain, with only its Go build replaced, and read
// the temporary directory it was given afterwards: nothing of its own may
// be left there when the tests pass, when one fails, or when the build
// does. No worker is launched: the selected test is the pass/fail boundary.
func TestTheWorkerTestsLeaveNothingBehind(t *testing.T) {
	if probe := os.Getenv("AII_PREPARATION_PROBE"); probe != "" {
		if probe == "fail" {
			t.Fatal("preparation fixture test failure")
		}
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("the build stand-in is a POSIX shell; native Windows runs the real build")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, buildExit, probe string
		wantExit               int
	}{
		{"pass", "0", "pass", 0},
		{"test failure", "0", "fail", 1},
		{"build failure", "7", "pass", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, temporary := t.TempDir(), t.TempDir()
			// The stand-in writes where the build was told to (go build -o
			// PATH .), so a run that built has a file of its own to remove.
			script := "#!/bin/sh\ntouch \"$3\"\necho preparation-fixture-build >&2\nexit \"$AII_PREPARATION_EXIT\"\n"
			if err := os.WriteFile(filepath.Join(root, "go"), []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, "-test.run=^TestTheWorkerTestsLeaveNothingBehind$", "-test.v")
			cmd.WaitDelay = 5 * time.Second
			cmd.Env = append(os.Environ(), "PATH="+root+string(os.PathListSeparator)+os.Getenv("PATH"),
				"TMPDIR="+temporary, "AII_PREPARATION_EXIT="+tc.buildExit, "AII_PREPARATION_PROBE="+tc.probe)
			output, err := cmd.CombinedOutput()
			code := 0
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else if err != nil {
				t.Fatalf("run TestMain: %v\n%s", err, output)
			}
			if ctx.Err() != nil || code != tc.wantExit {
				t.Fatalf("TestMain exit=%d, want %d (deadline=%v):\n%s", code, tc.wantExit, ctx.Err(), output)
			}
			if tc.buildExit != "0" && !strings.Contains(string(output), "preparation-fixture-build") {
				t.Errorf("build diagnostic lost: %s", output)
			}
			if tc.probe == "fail" && !strings.Contains(string(output), "preparation fixture test failure") {
				t.Errorf("test diagnostic lost: %s", output)
			}
			left, err := os.ReadDir(temporary)
			if err != nil || len(left) != 0 {
				t.Errorf("TestMain left owned files: %v (read error=%v)", left, err)
			}
		})
	}
}

func fixture(name string) string {
	return filepath.Join("..", "..", "internal", "pluginworker", "testdata", name)
}

type workerProc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr *bytes.Buffer
	done   chan error
}

func startWorker(t *testing.T, args ...string) *workerProc {
	t.Helper()
	return startWorkerWithin(t, 30*time.Second, args...)
}

func startWorkerWithin(t *testing.T, timeout time.Duration, args ...string) *workerProc {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, workerBin, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	w := &workerProc{cmd: cmd, stdin: stdin, stdout: stdout, stderr: stderr, done: make(chan error, 1)}
	go func() { w.done <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-w.done })
	return w
}

// exitCode waits for the worker and returns its exit code.
func (w *workerProc) exitCode(t *testing.T) int {
	t.Helper()
	err := <-w.done
	w.done <- err // allow the cleanup drain
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	t.Fatalf("worker did not exit normally: %v", err)
	return -1
}

func TestWorkerEchoRoundTripOverPipes(t *testing.T) {
	w := startWorker(t, fixture("echo.wasm"))
	for i, frame := range [][]byte{
		[]byte(`{"jsonrpc":"2.0","id":"1","method":"plugin.invoke","params":{"n":1}}`),
		[]byte(`{"jsonrpc":"2.0","id":"2","method":"plugin.invoke","params":{"n":2}}`),
	} {
		if err := bbb.WriteFrame(w.stdin, frame, bbb.MaxControlFrameBytes); err != nil {
			t.Fatalf("write frame %d: %v", i, err)
		}
		got, err := bbb.ReadFrame(w.stdout, bbb.MaxControlFrameBytes)
		if err != nil {
			t.Fatalf("read frame %d: %v", i, err)
		}
		if !bytes.Equal(got, frame) {
			t.Fatalf("frame %d: got %q want %q", i, got, frame)
		}
	}
	if err := w.stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if code := w.exitCode(t); code != 0 {
		t.Fatalf("exit code %d after clean EOF, want 0\nstderr:\n%s", code, w.stderr)
	}
	if !strings.Contains(w.stderr.String(), "event=ready") {
		t.Errorf("stderr missing readiness banner:\n%s", w.stderr)
	}
	if !strings.Contains(w.stderr.String(), "event=shutdown reason=stdin-eof") {
		t.Errorf("stderr missing clean-shutdown line:\n%s", w.stderr)
	}
}

func TestWorkerTrapExitsNonzeroWithTelemetry(t *testing.T) {
	w := startWorker(t, fixture("trap.wasm"))
	if err := bbb.WriteFrame(w.stdin, []byte(`{}`), bbb.MaxControlFrameBytes); err != nil {
		t.Fatal(err)
	}
	if code := w.exitCode(t); code != 3 {
		t.Fatalf("exit code %d after guest trap, want 3\nstderr:\n%s", code, w.stderr)
	}
	// stdout carried no frame — the failure is out-of-band only.
	if rest, _ := io.ReadAll(w.stdout); len(rest) != 0 {
		t.Errorf("stdout must stay frame-only and empty on trap, got %q", rest)
	}
	errText := w.stderr.String()
	if !strings.Contains(errText, "event=fatal stage=invoke") || !strings.Contains(errText, "trap") {
		t.Errorf("stderr missing trap telemetry line:\n%s", errText)
	}
}

func TestWorkerInvokeTimeoutExitsNonzero(t *testing.T) {
	w := startWorker(t, "-invoke-timeout=300ms", fixture("loop.wasm"))
	if err := bbb.WriteFrame(w.stdin, []byte(`{}`), bbb.MaxControlFrameBytes); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if code := w.exitCode(t); code != 3 {
		t.Fatalf("exit code %d after invoke timeout, want 3\nstderr:\n%s", code, w.stderr)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("timeout kill took %v, want prompt", elapsed)
	}
	if !strings.Contains(w.stderr.String(), "deadline") {
		t.Errorf("stderr missing deadline telemetry:\n%s", w.stderr)
	}
}

func TestWorkerRejectsOversizeInboundFrame(t *testing.T) {
	w := startWorker(t, fixture("echo.wasm"))
	// A hostile header declaring 1 MiB + 1: the worker must refuse
	// without reading the payload and die (the stream cannot resync).
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(bbb.MaxControlFrameBytes+1))
	if _, err := w.stdin.Write(header[:]); err != nil {
		t.Fatal(err)
	}
	if code := w.exitCode(t); code != 4 {
		t.Fatalf("exit code %d after oversize frame, want 4\nstderr:\n%s", code, w.stderr)
	}
	if !strings.Contains(w.stderr.String(), "event=fatal stage=stream") {
		t.Errorf("stderr missing stream telemetry:\n%s", w.stderr)
	}
}

func TestWorkerRejectsOversizeGuestResponse(t *testing.T) {
	w := startWorker(t, fixture("bloat.wasm"))
	if err := bbb.WriteFrame(w.stdin, []byte(`{}`), bbb.MaxControlFrameBytes); err != nil {
		t.Fatal(err)
	}
	if code := w.exitCode(t); code != 3 {
		t.Fatalf("exit code %d after over-budget guest response, want 3\nstderr:\n%s", code, w.stderr)
	}
	if rest, _ := io.ReadAll(w.stdout); len(rest) != 0 {
		t.Errorf("no frame may reach stdout for an over-budget response, got %q", rest)
	}
	if !strings.Contains(w.stderr.String(), "ceiling") {
		t.Errorf("stderr missing frame-budget telemetry:\n%s", w.stderr)
	}
}

func TestWorkerWrongVersionFailsLoad(t *testing.T) {
	w := startWorker(t, fixture("wrongver.wasm"))
	if code := w.exitCode(t); code != 2 {
		t.Fatalf("exit code %d for wrong protocol version, want 2\nstderr:\n%s", code, w.stderr)
	}
	errText := w.stderr.String()
	if strings.Contains(errText, "event=ready") {
		t.Errorf("worker must not report ready before admission:\n%s", errText)
	}
	if !strings.Contains(errText, "event=fatal stage=load") || !strings.Contains(errText, "bbb_protocol_version") {
		t.Errorf("stderr missing admission telemetry:\n%s", errText)
	}
}
