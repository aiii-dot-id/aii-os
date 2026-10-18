package tools

import (
	"context"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestReviewRedirectGuardFailureKeepsItsRealCause(t *testing.T) {
	client := firewall.GuardedClient(time.Second, func(context.Context, string) error {
		return context.DeadlineExceeded
	}, http.DefaultTransport)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.invalid/next", nil)
	if err != nil {
		t.Fatal(err)
	}
	err = client.CheckRedirect(req, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("redirect guard lost its real cause: %v", err)
	}
	if errors.Is(err, firewall.ErrEgressBlocked) {
		t.Fatalf("redirect guard relabeled a timeout as policy denial: %v", err)
	}
}

// A limit near MaxInt used to wrap offset-1+limit negative and panic the
// process. It is refused at the ceiling, and the arithmetic no longer
// adds. The registry boundary is proved separately below.
func TestAModelAuthoredLimitCannotPanicTheReadTool(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte("a\nb\nc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := &ReadTool{maxBytes: 51200}
	// Each of these PASSES intArg (integral, in range) and used to reach
	// offset-1+limit; float64(MaxInt64) does not, since it is no longer
	// integral after conversion, and is refused earlier for that reason.
	for _, limit := range []any{float64(1 << 62), int64(math.MaxInt64), "9223372036854775807", float64(math.MaxInt32)} {
		res, err := tool.Execute(context.Background(), map[string]any{"file_path": p, "offset": 2, "limit": limit})
		if err != nil {
			t.Fatal(err)
		}
		if res.Error == "" || !strings.Contains(res.Error, "ceiling") {
			t.Fatalf("limit %v was not refused at the ceiling: %+v", limit, res)
		}
	}
	res, _ := tool.Execute(context.Background(), map[string]any{"file_path": p, "offset": 2, "limit": maxReadLimit})
	if res.Error != "" || !strings.HasPrefix(res.Output, "     2\tb\n     3\tc") {
		t.Fatalf("a limit at the ceiling must work: %+v", res)
	}
}

type panickingTool struct{}

func (panickingTool) Name() string        { return "boom" }
func (panickingTool) Description() string { return "panics" }
func (panickingTool) Parameters() map[string]any {
	return map[string]any{"type": "object"}
}
func (panickingTool) Execute(context.Context, map[string]any) (Result, error) {
	var s []string
	return Result{Output: s[5]}, nil
}

// The registry turns a tool panic into a tool error. Without this a
// model-authored argument could take the resident down.
func TestARegistryToolPanicBecomesAToolError(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	r.Register(panickingTool{})
	var res Result
	var err error
	func() {
		defer func() {
			if p := recover(); p != nil {
				t.Fatalf("the panic escaped Registry.Execute: %v", p)
			}
		}()
		res, err = r.Execute(context.Background(), "boom", map[string]any{})
	}()
	if err != nil || !strings.Contains(res.Error, "failed internally") {
		t.Fatalf("want a tool error naming the failure, got %+v / %v", res, err)
	}
}

// A page of a large file must not load the file. 16 MiB of short lines,
// one 5-line page: the old code allocated the whole file plus a slice of
// every line (33 MB measured); streaming allocates a buffer and a page.
func TestReadPagesWithoutLoadingTheWholeFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "big")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 99) + "\n"
	for i := 0; i < (16<<20)/100; i++ {
		if _, err := f.WriteString(line); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()
	tool := &ReadTool{maxBytes: 51200}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	res, err := tool.Execute(context.Background(), map[string]any{"file_path": p, "offset": 1000, "limit": 5})
	runtime.ReadMemStats(&after)
	if err != nil || res.Error != "" {
		t.Fatalf("page failed: %+v %v", res, err)
	}

	if !strings.Contains(res.Output, "more lines — continue at offset 1005") {
		t.Fatalf("page contract broken: %q", res.Output[len(res.Output)-80:])
	}
	if delta := after.TotalAlloc - before.TotalAlloc; delta > 4<<20 {
		t.Fatalf("a 5-line page allocated %d bytes — the file was read whole", delta)
	}
	_ = fmt.Sprintf
}

func TestReviewOneLongLineKeepsMemoryBoundedByThePage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "one-line")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(64 << 20); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	tool := &ReadTool{maxBytes: 1024}
	res, err := tool.Execute(context.Background(), map[string]any{"file_path": p})
	runtime.ReadMemStats(&after)
	if err != nil || res.Error != "" || !res.Truncated {
		t.Fatalf("long-line page failed: %+v / %v", res, err)
	}
	if delta := after.TotalAlloc - before.TotalAlloc; delta > 8<<20 {
		t.Fatalf("a 1 KiB page of one long line allocated %d bytes", delta)
	}
}

// A cancelled context releases a long skip. The FIFO test above proves a
// non-regular file is refused before any read; this proves the loops
// themselves observe ctx — the mutant that drops the checks scans the
// whole file and returns a page as if nothing happened.
func TestReviewReadObservesCancellationWhileSkipping(t *testing.T) {
	p := filepath.Join(t.TempDir(), "big")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("y", 99) + "\n"
	for i := 0; i < (8<<20)/100; i++ {
		if _, err := f.WriteString(line); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: the very first loop check must fire
	tool := &ReadTool{maxBytes: 51200}
	res, err := tool.Execute(ctx, map[string]any{"file_path": p, "offset": 80000, "limit": 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Error, "cancelled") {
		t.Fatalf("a cancelled read scanned on and returned: %+v", res)
	}
}
