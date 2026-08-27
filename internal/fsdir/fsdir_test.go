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
	// The window after the first poke stays quiet: ten writes, one look
	// (a second poke may arrive if a write landed after delivery — the
	// contract is coalescing, not exactly-once; zero MORE within the
	// debounce quiet is the assertion).
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
