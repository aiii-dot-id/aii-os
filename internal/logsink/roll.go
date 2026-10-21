package logsink

import (
	"os"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
)

type liveLog struct {
	mu   sync.Mutex
	path string
	f    *os.File

	size  int64
	limit int64

	rolled func(size int64, name string, err error)
}

func (l *liveLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return 0, os.ErrClosed
	}
	n, err := l.f.Write(p)
	l.size += int64(n)

	if l.limit > 0 && l.size >= l.limit {
		size := l.size
		name, rerr := l.roll()
		if l.rolled != nil {
			l.rolled(size, name, rerr)
		}
	}
	return n, err
}

func (l *liveLog) roll() (string, error) {
	l.size = 0
	name, err := rotateIfPresent(l.path, l.f)
	if err != nil {
		return "", err
	}
	f, err := atomicfile.OpenAppendRemovable(l.path, 0o640)
	if err != nil {
		return name, err
	}
	_ = l.f.Close()
	l.f = f
	return name, nil
}

func (l *liveLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

func (s *Sink) afterRoll(size int64, name string, err error) {
	s.work.Add(1)
	go func() {
		defer s.work.Done()
		switch {
		case err != nil && name == "":
			Warn("logs.error", "the live log reached %d bytes (logs.max_bytes) and could not be rolled: %v — it goes on growing, and the roll is tried again when as many bytes again are written", size, err)
		case err != nil:
			Warn("logs.error", "the live log reached %d bytes (logs.max_bytes) and was rolled to %s, but a fresh %s could not be opened: %v — the lines go on into %s, and a fresh one is tried again when as many bytes again are written", size, name, LiveName, err, name)
		case name == "":
			Info("logs.end", "the live log reached %d bytes (logs.max_bytes) with no file at its name to roll; a fresh %s was opened and this is it", size, LiveName)
		default:
			Info("logs.end", "the live log reached %d bytes (logs.max_bytes) and was rolled to %s; this file continues it", size, name)
		}
		s.retainNow()
	}()
}
