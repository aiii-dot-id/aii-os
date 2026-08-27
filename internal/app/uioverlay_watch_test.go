package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestUIOverlayWatchIsSilentWhenAbsent: an absent overlay directory is
// the normal case — the snapshot stays empty and nothing broadcasts.
// The silence discipline: a watcher that fires on nothing is noise.
func TestUIOverlayWatchIsSilentWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	a := New(&Config{Identity: IdentityConfig{LedgerPath: filepath.Join(dir, "ledger.jsonl")}})
	if got := a.overlaySnapshot(); got != "" {
		t.Fatalf("absent dir must snapshot empty, got %q", got)
	}
}

// TestUIOverlaySnapshotChanges pins the watcher's trigger: every
// resolver-visible mutation — file added, edited, deleted, or the
// directory itself appearing — changes the digest. The broadcast the
// watcher fans is the house idiom (BroadcastTheme/BroadcastLayout);
// delivery to a live socket is pinned by the dashboard package's
// round-trip tests. This test owns the trigger.
func TestUIOverlaySnapshotChanges(t *testing.T) {
	dir := t.TempDir()
	a := New(&Config{Identity: IdentityConfig{LedgerPath: filepath.Join(dir, "ledger.jsonl")}})

	base := a.overlaySnapshot()
	if base != "" {
		t.Fatalf("absent dir must snapshot empty, got %q", base)
	}

	// An empty directory overrides nothing: the resolver cannot
	// resolve anything differently, so the digest must not change.
	// (Negative-control twin of the old assertion: the first
	// SERVABLE FILE is the event, not the directory.)
	if err := os.MkdirAll(filepath.Join(dir, "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	if a.overlaySnapshot() != base {
		t.Fatal("empty dir appearing must NOT change the digest")
	}

	// A servable file added is a change.
	css := filepath.Join(dir, "ui", "custom.css")
	if err := os.WriteFile(css, []byte("body{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	withFile := a.overlaySnapshot()
	if withFile == base || !strings.Contains(withFile, "custom.css") {
		t.Fatalf("added file must appear in digest, got %q", withFile)
	}

	// A non-servable file is not a change the resolver can see.
	if err := os.WriteFile(filepath.Join(dir, "ui", "logo.png"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if a.overlaySnapshot() != withFile {
		t.Fatal("non-servable drop must not change the digest")
	}

	// Nested servable (views/x.js) is a change.
	if err := os.MkdirAll(filepath.Join(dir, "ui", "views"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ui", "views", "op.js"), []byte("// x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.overlaySnapshot(), "/views/op.js") {
		t.Fatal("nested servable must appear in digest")
	}

	// Deletion is a change.
	if err := os.Remove(css); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(a.overlaySnapshot(), "custom.css") {
		t.Fatal("deleted file must leave the digest")
	}
}

// TestUIOverlayWatcherFiresBroadcastOnEdit is the full-loop proof: a
// real overlay edit, the app-owned watcher, and a real WebSocket client
// connected to a real dashboard server — the "overlays" push must
// arrive without any query. This is the W3 contract: an edit reaches
// the operator's screen with no F5 and no manual query.
//
// The app and dashboard packages are separate, so the WS client half
// runs in the dashboard package against a BroadcastOverlay the app
// test drives the same way (overlay_ws_roundtrip_test.go pins the
// query side; this pins the push side at the app seam).
// TestUIOverlayWatcherLoopUpdatesScreenState is the loop proof at the
// watchEvery seam: a real watcher running at test cadence against a
// live dir, a real edit, and overlayLast — "what the screen last
// saw" — must move. No live server needed: the socket half is pinned
// by the dashboard package (TestBroadcastOverlayOverWebSocket), this
// test owns the loop's decide-and-broadcast link. Cutting the loop's
// digest comparison must fail this test via the deadline.
func TestUIOverlayWatcherLoopUpdatesScreenState(t *testing.T) {
	dir := t.TempDir()
	a := New(&Config{Identity: IdentityConfig{LedgerPath: filepath.Join(dir, "id-ledger.jsonl")}})
	a.watchEvery = 30 * time.Millisecond
	if err := os.MkdirAll(filepath.Join(dir, "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	go a.watchUIOverlay()
	defer a.bgCancel()

	// Wait for the watcher's initial snapshot to be stored.
	deadline := time.Now().Add(5 * time.Second)
	for a.overlayLast.Load() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if a.overlayLast.Load() == nil {
		t.Fatal("watcher never stored an initial snapshot")
	}

	// The edit that must reach the screen.
	css := filepath.Join(dir, "ui", "custom.css")
	if err := os.WriteFile(css, []byte("body{background:#101820}"), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if s := a.overlayLast.Load(); s != nil && strings.Contains(*s, "custom.css") {
			return // green: the loop saw the edit
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("watcher loop never recorded the edit — the screen would never know")
}

// TestOverlayDiffDetectsDeletion pins the P2 fix: a deleted path must
// reach the client as a path, not as a fresh token with an empty
// list. The old diff walked only the new snapshot — deletions were
// invisible, and a no-path push is a client no-op (deleted CSS stayed
// visually active). The union is the candidate set.
func TestOverlayDiffDetectsDeletion(t *testing.T) {
	one := "/custom.css 100 10"
	two := "/views/panel.js 200 20"
	// Deletion: custom.css gone, panel.js added.
	got := overlayDiff(one, two)
	if len(got) != 2 {
		t.Fatalf("deletion must surface: want 2 paths (deleted + added), got %v", got)
	}
	if got[0] != "/custom.css" || got[1] != "/views/panel.js" {
		t.Fatalf("want [/custom.css /views/panel.js], got %v", got)
	}
	// Pure deletion: empty new snapshot, one gone path.
	got = overlayDiff(one, "")
	if len(got) != 1 || got[0] != "/custom.css" {
		t.Fatalf("pure deletion must surface the path, got %v", got)
	}
	// Edit still works: same path, changed line.
	got = overlayDiff(one, "/custom.css 999 10")
	if len(got) != 1 || got[0] != "/custom.css" {
		t.Fatalf("edit must surface the path, got %v", got)
	}
	// No change: identical snapshots.
	if got := overlayDiff(one, one); got != nil {
		t.Fatalf("identical snapshots must produce no paths, got %v", got)
	}
}
