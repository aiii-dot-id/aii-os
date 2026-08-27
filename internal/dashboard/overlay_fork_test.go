package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// "accepted" is not one outcome. An overlay that ADDS composes with the
// shipped frame forever; an overlay that REPLACES a shipped file is
// frozen at the build it was copied from and will silently miss every
// later fix to that file. Both are permitted — R71 grants both hands —
// but they are not the same event, and the readback is the only moment
// anyone is looking. These tests pin the difference.

func writeOverlay(t *testing.T, dir, name, body string) {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", name, err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// Overlaying layout.css replaces 32 KiB of shipped frame. The operator
// now owns it. If the readback does not say so here, nothing ever will.
func TestOverlayReportsForkOfShippedFrame(t *testing.T) {
	dir := t.TempDir()
	writeOverlay(t, dir, "layout.css", "body{color:red}")
	s := newOverlayServer(t, dir)

	out := captureLog(t, func() {
		if _, ok := s.overlayAsset("/layout.css"); !ok {
			t.Fatal("a valid overlay must still be served — R71 grants the fork")
		}
	})
	if !strings.Contains(out, "FORK") {
		t.Errorf("replacing shipped frame must be reported as a fork; log was %q", out)
	}
	if !strings.Contains(out, "custom.css") {
		t.Errorf("the fork report must name the additive alternative; log was %q", out)
	}
}

// custom.css ships as an empty stub. Overlaying it is the supported path
// and must NOT be reported as a fork, or the warning becomes noise and
// stops being read — which is how true warnings die.
func TestOverlayReportsAdditiveLayerNotFork(t *testing.T) {
	dir := t.TempDir()
	writeOverlay(t, dir, "custom.css", "body{color:red}")
	s := newOverlayServer(t, dir)

	out := captureLog(t, func() {
		if _, ok := s.overlayAsset("/custom.css"); !ok {
			t.Fatal("the additive layer must be served")
		}
	})
	if strings.Contains(out, "FORK") {
		t.Errorf("the additive layer is not a fork; log was %q", out)
	}
	if !strings.Contains(out, "additive") {
		t.Errorf("the additive layer must be reported as such; log was %q", out)
	}
}

// A file with no shipped counterpart cannot diverge from anything.
func TestOverlayReportsNewFileNotFork(t *testing.T) {
	dir := t.TempDir()
	writeOverlay(t, dir, "views/operator-panel.js", "export const x=1")
	s := newOverlayServer(t, dir)

	out := captureLog(t, func() {
		if _, ok := s.overlayAsset("/views/operator-panel.js"); !ok {
			t.Fatal("a new overlay file must be served")
		}
	})
	if strings.Contains(out, "FORK") {
		t.Errorf("a file with no shipped counterpart is not a fork; log was %q", out)
	}
	if !strings.Contains(out, "new file") {
		t.Errorf("a new file must be reported as such; log was %q", out)
	}
}

// The shipped stubs must actually BE stubs. If a future edit puts real
// rules in custom.css, overlaying it starts replacing behaviour and the
// "composes forever" promise above quietly becomes false.
func TestCustomStubsAreEmptyOfRules(t *testing.T) {
	for _, name := range []string{"static/custom.css", "static/custom.js"} {
		b, err := staticFS.ReadFile(name)
		if err != nil {
			t.Fatalf("%s must ship: %v", name, err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			s := strings.TrimSpace(line)
			if s == "" || strings.HasPrefix(s, "/*") || strings.HasPrefix(s, "*") ||
				strings.HasPrefix(s, "*/") || strings.HasPrefix(s, "//") {
				continue
			}
			t.Errorf("%s must ship as a comment-only stub; found executable line %q", name, s)
		}
	}
}

// A fork verdict without a build referent cannot answer the question it
// raises: "frozen — diverged from WHICH build?" The stamp closes that,
// and because dedup keys on path+outcome, a build change re-keys the
// verdict: the next request under a new build is a NEW decision, not a
// suppressed duplicate. This is re-detection: the fork cannot silently
// shadow shipped bytes it never saw.
func TestForkVerdictCarriesTheEvaluatingBuild(t *testing.T) {
	dir := t.TempDir()
	writeOverlay(t, dir, "layout.css", "body{color:red}")
	s := newOverlayServer(t, dir)
	s.SetBuildStamp("aaabbb11")

	out := captureLog(t, func() {
		s.overlayAsset("/layout.css")
		s.overlayAsset("/layout.css") // dedup: same path, same verdict
	})
	if !strings.Contains(out, "at build aaabbb11") {
		t.Fatalf("the fork verdict must name the evaluating build: %q", out)
	}
	if strings.Count(out, "FORK") != 1 {
		t.Fatalf("same build, same verdict — dedup must hold to one line: %q", out)
	}

	out2 := captureLog(t, func() {
		s.SetBuildStamp("aaabbb11b") // a different build, same everything else
		s.overlayAsset("/layout.css")
	})
	if !strings.Contains(out2, "at build aaabbb11b") {
		t.Fatalf("a new build under a frozen fork must re-decide with its own stamp: %q", out2)
	}
}

// The byte-identical fork names the build too: "byte-identical" is a
// claim about one build's bytes, and the operator must know which.
func TestIdenticalForkVerdictNamesTheBuild(t *testing.T) {
	dir := t.TempDir()
	shipped, err := staticFS.ReadFile("static/layout.css")
	if err != nil {
		t.Fatalf("shipped layout.css: %v", err)
	}
	writeOverlay(t, dir, "layout.css", string(shipped))
	s := newOverlayServer(t, dir)
	s.SetBuildStamp("aaabbb11")

	out := captureLog(t, func() {
		s.overlayAsset("/layout.css")
	})
	if !strings.Contains(out, "byte-identical to /layout.css at build aaabbb11") {
		t.Fatalf("byte-identical is a per-build claim; verdict: %q", out)
	}
}
