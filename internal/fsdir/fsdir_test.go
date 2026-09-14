package fsdir

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/quiesce"
)

// .
// .
// .

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
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := New(ctx, nil, dir, Options{Debounce: 120 * time.Millisecond, Heartbeat: time.Hour})

	for i := 0; i < 10; i++ {
		if err := os.WriteFile(filepath.Join(dir, "a.css"), []byte{byte(i)}, 0o644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !waitPoke(t, w, slack) {
		t.Fatal("the storm never poked")
	}
	// .
	// .
	// .
	// .
	pokes := 1
	deadline := time.After(600 * time.Millisecond)
	for {
		select {
		case <-w.C:
			pokes++
		case <-deadline:
			if pokes > 2 {
				t.Fatalf("ten writes delivered %d pokes — debounce is not coalescing", pokes)
			}
			return
		}
	}
}

func TestAbsentDirPromotesWhenCreated(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "ui")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := New(ctx, nil, dir, Options{Debounce: 30 * time.Millisecond, Heartbeat: 150 * time.Millisecond})

	// .
	if !waitPoke(t, w, slack) {
		t.Fatal("heartbeat did not poke while the directory was absent")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// .
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

// .
// .
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

// .
// .
// .
// .
// .
// .
func TestDepthOneSeesTheWriteInsideANewChildDirectory(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := New(ctx, nil, dir, Options{Debounce: 30 * time.Millisecond, Heartbeat: time.Hour, Depth: 1})

	child := filepath.Join(dir, "org.example.memory")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	// .
	// .
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

// .
// .
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
	// .
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

// .
// .
func TestDepthZeroIgnoresChildDirectories(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "sub")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := New(ctx, nil, dir, Options{Debounce: 30 * time.Millisecond, Heartbeat: time.Hour})

	if err := os.WriteFile(filepath.Join(child, "deep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if waitPoke(t, w, 1200*time.Millisecond) {
		t.Fatal("a depth-0 watch poked for a write one level down — the default watch grew a reach it never declared")
	}
}
