package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const capabilitiesRegistry = `{
  "providers": [
    {
      "name": "Anthropic",
      "url": "https://api.anthropic.com",
      "models": [
        "claude-a",
        "claude-b",
        "claude-new"
      ]
    },
    {
      "name": "Local server",
      "url": "http://127.0.0.1:8080/v1",
      "models": [
        "private-model"
      ]
    }
  ],
  "model_capabilities": {
    "claude-a": {
      "thinking": "adaptive",
      "images": false,
      "effort": []
    },
    "claude-b": {
      "input": {
        "image": true
      },
      "context_length": 100000,
      "source": "Anthropic's model list, checked by hand"
    }
  },
  "dialect_effort_floor": {
    "anthropic": [
      "low"
    ]
  }
}
`

const capabilitiesCatalogue = `{
  "anthropic": {"models": {
    "claude-a": {"tool_call": true, "modalities": {"input": ["text", "image", "pdf"], "output": ["text"]}, "limit": {"context": 200000, "output": 64000}},
    "claude-b": {"tool_call": true, "modalities": {"input": ["text", "image"], "output": ["text"]}, "limit": {"context": 200000, "output": 32000}}
  }}
}`

// THE BASELINE IS FILLED, NEVER OVERWRITTEN. What a row does not say is
// filled from the vendor's models.dev list, with source and date; what it
// says differently is kept and printed; a model models.dev does not list
// is named uncatalogued and left alone, and so is an entry no vendor
// serves; and every byte of the file outside model_capabilities is as it
// was.
func TestTheBaselineIsFilledFromModelsDevAndNeverOverwritten(t *testing.T) {
	var report bytes.Buffer
	out, err := generateCapabilities([]byte(capabilitiesRegistry), []byte(capabilitiesCatalogue), "2026-10-20", &report)
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		ModelCapabilities map[string]map[string]any `json:"model_capabilities"`
	}
	if err := json.Unmarshal(out, &reg); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	a := reg.ModelCapabilities["claude-a"]
	if _, old := a["images"]; old || a["thinking"] != "adaptive" || a["tools"] != true || a["context_length"] != 200000.0 || a["max_output_tokens"] != 64000.0 ||
		a["source"] != "models.dev (anthropic)" || a["as_of"] != "2026-10-20" {
		t.Fatalf("claude-a: %v", a)
	}
	if in := a["input"].(map[string]any); in["image"] != false || in["pdf"] != true || in["audio"] != false || in["video"] != false {
		t.Fatalf("claude-a's own images: false was overwritten, or a kind was not filled: %v", in)
	}
	if out := a["output"].(map[string]any); out["text"] != true || out["image"] != false {
		t.Fatalf("claude-a output: %v", out)
	}
	b := reg.ModelCapabilities["claude-b"]
	if b["context_length"] != 100000.0 || b["source"] != "Anthropic's model list, checked by hand" || b["max_output_tokens"] != 32000.0 {
		t.Fatalf("claude-b's hand-kept values were overwritten: %v", b)
	}
	if _, made := reg.ModelCapabilities["claude-new"]; made {
		t.Fatal("a row was made for a model models.dev does not list")
	}
	if _, made := reg.ModelCapabilities["private-model"]; made {
		t.Fatal("a local server's model was looked up")
	}
	for _, line := range []string{
		"claude-a input.image: the row says false, models.dev says true; kept",
		"claude-b context_length: the row says 100000, models.dev (anthropic) says 200000; kept",
		`claude-b source: the row says "Anthropic's model list, checked by hand"; kept`,
		"claude-new: uncatalogued: models.dev (anthropic) does not list it",
		"claude-a: filled from models.dev (anthropic)",
	} {
		if !strings.Contains(report.String(), line+"\n") {
			t.Errorf("the report does not say %q:\n%s", line, report.String())
		}
	}
	head, tail := capabilitiesRegistry[:strings.Index(capabilitiesRegistry, `"model_capabilities"`)], capabilitiesRegistry[strings.Index(capabilitiesRegistry, `,
  "dialect_effort_floor"`):]
	if !bytes.HasPrefix(out, []byte(head)) || !bytes.HasSuffix(out, []byte(tail)) {
		t.Fatalf("bytes outside model_capabilities changed:\n%s", out)
	}
	if !strings.Contains(string(out), "\n    \"claude-a\": {\n      \"thinking\": \"adaptive\",\n") {
		t.Fatalf("a row lost its order or the file's indentation:\n%s", out)
	}
	again, err := generateCapabilities(out, []byte(capabilitiesCatalogue), "2026-10-20", &bytes.Buffer{})
	if err != nil || !bytes.Equal(again, out) {
		t.Fatalf("a second run changed the file: %v\n%s", err, again)
	}
}
