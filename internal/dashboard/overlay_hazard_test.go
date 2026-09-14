package dashboard

import (
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
	// .
	if !strings.Contains(out, "additive layer") {
		t.Fatalf("the accepted-outcome verdict was lost behind the hazard: %q", out)
	}
}

// .
// .
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

// .
func TestSmallTextElsewhereIsNotAHazard(t *testing.T) {
	css := []byte(".pill { font-size: 10.5px } .crumb { font-size:13px }")
	if out := acceptedOutcome("/custom.css", css, ""); strings.Contains(out, "HAZARD") {
		t.Fatalf("ordinary small frame text was flagged: %q", out)
	}
}

// .
// .
func TestRelativeUnitsAreNotGuessedAt(t *testing.T) {
	css := []byte("textarea { font-size: 0.9rem }")
	if out := acceptedOutcome("/custom.css", css, ""); strings.Contains(out, "HAZARD") {
		t.Fatalf("a unit this cannot resolve was warned about anyway: %q", out)
	}
}

// .
func TestNonCSSOverlayIsNotScanned(t *testing.T) {
	js := []byte(`const s = "font-size: 12px on a textarea";`)
	if out := acceptedOutcome("/custom.js", js, ""); strings.Contains(out, "HAZARD") {
		t.Fatalf("a string inside JavaScript was read as a CSS declaration: %q", out)
	}
}

// .
// .
// .
func TestHazardRidesAForkedStylesheetToo(t *testing.T) {
	out := acceptedOutcome("/layout.css", []byte(".composer textarea{font-size:14px}"), "")
	if !strings.Contains(out, "FORK") {
		t.Fatalf("expected the fork verdict: %q", out)
	}
	if !strings.Contains(out, "HAZARD") {
		t.Fatalf("a forked stylesheet carrying the zoom trap said nothing: %q", out)
	}
}

// .
// .
func TestAcceptedOutcomeCountsTheBytesItWasGiven(t *testing.T) {
	body := []byte("body{}")
	out := acceptedOutcome("/custom.css", body, "")
	if !strings.Contains(out, "6 bytes") {
		t.Fatalf("the size reported does not describe the content: %q", out)
	}
}

// .
// .
// .
// .
// .
// .
// .
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

// .
// .
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
