package fsdir

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/quiesce"
)

// The tests hold the contract, not the backend: a poke is a poke,
// whichever syscall carried it. Heartbeats are pushed far out (1h)
// wherever the assertion is "the EVENT plane did this".

const slack = 3 * time.Second

func waitPoke(t *testing.T, w *Watch, d time.Duration) bool {
	t.Helper()
	select {
	case <-w.C:
		return true
	case <-time.After(d):
		return false
	}
}

func TestEventPokesWithoutHeartbeat(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := New(ctx, nil, dir, Options{Debounce: 30 * time.Millisecond, Heartbeat: time.Hour})

	if err := os.WriteFile(filepath.Join(dir, "a.css"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !waitPoke(t, w, slack) {
		t.Fatal("an event under the directory did not poke — the event plane is dead and only the heartbeat would have saved it")
	}
}

func TestSaveStormCoalesces(t *testing.T) {
	const window = time.Second
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := New(ctx, nil, dir, Options{Debounce: window, Heartbeat: time.Hour})

	for i := 0; i < 10; i++ {
		if err := os.WriteFile(filepath.Join(dir, "a.css"), []byte{byte(i)}, 0o644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	// A poke that must come: a long wait costs time only when it never does.
	if !waitPoke(t, w, 30*time.Second) {
		t.Fatal("the storm never poked")
	}
	// Every poke after it, until a whole window passes without one; a third
	// fails at once, so a watcher that never stops poking fails here rather
	// than at the package's timeout.
	pokes := 1
	for quiet := false; !quiet; {
		select {
		case <-w.C:
			if pokes++; pokes > 2 {
				t.Fatalf("ten writes delivered %d pokes — debounce is not coalescing", pokes)
			}
		case <-time.After(window):
			quiet = true
		}
	}
}

func TestAbsentDirPromotesWhenCreated(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "ui")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := New(ctx, nil, dir, Options{Debounce: 30 * time.Millisecond, Heartbeat: 150 * time.Millisecond})

	// Absent: heartbeats poke (insurance), nothing crashes.
	if !waitPoke(t, w, slack) {
		t.Fatal("heartbeat did not poke while the directory was absent")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Promotion happens on a heartbeat; then the EVENT plane must carry.
	time.Sleep(400 * time.Millisecond)
	drain(w)
	if err := os.WriteFile(filepath.Join(dir, "born.css"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !waitPoke(t, w, slack) {
		t.Fatal("no poke after the directory appeared and a file landed")
	}
}

func TestFileFilterNarrowsEvents(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := New(ctx, nil, dir, Options{Debounce: 30 * time.Millisecond, Heartbeat: time.Hour, File: "config.json"})

	if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if waitPoke(t, w, 700*time.Millisecond) {
		t.Fatal("an unrelated file poked a File-narrowed watch")
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !waitPoke(t, w, slack) {
		t.Fatal("the named file did not poke")
	}
}

// The rename-safe save: write a temp name, rename into place — every
// editor's atomic save, and the exact shape mtime polling races.
func TestAtomicRenameSavePokes(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := New(ctx, nil, dir, Options{Debounce: 30 * time.Millisecond, Heartbeat: time.Hour, File: "config.json"})

	tmp := filepath.Join(dir, ".config.json.tmp")
	if err := os.WriteFile(tmp, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, "config.json")); err != nil {
		t.Fatal(err)
	}
	if !waitPoke(t, w, slack) {
		t.Fatal("an atomic-rename save did not poke the named file")
	}
}

func TestParkedMeansSilentThenCatchUp(t *testing.T) {
	dir := t.TempDir()
	gate := quiesce.NewGate()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := New(ctx, gate, dir, Options{Debounce: 30 * time.Millisecond, Heartbeat: time.Hour})

	gate.Pause()
	if err := os.WriteFile(filepath.Join(dir, "a.css"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if waitPoke(t, w, 700*time.Millisecond) {
		t.Fatal("a parked watch delivered — parked means silent")
	}
	gate.Resume()
	if !waitPoke(t, w, slack) {
		t.Fatal("resume did not deliver the held look (catch-up)")
	}
}

func drain(w *Watch) {
	for {
		select {
		case <-w.C:
		default:
			return
		}
	}
}

// THE INSTALL SEQUENCE, WHICH IS THE WHOLE REASON DEPTH EXISTS:
// mkdir plugins/<id>/ then write the package inside it. The parent
// watch sees the mkdir. Nothing watches the child, so the write that
// follows a millisecond later is invisible, and the caller waits a
// full heartbeat for a file that landed instantly. Heartbeat is an
// hour here so only the EVENT plane can pass this.
func TestDepthOneSeesTheWriteInsideANewChildDirectory(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := New(ctx, nil, dir, Options{Debounce: 30 * time.Millisecond, Heartbeat: time.Hour, Depth: 1})

	child := filepath.Join(dir, "org.example.memory")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	// The mkdir itself pokes (it is an event on the parent). Drain it:
	// the assertion is about what comes AFTER.
	if !waitPoke(t, w, slack) {
		t.Fatal("the child directory appearing did not poke the parent watch")
	}

	if err := os.WriteFile(filepath.Join(child, "pkg.aiiospkg"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !waitPoke(t, w, slack) {
		t.Fatal("a write INSIDE the new child directory never poked — depth 1 did not arm the child, so an install waits for the heartbeat")
	}
}

// A child that goes away takes its watch with it, and the watch set
// must not leak: uninstall is as ordinary as install.
func TestDepthOneDropsAChildThatLeaves(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "gone-soon")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := New(ctx, nil, dir, Options{Debounce: 30 * time.Millisecond, Heartbeat: time.Hour, Depth: 1})

	if err := os.RemoveAll(child); err != nil {
		t.Fatal(err)
	}
	if !waitPoke(t, w, slack) {
		t.Fatal("removing a watched child did not poke")
	}
	// Re-creating the same path must arm again, not stay stale.
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if !waitPoke(t, w, slack) {
		t.Fatal("the child returning did not poke")
	}
	if err := os.WriteFile(filepath.Join(child, "again.aiiospkg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !waitPoke(t, w, slack) {
		t.Fatal("a write in the RE-created child never poked — the watch did not re-arm after the path returned")
	}
}

// Depth 0 registers only the parent, including after a new child appears.
// A child's metadata can still produce a parent-directory notification on
// Windows; events are look-triggers, not proof of a recursive watch.
func TestDepthZeroIgnoresChildDirectories(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "sub")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := New(ctx, nil, dir, Options{Debounce: 30 * time.Millisecond, Heartbeat: time.Hour})
	parentOnly := func() {
		t.Helper()
		if w.fw == nil {
			t.Fatal("the depth check requires an active event plane")
		}
		watched := w.fw.WatchList()
		if len(watched) != 1 || filepath.Clean(watched[0]) != filepath.Clean(dir) {
			t.Fatalf("depth 0 must watch only the parent %q, got %q", dir, watched)
		}
	}
	parentOnly()
	if err := os.Mkdir(filepath.Join(dir, "new-child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !waitPoke(t, w, slack) {
		t.Fatal("creating a direct child did not poke the parent watch")
	}
	parentOnly() // the event must not register existing or new children
}
