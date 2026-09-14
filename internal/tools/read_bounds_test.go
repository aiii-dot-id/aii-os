package tools

// .
// .
// .

import (
	"context"
	"errors"
	"fmt"
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
	client := GuardedClient(time.Second, func(context.Context, string) error {
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
	if errors.Is(err, ErrEgressBlocked) {
		t.Fatalf("redirect guard relabeled a timeout as policy denial: %v", err)
	}
}

// .
// .
// .
func TestAModelAuthoredLimitCannotPanicTheReadTool(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte("a\nb\nc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := &ReadTool{maxBytes: 51200}
	// .
	// .
	// .
	for _, limit := range []interface{}{float64(1 << 62), int64(math.MaxInt64), "9223372036854775807", float64(math.MaxInt32)} {
		res, err := tool.Execute(context.Background(), map[string]interface{}{"file_path": p, "offset": 2, "limit": limit})
		if err != nil {
			t.Fatal(err)
		}
		if res.Error == "" || !strings.Contains(res.Error, "ceiling") {
			t.Fatalf("limit %v was not refused at the ceiling: %+v", limit, res)
		}
	}
	res, _ := tool.Execute(context.Background(), map[string]interface{}{"file_path": p, "offset": 2, "limit": maxReadLimit})
	if res.Error != "" || !strings.HasPrefix(res.Output, "b\nc") {
		t.Fatalf("a limit at the ceiling must work: %+v", res)
	}
}

type panickingTool struct{}

func (panickingTool) Name() string        { return "boom" }
func (panickingTool) Description() string { return "panics" }
func (panickingTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}
func (panickingTool) Execute(context.Context, map[string]interface{}) (Result, error) {
	var s []string
	return Result{Output: s[5]}, nil
}

// .
// .
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
		res, err = r.Execute(context.Background(), "boom", map[string]interface{}{})
	}()
	if err != nil || !strings.Contains(res.Error, "failed internally") {
		t.Fatalf("want a tool error naming the failure, got %+v / %v", res, err)
	}
}

// .
// .
// .
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
	res, err := tool.Execute(context.Background(), map[string]interface{}{"file_path": p, "offset": 1000, "limit": 5})
	runtime.ReadMemStats(&after)
	if err != nil || res.Error != "" {
		t.Fatalf("page failed: %+v %v", res, err)
	}
	// .
	// .
	// .
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
	res, err := tool.Execute(context.Background(), map[string]interface{}{"file_path": p})
	runtime.ReadMemStats(&after)
	if err != nil || res.Error != "" || !res.Truncated {
		t.Fatalf("long-line page failed: %+v / %v", res, err)
	}
	if delta := after.TotalAlloc - before.TotalAlloc; delta > 8<<20 {
		t.Fatalf("a 1 KiB page of one long line allocated %d bytes", delta)
	}
}

// .
// .
// .
// .
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
	cancel()
	tool := &ReadTool{maxBytes: 51200}
	res, err := tool.Execute(ctx, map[string]interface{}{"file_path": p, "offset": 80000, "limit": 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Error, "cancelled") {
		t.Fatalf("a cancelled read scanned on and returned: %+v", res)
	}
}
