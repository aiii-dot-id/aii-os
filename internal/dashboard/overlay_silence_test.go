package dashboard

import (
	"os"
	"path/filepath"
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
// .
func TestOverlayAbsentDirIsSilent(t *testing.T) {
	// .
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
	// .
	// .
	// .
	// .
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
