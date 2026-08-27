package dashboard

import (
	"strings"
	"testing"
)

// R71 lets both hands re-form the frame, and custom.css loads LAST — so
// an equal-specificity rule there beats the shipped stylesheet at every
// width. Most of that is exactly the point. One case is not: a form
// control under 16px makes iOS zoom on focus and never zoom back, and
// nothing connects "I made my chat text smaller" to "the presence strip
// is gone and the nav is clipped".
//
// The readback names it at acceptance. It does not refuse it.

func TestOverlayNamesTheZoomHazard(t *testing.T) {
	css := []byte(".composer textarea { font-size: 14px; color: red; }")
	out := acceptedOutcome("/custom.css", css, "")

	if !strings.Contains(out, "HAZARD") {
		t.Fatalf("an overlay that reinstates the iOS zoom trap was accepted in silence: %q", out)
	}
	if !strings.Contains(out, "14px") {
		t.Fatalf("the readback did not say what the value actually is: %q", out)
	}
	if !strings.Contains(out, "16px or larger") {
		t.Fatalf("the readback named a problem with no way out of it: %q", out)
	}
	// Naming a hazard must not cost the outcome it rides on.
	if !strings.Contains(out, "additive layer") {
		t.Fatalf("the accepted-outcome verdict was lost behind the hazard: %q", out)
	}
}

// The negative control that keeps the warning worth reading. A hint that
// fires on healthy CSS is noise, and noise is not read.
func TestSixteenPxIsNotAHazard(t *testing.T) {
	for _, css := range []string{
		".composer textarea { font-size: 16px }",
		"input[type=text] { font-size: 18px }",
		"textarea { font-size:16.5px }",
	} {
		if out := acceptedOutcome("/custom.css", []byte(css), ""); strings.Contains(out, "HAZARD") {
			t.Fatalf("healthy CSS was warned about (%q): %q", css, out)
		}
	}
}

// A small font-size that is NOT on a form control zooms nothing.
func TestSmallTextElsewhereIsNotAHazard(t *testing.T) {
	css := []byte(".pill { font-size: 10.5px } .crumb { font-size:13px }")
	if out := acceptedOutcome("/custom.css", css, ""); strings.Contains(out, "HAZARD") {
		t.Fatalf("ordinary small frame text was flagged: %q", out)
	}
}

// rem/em depend on a root this cannot resolve. Guessing produces the
// noisy warnings that stop being read, so it stays quiet.
func TestRelativeUnitsAreNotGuessedAt(t *testing.T) {
	css := []byte("textarea { font-size: 0.9rem }")
	if out := acceptedOutcome("/custom.css", css, ""); strings.Contains(out, "HAZARD") {
		t.Fatalf("a unit this cannot resolve was warned about anyway: %q", out)
	}
}

// A JS overlay has no font sizes to read.
func TestNonCSSOverlayIsNotScanned(t *testing.T) {
	js := []byte(`const s = "font-size: 12px on a textarea";`)
	if out := acceptedOutcome("/custom.js", js, ""); strings.Contains(out, "HAZARD") {
		t.Fatalf("a string inside JavaScript was read as a CSS declaration: %q", out)
	}
}

// The hazard rides every accepted kind, not only the additive layer — a
// FORK of layout.css is the likeliest place to hit this, since it means
// owning the 16px rule outright.
func TestHazardRidesAForkedStylesheetToo(t *testing.T) {
	out := acceptedOutcome("/layout.css", []byte(".composer textarea{font-size:14px}"), "")
	if !strings.Contains(out, "FORK") {
		t.Fatalf("expected the fork verdict: %q", out)
	}
	if !strings.Contains(out, "HAZARD") {
		t.Fatalf("a forked stylesheet carrying the zoom trap said nothing: %q", out)
	}
}

// The byte count now derives from the content it describes rather than
// being passed beside it.
func TestAcceptedOutcomeCountsTheBytesItWasGiven(t *testing.T) {
	body := []byte("body{}")
	out := acceptedOutcome("/custom.css", body, "")
	if !strings.Contains(out, "6 bytes") {
		t.Fatalf("the size reported does not describe the content: %q", out)
	}
}

// A byte-identical fork carries the shipped baseline, and the shipped
// baseline legitimately contains sub-16px rules (layout.css pins 16px
// only inside the max-width:767px block; desktop blocks set smaller
// values for other controls). Warning on a copy nobody edited would
// describe the shipped frame — noise that stops being read. The FORK
// verdict itself still lands: ownership, not today's bytes, is the
// divergence. Found by the Method review, 2026-08-24.
func TestIdenticalForkCarriesNoHazardRider(t *testing.T) {
	shipped, err := staticFS.ReadFile("static/layout.css")
	if err != nil {
		t.Fatalf("shipped layout.css unreadable: %v", err)
	}
	out := acceptedOutcome("/layout.css", shipped, "")
	if !strings.Contains(out, "FORK") {
		t.Fatalf("expected the fork verdict: %q", out)
	}
	if !strings.Contains(out, "byte-identical") {
		t.Fatalf("the identical fork should say so: %q", out)
	}
	if strings.Contains(out, "HAZARD") {
		t.Fatalf("a copy nobody edited was warned about: %q", out)
	}
}

// The edited fork keeps its rider: owning the file AND changing the
// zoom rule is the real trap, and the readback must still name it.
func TestEditedForkKeepsItsHazard(t *testing.T) {
	shipped, err := staticFS.ReadFile("static/layout.css")
	if err != nil {
		t.Fatalf("shipped layout.css unreadable: %v", err)
	}
	edited := append([]byte(nil), shipped...)
	edited = append(edited, []byte("\n.composer textarea{font-size:14px}\n")...)
	out := acceptedOutcome("/layout.css", edited, "")
	if !strings.Contains(out, "FORK") {
		t.Fatalf("expected the fork verdict: %q", out)
	}
	if !strings.Contains(out, "HAZARD") {
		t.Fatalf("an edited fork owning the zoom trap said nothing: %q", out)
	}
}
