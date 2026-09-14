package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// .
// .
// .
func TestUIOverlayWatchIsSilentWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	a := New(&Config{Identity: IdentityConfig{LedgerPath: filepath.Join(dir, "ledger.jsonl")}})
	if got := a.overlaySnapshot(); got != "" {
		t.Fatalf("absent dir must snapshot empty, got %q", got)
	}
}

// .
// .
// .
// .
// .
// .
func TestUIOverlaySnapshotChanges(t *testing.T) {
	dir := t.TempDir()
	a := New(&Config{Identity: IdentityConfig{LedgerPath: filepath.Join(dir, "ledger.jsonl")}})

	base := a.overlaySnapshot()
	if base != "" {
		t.Fatalf("absent dir must snapshot empty, got %q", base)
	}

	// .
	// .
	// .
	// .
	if err := os.MkdirAll(filepath.Join(dir, "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	if a.overlaySnapshot() != base {
		t.Fatal("empty dir appearing must NOT change the digest")
	}

	// .
	css := filepath.Join(dir, "ui", "custom.css")
	if err := os.WriteFile(css, []byte("body{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	withFile := a.overlaySnapshot()
	if withFile == base || !strings.Contains(withFile, "custom.css") {
		t.Fatalf("added file must appear in digest, got %q", withFile)
	}

	// .
	if err := os.WriteFile(filepath.Join(dir, "ui", "logo.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if a.overlaySnapshot() != withFile {
		t.Fatal("non-servable drop must not change the digest")
	}

	// .
	if err := os.MkdirAll(filepath.Join(dir, "ui", "views"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ui", "views", "op.js"), []byte("// x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.overlaySnapshot(), "/views/op.js") {
		t.Fatal("nested servable must appear in digest")
	}

	// .
	if err := os.Remove(css); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(a.overlaySnapshot(), "custom.css") {
		t.Fatal("deleted file must leave the digest")
	}
}

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
func TestUIOverlayWatcherLoopUpdatesScreenState(t *testing.T) {
	dir := t.TempDir()
	a := New(&Config{Identity: IdentityConfig{LedgerPath: filepath.Join(dir, "id-ledger.jsonl")}})
	a.watchEvery = 30 * time.Millisecond
	if err := os.MkdirAll(filepath.Join(dir, "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	go a.watchUIOverlay()
	defer a.bgCancel()

	// .
	deadline := time.Now().Add(5 * time.Second)
	for a.overlayLast.Load() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if a.overlayLast.Load() == nil {
		t.Fatal("watcher never stored an initial snapshot")
	}

	// .
	css := filepath.Join(dir, "ui", "custom.css")
	if err := os.WriteFile(css, []byte("body{background:#101820}"), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if s := a.overlayLast.Load(); s != nil && strings.Contains(*s, "custom.css") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("watcher loop never recorded the edit — the screen would never know")
}

// .
// .
// .
// .
// .
func TestOverlayDiffDetectsDeletion(t *testing.T) {
	one := "/custom.css 100 10"
	two := "/views/panel.js 200 20"
	// .
	got := overlayDiff(one, two)
	if len(got) != 2 {
		t.Fatalf("deletion must surface: want 2 paths (deleted + added), got %v", got)
	}
	if got[0] != "/custom.css" || got[1] != "/views/panel.js" {
		t.Fatalf("want [/custom.css /views/panel.js], got %v", got)
	}
	// .
	got = overlayDiff(one, "")
	if len(got) != 1 || got[0] != "/custom.css" {
		t.Fatalf("pure deletion must surface the path, got %v", got)
	}
	// .
	got = overlayDiff(one, "/custom.css 999 10")
	if len(got) != 1 || got[0] != "/custom.css" {
		t.Fatalf("edit must surface the path, got %v", got)
	}
	// .
	if got := overlayDiff(one, one); got != nil {
		t.Fatalf("identical snapshots must produce no paths, got %v", got)
	}
}
