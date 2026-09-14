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

// .
// .
// .
const (
	DefaultDebounce  = 150 * time.Millisecond
	DefaultHeartbeat = 45 * time.Second
)

// .
type Options struct {
	Debounce  time.Duration
	Heartbeat time.Duration
	// .
	// .
	// .
	File string
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
	Depth int
}

// .
type Watch struct {
	// .
	// .
	C <-chan struct{}

	c    chan struct{}
	dir  string
	file string
	gate *quiesce.Gate
	fw   *fsnotify.Watcher

	depth   int
	watched map[string]bool
	dirty   bool
}

// .
// .
// .
// .
func New(ctx context.Context, gate *quiesce.Gate, dir string, opts Options) *Watch {
	if opts.Debounce <= 0 {
		opts.Debounce = DefaultDebounce
	}
	if opts.Heartbeat <= 0 {
		opts.Heartbeat = DefaultHeartbeat
	}
	w := &Watch{
		c:       make(chan struct{}, 1),
		dir:     dir,
		file:    opts.File,
		gate:    gate,
		depth:   opts.Depth,
		watched: map[string]bool{},
	}
	w.C = w.c
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("fsdir: %s: no event plane (%v) — heartbeat-only at %s", dir, err, opts.Heartbeat)
	} else {
		w.fw = fw
	}
	// .
	// .
	// .
	// .
	// .
	// .
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

	// .
	// .
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
		// .
		// .
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
			if w.depth > 0 {
				// .
				// .
				// .
				// .
				w.tryAdd()
			}
			w.dirty = true
			arm()

		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			// .
			// .
			// .
			log.Printf("fsdir: %s: event plane error (%v) — forcing a look", w.dir, err)
			w.dirty = true
			arm()

		case <-debC:
			debC = nil
			if w.gate.Paused() {
				continue
			}
			w.dirty = false
			w.poke()

		case <-hb.C:
			// .
			w.tryAdd()
			w.dirty = false
			w.poke()
		}
	}
}

// .
func (w *Watch) poke() {
	select {
	case w.c <- struct{}{}:
	default:
	}
}

// .
// .
// .
// .
// .
func (w *Watch) tryAdd() {
	if w.fw == nil {
		return
	}
	want := make(map[string]bool, len(w.watched)+1)
	if _, err := os.Stat(w.dir); err == nil {
		want[w.dir] = true
		if w.depth > 0 {
			// .
			// .
			// .
			if ents, err := os.ReadDir(w.dir); err == nil {
				for _, e := range ents {
					if e.IsDir() {
						want[filepath.Join(w.dir, e.Name())] = true
					}
				}
			}
		}
	}
	for p := range w.watched {
		if want[p] {
			continue
		}
		// .
		// .
		// .
		_ = w.fw.Remove(p)
		delete(w.watched, p)
	}
	for p := range want {
		if w.watched[p] {
			continue
		}
		if err := w.fw.Add(p); err != nil {
			// .
			// .
			continue
		}
		w.watched[p] = true
	}
}
