// Package fsdir delivers coalesced "look now" signals for one
// directory — the event-driven half of the house watching law
// (operator ruling 2026-08-26):
//
//	EVENTS ARE TRIGGERS TO LOOK; THE CALLER'S SNAPSHOT IS THE TRUTH.
//
// fsnotify supplies the syscall plane: inotify on linux and android
// (GOOS=android implies the linux build tag), kqueue on macOS and iOS
// (GOOS=ios implies darwin), ReadDirectoryChangesW on windows — all
// five verified by cross-compilation, cgo-free. Everything above the
// syscalls is owned here, because the field's watchers (chokidar,
// Watchman, Air) all converged on the same lessons: event NAMES are
// never trusted — atomic-rename saves arrive as create/rename storms,
// kqueue and inotify disagree on semantics, inotify drops events on
// queue overflow — so this package never interprets an event. Every
// event is the same one bit: "look".
//
// The contract:
//
//   - C fires once after a quiet DEBOUNCE window following any event
//     under the directory: one editor save is one poke, not five.
//   - C fires on every HEARTBEAT tick — drift insurance for what
//     events cannot carry (overflow, network/FUSE mounts where events
//     never arrive, the absent-directory case). The heartbeat is a
//     quiesce.Ticker, so PARKED MEANS SILENT holds end to end: no
//     delivery while the gate is paused, and the resume catch-up tick
//     delivers anything held. (The kernel may still queue raw events
//     while parked; the process stays silent.)
//   - An ABSENT directory is the normal case: the watch runs
//     heartbeat-only and promotes itself to events when the directory
//     appears, re-trying on each heartbeat. A deleted directory
//     demotes the same way, silently.
//   - C has capacity one and drops when the receiver is mid-look —
//     coalescing is the point; the caller's snapshot absorbs any
//     number of pokes into one truth.
//
// A watch whose event plane cannot start at all degrades to
// heartbeat-only and says so once — the five-platform law's honest
// no-op rule, applied to a capability instead of a platform.
package fsdir

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/aiii-dot-id/aii-os/internal/quiesce"
)

// Defaults. Debounce covers an editor's save storm; the heartbeat is
// pure drift insurance now that events carry the signal (the old 2s
// mtime polls were ~43k wakeups/day each while foregrounded).
const (
	DefaultDebounce  = 150 * time.Millisecond
	DefaultHeartbeat = 45 * time.Second
)

// Options tune one watch. Zero values take the defaults above.
type Options struct {
	Debounce  time.Duration
	Heartbeat time.Duration
	// File, when set, narrows event pokes to this basename — the
	// single-file-in-a-directory watch (config.json). Heartbeats are
	// not narrowed; insurance insures everything.
	File string
}

// Watch is one directory's coalesced look-signal.
type Watch struct {
	// C delivers "look now". Capacity one; never closed — receivers
	// select against their own context.
	C <-chan struct{}

	c    chan struct{}
	dir  string
	file string
	gate *quiesce.Gate
	fw   *fsnotify.Watcher

	watching bool // dir currently added to fw (pump goroutine only)
	dirty    bool // an event arrived and was not yet delivered (pump goroutine only)
}

// New starts a watch on dir and returns immediately. It never fails:
// a missing directory or a dead event plane both degrade to
// heartbeat-only, which is exactly the pre-fsdir behaviour. The watch
// dies with ctx.
func New(ctx context.Context, gate *quiesce.Gate, dir string, opts Options) *Watch {
	if opts.Debounce <= 0 {
		opts.Debounce = DefaultDebounce
	}
	if opts.Heartbeat <= 0 {
		opts.Heartbeat = DefaultHeartbeat
	}
	w := &Watch{
		c:    make(chan struct{}, 1),
		dir:  dir,
		file: opts.File,
		gate: gate,
	}
	w.C = w.c
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("fsdir: %s: no event plane (%v) — heartbeat-only at %s", dir, err, opts.Heartbeat)
	} else {
		w.fw = fw
	}
	// Arm the event plane BEFORE returning: the caller snapshots right
	// after New, and an event landing between Add and that snapshot
	// just queues a poke the first look absorbs. The reverse order was
	// a hole — an edit between New returning and the pump goroutine
	// arming was invisible until the heartbeat (caught by the two
	// immediate-write tests on day one).
	w.tryAdd()
	go w.run(ctx, opts)
	return w
}

func (w *Watch) run(ctx context.Context, opts Options) {
	hb := quiesce.NewTicker(w.gate, opts.Heartbeat)
	defer hb.Stop()
	if w.fw != nil {
		defer w.fw.Close()
	}

	var events chan fsnotify.Event
	var errs chan error
	if w.fw != nil {
		events = w.fw.Events
		errs = w.fw.Errors
	}

	// One debounce timer, drain-safe. debC is nil while no window is
	// open, which parks that select arm.
	debounce := time.NewTimer(opts.Debounce)
	if !debounce.Stop() {
		<-debounce.C
	}
	var debC <-chan time.Time

	arm := func() {
		if debC == nil {
			debounce.Reset(opts.Debounce)
			debC = debounce.C
		}
		// A window already open keeps its deadline: a steady event
		// stream still delivers at the FIRST quiet debounce, not never.
	}

	for {
		select {
		case <-ctx.Done():
			return

		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if w.file != "" && filepath.Base(ev.Name) != w.file {
				continue
			}
			w.dirty = true
			arm()

		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			// Overflow or backend trouble: events were LOST, the
			// snapshot was not. The response to unknown loss is an
			// immediate look, and the heartbeat keeps insuring after.
			log.Printf("fsdir: %s: event plane error (%v) — forcing a look", w.dir, err)
			w.dirty = true
			arm()

		case <-debC:
			debC = nil
			if w.gate.Paused() {
				continue // hold; dirty stays set, resume catch-up delivers
			}
			w.dirty = false
			w.poke()

		case <-hb.C:
			// Silent while parked; carries the one resume catch-up.
			w.tryAdd()
			w.dirty = false
			w.poke()
		}
	}
}

// poke delivers one coalesced signal.
func (w *Watch) poke() {
	select {
	case w.c <- struct{}{}:
	default:
	}
}

// tryAdd (re)arms the event plane for the directory. Absent dir:
// stays/becomes heartbeat-only. Present dir: added if not already.
// Cheap when settled; called from the pump goroutine only.
func (w *Watch) tryAdd() {
	if w.fw == nil {
		return
	}
	if _, err := os.Stat(w.dir); err != nil {
		if w.watching {
			// The directory left; kernel watches die with it. Demote
			// silently — absence is the normal case, and the caller's
			// snapshot already said what absence means.
			_ = w.fw.Remove(w.dir)
			w.watching = false
		}
		return
	}
	if w.watching {
		return
	}
	if err := w.fw.Add(w.dir); err != nil {
		// Present but unwatchable (FUSE, descriptor exhaustion): the
		// heartbeat carries it; retried next beat.
		return
	}
	w.watching = true
}
