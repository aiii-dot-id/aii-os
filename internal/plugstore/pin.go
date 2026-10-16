package plugstore

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/filelock"
)

var ErrCollecting = errors.New("the store's writer holds this entry at this moment")

var ErrReplaced = errors.New("the entry's record was replaced while it was pinned")

type Pin struct {
	f    *os.File
	path string
}

func (s Store) PinRoot(key string) (*Pin, error) {
	path, err := s.RootRecord(key)
	if err != nil {
		return nil, err
	}
	return pin(path)
}

func (s Store) PinView(key string) (*Pin, error) {
	path, err := s.ViewRecord(key)
	if err != nil {
		return nil, err
	}
	return pin(path)
}

func pin(path string) (*Pin, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return hold(f, path)
}

func hold(f *os.File, path string) (*Pin, error) {
	if err := filelock.RLock(f); err != nil {
		f.Close()
		if errors.Is(err, filelock.ErrHeld) {
			return nil, fmt.Errorf("pin %s: %w", path, ErrCollecting)
		}
		return nil, fmt.Errorf("pin %s: %w", path, err)
	}
	held, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("pin %s: %w", path, err)
	}
	now, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) || err == nil && !os.SameFile(held, now) {
		f.Close()
		return nil, fmt.Errorf("pin %s: %w", path, ErrReplaced)
	}
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("pin %s: %w", path, err)
	}
	noteHeld(path, 1)
	return &Pin{f: f, path: path}, nil
}

func (p *Pin) Stat() (os.FileInfo, error) { return p.f.Stat() }

func (p *Pin) Read() ([]byte, error) {
	if _, err := p.f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(p.f, maxRecordBytes))
}

func (p *Pin) Close() error {
	if p == nil || p.f == nil {
		return nil
	}
	err := p.f.Close()
	p.f = nil
	noteHeld(p.path, -1)
	return err
}

var held struct {
	mu sync.Mutex
	n  map[string]int
}

func noteHeld(path string, d int) {
	held.mu.Lock()
	defer held.mu.Unlock()
	if held.n == nil {
		held.n = map[string]int{}
	}
	if held.n[path] += d; held.n[path] <= 0 {
		delete(held.n, path)
	}
}
