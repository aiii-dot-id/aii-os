package broker

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// .
// .
func TestSettingsGetNeedsNoGrantAndSurvivesSAFE(t *testing.T) {
	safe := false
	h, err := New(Config{Store: newStore(t), InSAFE: func() bool { return safe }})
	if err != nil {
		t.Fatal(err)
	}
	b := h.Bind("org.example.cfg", packagefmt.TierT0, nil)
	call := func() string {
		reply, err := b.Dispatch(context.Background(), methodInvokeCall, []byte(`{"operation":"settings.get"}`))
		if err != nil {
			t.Fatal(err)
		}
		return string(reply)
	}
	if out := call(); !strings.Contains(out, `"values":{}`) {
		t.Fatalf("no source reads as an empty object: %s", out)
	}
	b.SetSettings(func() map[string]interface{} { return map[string]interface{}{"recall_limit": 8.0, "api_key": "acme"} })
	out := call()
	if !strings.Contains(out, `"values":{"api_key":"acme","recall_limit":8}`) || !strings.Contains(out, `"status":"succeeded"`) {
		t.Fatalf("the host's values, whole: %s", out)
	}
	var reply struct {
		Receipt map[string]interface{} `json:"external_receipt"`
	}
	if json.Unmarshal([]byte(out), &reply) != nil || reply.Receipt["host_authored"] != true {
		t.Fatalf("a settings read is receipted: %s", out)
	}
	safe = true
	if out := call(); !strings.Contains(out, `"recall_limit":8`) {
		t.Fatalf("SAFE does not take a plugin's own configuration away: %s", out)
	}
	b.BeginOperation(OperationScope{Operation: "read.op", Effects: EffectsReadInternal, Declared: true})
	defer b.EndOperation()
	if out := call(); !strings.Contains(out, `"recall_limit":8`) {
		t.Fatalf("a read-only operation with no capabilities still reads its settings: %s", out)
	}
	if out, _ := b.Dispatch(context.Background(), methodInvokeCall, []byte(`{"operation":"kv.put","target":{"key":"k"},"arguments":{"value":"v"}}`)); !strings.Contains(string(out), "POLICY_DENY") {
		t.Fatalf("the same scope still refuses a write: %s", out)
	}
}
