package dashboard

import (
	"encoding/json"
	"strings"
	"testing"
)

// .
// .

// .
// .
// .
// .
// .
// .
// .

// .
// .
// .
// .
// .
// .
// .
// .

func TestThemeWireContract(t *testing.T) {
	s := &Server{}

	// .
	if got := s.themeBytes(); got != nil {
		t.Fatalf("an unwired theme source must yield nil, got %q", got)
	}

	// .
	// .
	s.SetThemeSource(func() []byte {
		return []byte(`{"v":1,"tokens":{"--accent":"#7cc4ff"}}`)
	})
	raw, err := json.Marshal(s.themeMessage())
	if err != nil {
		t.Fatalf("theme message must marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("theme message must decode: %v", err)
	}
	if decoded["type"] != "theme" {
		t.Fatalf(`ws.js dispatches on case 'theme'; got type %v`, decoded["type"])
	}
	payload, ok := decoded["theme"].(map[string]any)
	if !ok {
		t.Fatalf(`ws.js reads msg.theme as an object; got %T in %s`, decoded["theme"], raw)
	}
	tokens, ok := payload["tokens"].(map[string]any)
	if !ok {
		t.Fatalf(`theme.js reads payload.tokens; got %T`, payload["tokens"])
	}
	if tokens["--accent"] != "#7cc4ff" {
		t.Fatalf("token did not survive the wire: %v", tokens["--accent"])
	}

	// .
	// .
	// .
	// .
	// .
	s.SetThemeSource(func() []byte { return nil })
	raw, err = json.Marshal(s.themeMessage())
	if err != nil {
		t.Fatalf("cleared theme message must marshal: %v", err)
	}
	decoded = map[string]any{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("cleared theme message must decode: %v", err)
	}
	if decoded["type"] != "theme" {
		t.Fatalf("a cleared theme is still a theme message, got %v", decoded["type"])
	}
	if _, present := decoded["theme"]; present {
		t.Fatalf("omitempty must drop the payload so ws.js sees undefined, got %s", raw)
	}
	if strings.Contains(string(raw), "7cc4ff") {
		t.Fatalf("a cleared theme must not carry the previous tokens: %s", raw)
	}
}
