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
	defer os.RemoveAll(tmp)
	workerBin = filepath.Join(tmp, "aii-plugin-worker")
	out, err := exec.Command("go", "build", "-o", workerBin, ".").CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build worker binary: %v\n%s", err, out)
		os.Exit(1)
	}
	os.Exit(m.Run())
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
