package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The NEGATIVE half of the effort/display contract. The positive half
// (TestThinkingDisplayAndEffortReachTheWire) proves the values arrive;
// this proves their absence arrives as ABSENCE. Both regressions are
// 400-shaped: an empty output_config is a parameter the API may reject,
// and a display riding without its thinking block is exactly the
// dangling-field family the vendor already 400s on.
func TestUnsetEffortAndDanglingDisplaySendNothing(t *testing.T) {
	capture := func(mode, display, effort string) map[string]any {
		t.Helper()
		var captured map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewDecoder(r.Body).Decode(&captured)
			io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
		}))
		defer srv.Close()
		c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic",
			ThinkingMode: mode, ThinkingDisplay: display, ReasoningEffort: effort})
		if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
			t.Fatal(err)
		}
		return captured
	}

	// Unset effort: no output_config key at all — not an empty object.
	body := capture("adaptive", "", "")
	if _, present := body["output_config"]; present {
		t.Fatalf("unset effort still sent output_config: %v", body["output_config"])
	}

	// A display configured while thinking is OFF must not dangle: no
	// thinking key, and no output_config invented to carry it.
	for _, mode := range []string{"", "off"} {
		body = capture(mode, "summarized", "")
		if _, present := body["thinking"]; present {
			t.Fatalf("mode %q sent a thinking parameter: %v", mode, body["thinking"])
		}
		if _, present := body["output_config"]; present {
			t.Fatalf("mode %q with display invented output_config: %v", mode, body["output_config"])
		}
	}

	// And effort still flows when thinking is off — they are independent
	// levers on the wire (output_config is not a property of thinking).
	body = capture("off", "", "max")
	oc, _ := body["output_config"].(map[string]any)
	if oc == nil || oc["effort"] != "max" {
		t.Fatalf("effort must not require a thinking block, got %v", body["output_config"])
	}
	if _, present := body["thinking"]; present {
		t.Fatalf("effort-without-thinking sent a thinking parameter: %v", body["thinking"])
	}
}
