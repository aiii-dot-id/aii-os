package dashboard

import (
	"os"
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
func TestOverlayVerdictWireFormat(t *testing.T) {
	dir := t.TempDir()
	// .
	// .
	// .
	writeOverlay(t, dir, "custom.css", "body{color:red}")
	writeOverlay(t, dir, "theme.css", "/* replaces shipped frame */\n:root{}\n")
	writeOverlay(t, dir, "work.js", "console.log('no shipped counterpart')\n")
	writeOverlay(t, dir, "tiny.css", "textarea{font-size:5px}")
	writeOverlay(t, dir, "secret.png", "x")

	s := newOverlayServer(t, dir)
	// .
	// .
	// .
	s.SetBuildStamp("deadbeef")

	for _, p := range []string{"/custom.css", "/theme.css", "/work.js", "/tiny.css", "/secret.png"} {
		s.overlayAsset(p)
	}

	evs := s.overlayMessage().Overlays
	if len(evs) != 5 {
		t.Fatalf("wire carries %d events, want 5 (one per class)", len(evs))
	}

	verbs := map[string]bool{}
	for _, ev := range evs {
		outcome := ev.Outcome
		// .
		// .
		// .
		// .
		i := strings.Index(outcome, ": ")
		if i <= 0 {
			t.Errorf("%s: outcome %q carries no verb boundary", ev.Path, outcome)
			continue
		}
		verb := outcome[:i]
		sentence := outcome[i+2:]
		switch verb {
		case "accepted", "rejected", "inert":
		default:
			t.Errorf("%s: verb %q is not in the panel's label set", ev.Path, verb)
		}
		if sentence == "" {
			t.Errorf("%s: empty sentence after the verb boundary", ev.Path)
		}
		verbs[verb] = true

		// .
		// .
		if strings.Contains(outcome, "FORK") && !strings.Contains(outcome, "at build deadbeef") {
			t.Errorf("%s: fork verdict carries no build stamp: %q", ev.Path, outcome)
		}
		// .
		// .
		if ev.Path == "/tiny.css" && verb != "accepted" {
			t.Errorf("hazard-suffixed verdict split to verb %q, want accepted: %q", verb, outcome)
		}
	}
	if !verbs["accepted"] || !verbs["rejected"] {
		t.Errorf("expected accepted and rejected verbs among outcomes, got %v", verbs)
	}
}

// .
// .
// .
// .
func TestOverlayForkVerdictNamesItsPath(t *testing.T) {
	out := acceptedOutcome("/layout.css", []byte("body{color:red}"), "stamp1")
	if !strings.Contains(out, "will NOT receive upgrades to /layout.css") {
		t.Fatalf("fork verdict must name the frozen path in its tail: %q", out)
	}
	if !strings.Contains(out, "at build stamp1") {
		t.Fatalf("fork verdict must carry the evaluating build: %q", out)
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestDocsQuoteTheRealForkVerdict(t *testing.T) {
	raw, err := os.ReadFile("../../docs/UI_REFORM.md")
	if os.IsNotExist(err) {
		// .
		// .
		// .
		// .
		t.Skip("docs/UI_REFORM.md not present in this tree (docs-free export)")
	}
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	want := "frozen and will NOT receive upgrades to"
	if !strings.Contains(doc, want) {
		t.Fatalf("docs/UI_REFORM.md must quote the code's own fork wording (%q) — a quotation that paraphrases is a hand-copy that already drifted", want)
	}
	if strings.Contains(doc, "frozen at the build it was taken from") {
		t.Fatal("docs/UI_REFORM.md still carries the drifted pre-landing wording")
	}
	if strings.Contains(doc, "frozen at the build it was copied from") {
		t.Fatal("docs/UI_REFORM.md still carries the third variant wording")
	}
}
