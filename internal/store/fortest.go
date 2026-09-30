package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/store/compressvfs"
)

var fresh struct {
	once  sync.Once
	image []byte
	err   error
}

func freshImage() ([]byte, error) {
	fresh.once.Do(func() { fresh.image, fresh.err = makeFreshImage() })
	return fresh.image, fresh.err
}

func makeFreshImage() ([]byte, error) {
	dir, err := os.MkdirTemp("", "store-fresh-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "fresh.db")
	s, err := New(path)
	if err != nil {
		return nil, err
	}
	if _, err := s.w().Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("checkpoint fresh database image: %w", err)
	}
	if err := s.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func SeedForTest(path string) error {
	if _, err := os.Lstat(path); err == nil {
		return nil
	}
	image, err := freshImage()
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return err
		}
	}
	return os.WriteFile(path, image, 0o644)
}

func NewForTest(path string) (*Store, error) {
	if _, err := os.Lstat(path); err == nil {
		return New(path)
	}
	if err := SeedForTest(path); err != nil {
		return nil, err
	}
	if err := compressvfs.Register(); err != nil {
		return nil, err
	}
	return openPreparedStore(path)
}
