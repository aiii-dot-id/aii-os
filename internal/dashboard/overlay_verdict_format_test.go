package dashboard

import (
	"os"
	"strings"
	"testing"
)

// ASPIRE #2: the verdict format is a WIRE CONTRACT, not prose. panel.js
// splits every outcome on the FIRST ": " — verb before it, sentence
// after — and renders the verb as a row label. This test drives the
// real decision paths (additive, fork, new file, hazard-suffixed,
// rejected) and pins the shape the consumer actually performs, against
// the exact bytes the wire carries (overlayMessage, not a copy).
//
// The drift this pins shut was real: the FORK sentence existed in five
// places in three wordings, and the browser fixture had diverged
// mid-sentence from the source while still passing — every copy was
// self-consistent. A hand-copied sentence has two owners; a derived
// one has one.
func TestOverlayVerdictWireFormat(t *testing.T) {
	dir := t.TempDir()
	// One overlay of every accepted class, plus the hazard shape
	// (a CSS that sets a form control under 16px), plus a rejected
	// extension.
	writeOverlay(t, dir, "custom.css", "body{color:red}")
	writeOverlay(t, dir, "theme.css", "/* replaces shipped frame */\n:root{}\n")
	writeOverlay(t, dir, "work.js", "console.log('no shipped counterpart')\n")
	writeOverlay(t, dir, "tiny.css", "textarea{font-size:5px}")
	writeOverlay(t, dir, "secret.png", "x")

	s := newOverlayServer(t, dir)
	// The wired shape: production always has a stamp (app.go), and a
	// fork verdict without its stamp answers the wrong question —
	// "diverged from WHICH build?". Pin that it reaches the wire.
	s.SetBuildStamp("deadbeef")

	for _, p := range []string{"/custom.css", "/theme.css", "/work.js", "/tiny.css", "/secret.png"} {
		s.overlayAsset(p) // return value irrelevant here; the report is the product
	}

	evs := s.overlayMessage().Overlays
	if len(evs) != 5 {
		t.Fatalf("wire carries %d events, want 5 (one per class)", len(evs))
	}

	verbs := map[string]bool{}
	for _, ev := range evs {
		outcome := ev.Outcome
		// Exactly the split panel.js performs: first ": " only.
		// A sentence may contain further colons ("HAZARD: ...",
		// "inert: dir unopenable: <err>") — first-split must survive
		// them.
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

		// The FORK verdict must name its build — a frozen copy that
		// cannot say which build it froze at answers nothing.
		if strings.Contains(outcome, "FORK") && !strings.Contains(outcome, "at build deadbeef") {
			t.Errorf("%s: fork verdict carries no build stamp: %q", ev.Path, outcome)
		}
		// The hazard suffix must not break the verb seam: "accepted: ...
		// HAZARD: ..." still splits to verb "accepted".
		if ev.Path == "/tiny.css" && verb != "accepted" {
			t.Errorf("hazard-suffixed verdict split to verb %q, want accepted: %q", verb, outcome)
		}
	}
	if !verbs["accepted"] || !verbs["rejected"] {
		t.Errorf("expected accepted and rejected verbs among outcomes, got %v", verbs)
	}
}

// The fork sentence's tail — "will NOT receive upgrades to <path>" —
// names the exact file that stops receiving fixes. Pin that the path
// in the tail is the overlaid path itself, because that is what makes
// the warning actionable ("which file do I stop owning?").
func TestOverlayForkVerdictNamesItsPath(t *testing.T) {
	out := acceptedOutcome("/layout.css", []byte("body{color:red}"), "stamp1")
	if !strings.Contains(out, "will NOT receive upgrades to /layout.css") {
		t.Fatalf("fork verdict must name the frozen path in its tail: %q", out)
	}
	if !strings.Contains(out, "at build stamp1") {
		t.Fatalf("fork verdict must carry the evaluating build: %q", out)
	}
}

// The docs quote the verdict as a blockquote — presenting the exact
// wire sentence. A quotation is a hand-copy with a reader, and hand-
// copies diverge (the drift this file pins had three wordings live at
// once). So the doc's quote must match the code's own sentence: this
// test reads the doc and pins it to acceptedOutcome's output. It does
// not pin the whole sentence (the byte counts are example values); it
// pins the part that IS the contract: the fork warning tail.
func TestDocsQuoteTheRealForkVerdict(t *testing.T) {
	raw, err := os.ReadFile("../../docs/UI_REFORM.md")
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
