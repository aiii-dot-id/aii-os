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
package logsink

import (
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// .
const LiveName = "aii.log"

// .
// .
// .
const (
	rotatedPrefix = "aii-"
	rotatedExt    = ".log"
	gzipExt       = ".gz"
)

// .
type Config struct {
	// .
	// .
	// .
	// .
	// .
	Dir string
	// .
	// .
	MaxBackups int
	// .
	// .
	CompressDays int
}

// .
func (c Config) Enabled() bool { return c.Dir != "" }

// .
// .
func (c Config) maxBackups() int {
	if c.MaxBackups < 0 {
		return -1
	}
	if c.MaxBackups == 0 {
		return 9
	}
	return c.MaxBackups
}

// .
// .
func (c Config) compressDays() int {
	if c.CompressDays < 0 {
		return 0
	}
	if c.CompressDays == 0 {
		return 7
	}
	return c.CompressDays
}

// .
// .
type Sink struct {
	cfg  Config
	dir  string
	file *os.File
}

// .
// .
// .
// .
// .
// .
// .
func Install(cfg Config) (*Sink, error) {
	if !cfg.Enabled() {
		return nil, nil
	}
	// .
	// .
	// .
	dir := cfg.Dir
	if !filepath.IsAbs(dir) {
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("logsink: cannot create %s: %w", dir, err)
	}
	live := filepath.Join(dir, LiveName)
	if err := rotateIfPresent(live); err != nil {
		return nil, fmt.Errorf("logsink: rotate: %w", err)
	}
	// .
	// .
	f, err := atomicfile.OpenAppendRemovable(live, 0o640)
	if err != nil {
		return nil, fmt.Errorf("logsink: cannot open %s: %w", live, err)
	}
	s := &Sink{cfg: cfg, dir: dir, file: f}
	log.SetOutput(tee{file: f, stderr: os.Stderr})

	// .
	if gz, rm, err := s.CompressOlder(); err != nil {
		log.Printf("LOGS: retention error: %v", err)
	} else if gz+rm > 0 {
		log.Printf("LOGS: compressed %d, removed %d rotated log(s)", gz, rm)
	}
	return s, nil
}

// .
// .
// .
func (s *Sink) Close() {
	log.SetOutput(os.Stderr)
	if s == nil || s.file == nil {
		return
	}
	_ = s.file.Close()
	s.file = nil
}

// .
func (s *Sink) Dir() string { return s.dir }

// .
// .
// .
// .
// .
func rotateIfPresent(live string) error {
	fi, err := os.Stat(live)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Size() == 0 {
		return os.Remove(live)
	}
	base := filepath.Join(filepath.Dir(live),
		rotatedPrefix+fi.ModTime().UTC().Format("20060102-150405")+rotatedExt)
	target := base
	for i := 1; ; i++ {
		if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
			break
		}
		target = fmt.Sprintf("%s-%d%s", strings.TrimSuffix(base, rotatedExt), i, rotatedExt)
	}
	return os.Rename(live, target)
}
