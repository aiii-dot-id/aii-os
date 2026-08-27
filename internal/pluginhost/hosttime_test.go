package pluginhost

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// frameCapture is a fake invoker capturing the exact frame Execute
// builds — the seam the in-process wall and the supervisor both sit
// behind.
type frameCapture struct{ frame []byte }

func (f *frameCapture) Invoke(_ context.Context, frame []byte) ([]byte, error) {
	f.frame = append([]byte(nil), frame...)
	return []byte(`{"jsonrpc":"2.0","id":"h1","result":{"status":"succeeded","operation_result":{}}}`), nil
}

// TestHostTimeInjectedAndReserved pins the operator-approved host-time
// rule: plugins have no ambient clock by design, so the host injects
// _host_now_ms into every invoke's arguments; the _host* namespace is
// host-reserved — caller-supplied values are dropped, never trusted.
func TestHostTimeInjectedAndReserved(t *testing.T) {
	cap := &frameCapture{}
	tool := &operationTool{operation: "focus.set", inv: cap}

	before := time.Now().UnixMilli()
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"note":         "hello",
		"_host_now_ms": float64(12345), // forged: must be dropped and rewritten
		"_host_evil":   "x",            // reserved namespace: dropped entirely
	})
	after := time.Now().UnixMilli()
	if err != nil {
		t.Fatal(err)
	}

	var req struct {
		Params struct {
			Arguments map[string]interface{} `json:"arguments"`
		} `json:"params"`
	}
	if err := json.Unmarshal(cap.frame, &req); err != nil {
		t.Fatal(err)
	}
	args := req.Params.Arguments
	now, ok := args["_host_now_ms"].(float64)
	if !ok {
		t.Fatalf("_host_now_ms missing or non-numeric: %v", args["_host_now_ms"])
	}
	if int64(now) < before || int64(now) > after {
		t.Fatalf("_host_now_ms %d outside [%d,%d] — the forged value survived?", int64(now), before, after)
	}
	if _, present := args["_host_evil"]; present {
		t.Fatal("caller-supplied _host* key survived — the reserved namespace leaked")
	}
	if args["note"] != "hello" {
		t.Fatal("caller argument lost in the injection copy")
	}
	var _ tools.Result // keep the import honest if assertions change
}
