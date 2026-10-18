package dashboard

import (
	"encoding/json"
	"strings"
	"testing"
)

/*
Pins the theme WIRE CONTRACT — the seam nothing covered.

The two halves were each tested and the join was not. internal/app has
TestUIThemeLoadValidateWatch (disk, validation, live watcher) and the
dashboard has TestThemeAppliesInBrowsers (theme.js applying tokens in
Blink and Gecko). Between them sits the message, and a message is
exactly where two correct halves drift apart: the frontend reads
msg.type and msg.theme as literal strings, and nothing in Go fails to
compile when a JSON tag changes.

The subtle one is the CLEAR path. ServerMessage.Theme carries
`json:"theme,omitempty"`, so a cleared theme does not send a null field
— it sends NO field at all. The frontend survives that only because
ws.js does `onTheme(msg.theme || null)`, where undefined and null are
both falsy and both mean "restore compiled defaults". That is correct,
and it is correct by an accident of two independent choices, so it is
worth a test that would notice if either one moved.
*/

func TestThemeWireContract(t *testing.T) {
	s := &Server{}

	// No source wired at all must not panic and must mean "no theme".
	if got := s.themeBytes(); got != nil {
		t.Fatalf("an unwired theme source must yield nil, got %q", got)
	}

	// The populated case: the frontend reads msg.type === 'theme'
	// and msg.theme.tokens, so both literals are load-bearing.
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

	// The CLEAR path. A deleted theme.json makes the source return nil,
	// and omitempty then drops the key entirely. The frontend's
	// `msg.theme || null` turns that absence into the clear signal, so
	// the contract is: type is still theme, and theme is ABSENT (not
	// present-and-null, and above all not a stale previous payload).
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
