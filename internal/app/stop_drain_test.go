package app

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// neverEnds is a database export that has no end, for a peer to stop
// reading in the middle of.
type neverEnds struct{}

func (neverEnds) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// holdExport asks the identity's dashboard for its database on a
// connection of its own, reads the status line and nothing after it.
func holdExport(t *testing.T, addr string) {
	t.Helper()
	c, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if _, err := fmt.Fprintf(c, "POST /database/export HTTP/1.1\r\nHost: %s\r\nOrigin: https://%s\r\nSec-Fetch-Site: same-origin\r\nContent-Length: 0\r\n\r\n", addr, addr); err != nil {
		t.Fatal(err)
	}
	if status, err := bufio.NewReader(c).ReadString('\n'); err != nil || !strings.Contains(status, " 200 ") {
		t.Fatalf("the download did not begin, so nothing is held: %q %v", status, err)
	}
	// The handler blocks once the connection's buffers are full.
	time.Sleep(200 * time.Millisecond)
}

// stoppable is an identity with a ledger open and a dashboard serving,
// whose stop gives the dashboard one second.
func stoppable(t *testing.T, export func(context.Context) (io.ReadCloser, int64, error)) (*App, string) {
	t.Helper()
	cfg := defaultConfig()
	cfg.Dashboard.StopDrainSeconds = 1
	a := New(cfg)
	var err error
	if a.ledger, err = ledger.New(filepath.Join(t.TempDir(), "ledger.jsonl")); err != nil {
		t.Fatal(err)
	}
	a.dashboard = dashboard.New("127.0.0.1", 0, &dashboard.WSHandler{DatabaseExport: export})
	addr, err := a.dashboard.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return a, addr
}

func TestAStopOfTheIdentityIsNotHeldByADownloadNobodyReads(t *testing.T) {
	a, addr := stoppable(t, func(context.Context) (io.ReadCloser, int64, error) {
		return io.NopCloser(neverEnds{}), 1 << 40, nil
	})
	holdExport(t, addr)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	began := time.Now()
	if err := a.Shutdown(ctx); err != nil {
		t.Fatalf("the stop of an identity whose download nobody reads: %v", err)
	}
	if took := time.Since(began); took < 900*time.Millisecond || took > 6*time.Second {
		t.Fatalf("the stop took %s; the dashboard's bound is one second, and the stop goes on at it", took)
	}
	if a.ledger != nil {
		t.Fatal("the stop ended and the ledger is still open")
	}
}

// A caller deadline does not release resources still owned by a callback.
// The pending stop completes after the callback returns.

func TestAStopRemainsPendingUntilTheDashboardsRequestReturns(t *testing.T) {
	entered, mayReturn := make(chan struct{}), make(chan struct{})
	a, addr := stoppable(t, func(context.Context) (io.ReadCloser, int64, error) {
		close(entered)
		<-mayReturn
		return nil, 0, errors.New("the export was never made")
	})
	var released bool
	defer func() {
		if !released {
			close(mayReturn)
		}
		stopApp(t, a)
	}()
	c, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := fmt.Fprintf(c, "POST /database/export HTTP/1.1\r\nHost: %s\r\nOrigin: https://%s\r\nSec-Fetch-Site: same-origin\r\nContent-Length: 0\r\n\r\n", addr, addr); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the request did not reach its handler")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	err = a.Shutdown(ctx)
	var pending *ShutdownPending
	if !errors.As(err, &pending) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the stop must remain pending while its request runs: %v", err)
	}
	if a.ledger == nil {
		t.Fatal("pending stop closed the ledger beneath its request")
	}
	close(mayReturn)
	released = true
	settled, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	if err := a.Shutdown(settled); err != nil {
		t.Fatal(err)
	}
	if a.ledger != nil {
		t.Fatal("completed stop did not close the ledger")
	}
}

// dashboard.stop_drain_seconds is the bound's number: zero or absent for
// the default of five seconds, refused negative at load.
func TestTheStopsBoundIsTheOperatorsNumber(t *testing.T) {
	if got := New(defaultConfig()).stopDrain(); got != 5*time.Second {
		t.Fatalf("with nothing in the file the bound is %s, want 5s", got)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"dashboard":{"stop_drain_seconds":12}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := New(cfg).stopDrain(); got != 12*time.Second {
		t.Fatalf("the file says twelve seconds and the stop takes %s", got)
	}
	if err := os.WriteFile(path, []byte(`{"dashboard":{"stop_drain_seconds":-1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "dashboard.stop_drain_seconds") {
		t.Fatalf("a negative bound must be refused by its key: %v", err)
	}
}

// plugins.runtime.slow_storage_call_ms is refused negative, like the
// other numbers a file may name.
func TestANegativeSlowStorageThresholdIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"plugins":{"runtime":{"slow_storage_call_ms":-1}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "plugins.runtime.slow_storage_call_ms") {
		t.Fatalf("a negative threshold must be refused by its key: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"plugins":{"runtime":{"slow_storage_call_ms":250}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err := LoadConfig(path); err != nil || cfg.Plugins.Runtime.SlowStorageCallMs != 250 {
		t.Fatalf("a threshold of 250 ms must load as written: %v", err)
	}
}
