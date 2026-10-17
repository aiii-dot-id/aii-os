package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The absent-overlay-dir silence rule (sections_serve.go, OpenRoot
// path) has two halves, and each needs its own control:
//
//   1. ABSENT dir → silent. A missing overlay dir is the default state
//      of every install. Before the fix, boot emitted one inert line
//      per frame asset — 27 on a stock system — so the readback built
//      to prevent noise was itself the noise (observed live at 10:42).
//
//   2. UNOPENABLE dir → still reported. A directory that exists but
//      cannot be opened is a real fault an operator must act on. The
//      silence rule must not swallow it. This is the negative control
//      for the fix itself: make OpenRoot fail with a non-IsNotExist
//      error and confirm the report fires.
//
// The silence rule was observed working on the running system; the
// unopenable half was the untested claim. This file pins both.

// absentDirHasNoTwin: ensure no other test-created dir interferes; the
// TempDir we point at simply never gets created.
func TestOverlayAbsentDirIsSilent(t *testing.T) {
	// A path that cannot exist: its parent does not exist either.
	missing := filepath.Join(t.TempDir(), "no", "such", "dir")
	s := newOverlayServer(t, missing)

	out := captureLog(t, func() {
		if _, ok := s.overlayAsset("/theme.css"); ok {
			t.Fatal("an overlay behind a missing dir must not serve")
		}
	})
	if strings.TrimSpace(out) != "" {
		t.Fatalf("an absent overlay dir is the default state, not a fault; it must be silent, log was %q", out)
	}
}

func TestOverlayUnopenableDirStillReports(t *testing.T) {
	// Make OpenRoot fail WITHOUT IsNotExist, uid-independently: point
	// overlayDir at a REGULAR FILE. OpenRoot then fails ENOTDIR — a
	// real fault, not an absence. (A chmod-0000 dir was tried first;
	// it cannot deny root, so the test skipped under root.)
	dir := t.TempDir()
	notADir := filepath.Join(dir, "overlay-is-a-file")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	s := newOverlayServer(t, notADir)

	out := captureLog(t, func() {
		if _, ok := s.overlayAsset("/theme.css"); ok {
			t.Fatal("an overlay behind an unopenable dir must not serve")
		}
	})
	if !strings.Contains(out, "inert") || !strings.Contains(out, "unopenable") {
		t.Fatalf("a dir that exists but will not open is a real fault; it must be reported, log was %q", out)
	}
}
