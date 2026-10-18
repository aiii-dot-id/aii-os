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
	_, err := tool.Execute(context.Background(), map[string]any{
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
			Arguments map[string]any `json:"arguments"`
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

// A plugin that declares webhooks is told where they are mounted — its own,
// under the host's reserved namespace — in every call: a caller cannot forge it,
// a plugin that declares none is never given it, and an identity with no public
// origin gives none.
func TestAPluginIsToldWhereItsWebhooksAre(t *testing.T) {
	capture := func(ap *ActivePlugin, forged bool) map[string]any {
		t.Helper()
		cap := &frameCapture{}
		tool := &operationTool{operation: "mail.setup", inv: cap, owner: ap}
		args := map[string]any{"note": "hi"}
		if forged {
			args["_host_webhooks"] = map[string]any{"notify": "https://evil.example/hooks/x/y"}
		}
		if _, err := tool.Execute(context.Background(), args); err != nil {
			t.Fatal(err)
		}
		var req struct {
			Params struct {
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if err := json.Unmarshal(cap.frame, &req); err != nil {
			t.Fatal(err)
		}
		return req.Params.Arguments
	}
	url := func(id, path string) string { return "https://identity.example.test/hooks/" + id + "/" + path }
	mine := &ActivePlugin{ID: "org.example.mail", Webhooks: []WebhookDecl{{Path: "notify"}, {Path: "other/deep"}}, webhookURL: url}

	got, _ := capture(mine, true)["_host_webhooks"].(map[string]any)
	if got["notify"] != url(mine.ID, "notify") || got["other/deep"] != url(mine.ID, "other/deep") || len(got) != 2 {
		t.Fatalf("the plugin's own webhooks, and the forged value dropped: %v", got)
	}
	if _, present := capture(&ActivePlugin{ID: "org.example.plain", webhookURL: url}, true)["_host_webhooks"]; present {
		t.Fatal("a plugin that declares no webhook was given the key — or the forged one survived")
	}
	if _, present := capture(&ActivePlugin{ID: mine.ID, Webhooks: mine.Webhooks}, false)["_host_webhooks"]; present {
		t.Fatal("no origin, no URLs")
	}
	if _, present := capture(&ActivePlugin{ID: mine.ID, Webhooks: mine.Webhooks, webhookURL: func(string, string) string { return "" }}, false)["_host_webhooks"]; present {
		t.Fatal("an origin that is empty gives no URLs")
	}
}
