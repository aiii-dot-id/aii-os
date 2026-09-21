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
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
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
	MaxDays int
}

// .
func (c Config) Enabled() bool { return c.Dir != "" }

// .
func (c Config) maxDays() int {
	if c.MaxDays < 0 {
		return 0
	}
	if c.MaxDays == 0 {
		return 30
	}
	return c.MaxDays
}

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
	stop chan struct{}
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
		s := &Sink{cfg: cfg}
		s.installStream()
		return s, nil
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
	s.installStream()

	// .
	if gz, rm, err := s.CompressOlder(); err != nil {
		Warn("logs.error", "retention error: %v", err)
	} else if gz+rm > 0 {
		Info("logs.end", "compressed %d, removed %d rotated log(s)", gz, rm)
	}
	return s, nil
}

// .
func (s *Sink) installStream() {
	// .
	// .
	// .
	// .
	// .
	if def, cat, err := ParseDirective(os.Getenv(EnvDirective)); err == nil {
		SetLevels(def, cat)
	} else {
		defer func() { Warn("logs", "%v — the level is unchanged", err) }()
	}
	var file io.Writer
	if s.file != nil {
		file = s.file
	}
	slog.SetDefault(slog.New(newHandler(file, stderrOrNil())))
	// .
	s.stop = make(chan struct{})
	go digestEvery(time.Hour, s.stop)

}

// .
// .
// .
func (s *Sink) Close() {
	if s != nil && s.stop != nil {
		close(s.stop)
		s.stop = nil
	}
	// .
	FlushDigest()
	slog.SetDefault(slog.New(newHandler(nil, stderrOrNil())))
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
