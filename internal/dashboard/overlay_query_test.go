package dashboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// W2: the readback owes the human, not only the log. These tests pin the
// round trip through the real report path: outcomes recorded once must
// arrive in the ui.overlay answer, ordered and bounded, and the log line
// and the screen event must come from one decision (one writer, not two).
//
// The silence rules are part of the contract under test: absent files
// and an absent overlay dir record NOTHING (a stock install must not
// flood), while accepted, rejected, and real faults record exactly once.

func TestOverlayQueryRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "theme.css"), []byte("body{}"), 0o644); err != nil {
		t.Fatalf("write overlay: %v", err)
	}
	s := newOverlayServer(t, dir)

	// Drive the real report path: accepted, absent (silent by design),
	// rejected (not a frame extension). The log capture ties the screen
	// event to the same decision the log line came from.
	out := captureLog(t, func() {
		s.overlayAsset("/theme.css")
		s.overlayAsset("/missing.js")
		s.overlayAsset("/secret.png")
	})

	msg := s.overlayMessage()
	if msg.Type != "overlays" {
		t.Fatalf("query answer type = %q, want overlays", msg.Type)
	}
	if len(msg.Overlays) != 2 {
		t.Fatalf("overlays = %d events, want 2 (accepted + rejected; absent is silent by design)", len(msg.Overlays))
	}
	first, second := msg.Overlays[0], msg.Overlays[1]
	if first.Path != "/theme.css" || !strings.Contains(first.Outcome, "accepted") {
		t.Fatalf("first event = %+v, want accepted /theme.css", first)
	}
	if second.Path != "/secret.png" || !strings.Contains(second.Outcome, "rejected") {
		t.Fatalf("second event = %+v, want rejected /secret.png", second)
	}
	for _, e := range msg.Overlays {
		if _, err := time.Parse(time.RFC3339, e.DecidedAt); err != nil {
			t.Fatalf("decided_at %q is not RFC3339: %v", e.DecidedAt, err)
		}
	}
	// One decision, two surfaces: every screened event is in the log,
	// and the silent path is in neither.
	if !strings.Contains(out, "/theme.css") || !strings.Contains(out, "/secret.png") {
		t.Fatalf("screened events missing from log; log was %q", out)
	}
	if strings.Contains(out, "/missing.js") {
		t.Fatalf("absent file must be silent in both surfaces; log was %q", out)
	}
}

func TestOverlayQueryBounded(t *testing.T) {
	dir := t.TempDir()
	s := newOverlayServer(t, dir)

	// 40 real, distinct overlay files — more than the bound, each one a
	// genuine accepted outcome (absent files would be silent, and the
	// bound is only reachable through recorded events).
	for i := 0; i < 40; i++ {
		name := strings.Repeat("a", i+1) + ".js"
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write overlay %s: %v", name, err)
		}
	}
	captureLog(t, func() {
		for i := 0; i < 40; i++ {
			s.overlayAsset("/" + strings.Repeat("a", i+1) + ".js")
		}
	})

	msg := s.overlayMessage()
	if len(msg.Overlays) != 32 {
		t.Fatalf("overlays = %d events, want the 32 bound", len(msg.Overlays))
	}
	// Tail-keep: the newest survives. If the bound dropped newest
	// instead, the last event would be the 32nd path, not the 40th.
	last := msg.Overlays[len(msg.Overlays)-1]
	if !strings.Contains(last.Path, strings.Repeat("a", 40)+".js") {
		t.Fatalf("tail-keep failed: last = %+v, want the 40th path", last)
	}
}

// JSON wire shape: the client renders msg.overlays[].path/.outcome — the
// field tags must survive the encoder exactly as panel.js reads them, and
// an empty readback must omit the field entirely (a stock install renders
// no FRAME card, not an empty one).
func TestOverlayQueryWireShape(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "custom.css"), []byte("b{}"), 0o644); err != nil {
		t.Fatalf("write overlay: %v", err)
	}
	s := newOverlayServer(t, dir)
	captureLog(t, func() { s.overlayAsset("/custom.css") })

	raw, err := json.Marshal(s.overlayMessage())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	ov, ok := m["overlays"].([]any)
	if !ok || len(ov) != 1 {
		t.Fatalf("wire overlays = %v", m["overlays"])
	}
	e := ov[0].(map[string]any)
	for _, k := range []string{"path", "outcome", "decided_at"} {
		if _, ok := e[k]; !ok {
			t.Fatalf("wire event missing %q: %v", k, e)
		}
	}

	// Empty readback: no recorded outcomes means no overlays key on the
	// wire — omitempty is the render's silence, pinned here.
	quiet := newOverlayServer(t, t.TempDir())
	captureLog(t, func() { quiet.overlayAsset("/missing.js") })
	rawQ, err := json.Marshal(quiet.overlayMessage())
	if err != nil {
		t.Fatalf("marshal quiet: %v", err)
	}
	var mq map[string]any
	if err := json.Unmarshal(rawQ, &mq); err != nil {
		t.Fatalf("unmarshal quiet: %v", err)
	}
	if _, exists := mq["overlays"]; exists {
		t.Fatalf("empty readback must omit overlays on the wire, got %v", mq)
	}
}
