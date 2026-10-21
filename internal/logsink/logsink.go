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
	"sync"
	"time"
)

const LiveName = "aii.log"

const (
	rotatedPrefix = "aii-"
	rotatedExt    = ".log"
	gzipExt       = ".gz"
)

type Config struct {
	Dir string

	MaxBackups int

	CompressDays int

	MaxDays int

	MaxBytes int64

	MaxTotalBytes int64
}

const DefaultMaxBytes = 5 << 20

const MinMaxBytes = tailReadBytes

const MaxMaxBytes = maxGunzipBytes / 2

func (c Config) maxBytes() int64 {
	if c.MaxBytes <= 0 {
		return DefaultMaxBytes
	}
	return c.MaxBytes
}

func DefaultMaxTotalBytes(maxBytes int64, maxBackups int) int64 {
	c := Config{MaxBytes: maxBytes, MaxBackups: maxBackups}
	if c.maxBackups() < 0 {
		return 0
	}
	return c.maxTotalBytes()
}

func (c Config) maxTotalBytes() int64 {
	if c.MaxTotalBytes > 0 {
		return c.MaxTotalBytes
	}
	return c.maxBytes() * int64(c.maxBackups()+1)
}

func (c Config) Enabled() bool { return c.Dir != "" }

func (c Config) maxDays() int {
	if c.MaxDays < 0 {
		return 0
	}
	if c.MaxDays == 0 {
		return 30
	}
	return c.MaxDays
}

func (c Config) maxBackups() int {
	if c.MaxBackups < 0 {
		return -1
	}
	if c.MaxBackups == 0 {
		return 9
	}
	return c.MaxBackups
}

func (c Config) compressDays() int {
	if c.CompressDays < 0 {
		return 0
	}
	if c.CompressDays == 0 {
		return 7
	}
	return c.CompressDays
}

type Sink struct {
	cfg  Config
	dir  string
	file *liveLog
	stop chan struct{}

	retain sync.Mutex
	work   sync.WaitGroup
}

func Install(cfg Config) (*Sink, error) {
	if !cfg.Enabled() {
		s := &Sink{cfg: cfg}
		s.installStream()
		return s, nil
	}

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
	if _, err := rotateIfPresent(live, nil); err != nil {
		return nil, fmt.Errorf("logsink: rotate: %w", err)
	}

	f, err := atomicfile.OpenAppendRemovable(live, 0o640)
	if err != nil {
		return nil, fmt.Errorf("logsink: cannot open %s: %w", live, err)
	}
	s := &Sink{cfg: cfg, dir: dir}
	s.file = &liveLog{path: live, f: f, limit: cfg.maxBytes(), rolled: s.afterRoll}
	if fi, err := f.Stat(); err == nil {
		s.file.size = fi.Size()
	}
	s.installStream()

	s.retainNow()
	return s, nil
}

func (s *Sink) retainNow() {
	s.retain.Lock()
	defer s.retain.Unlock()
	gz, rm, cut, err := s.compressAndPrune()
	if err != nil {
		Warn("logs.error", "retention error: %v", err)
	} else if gz+rm > 0 {
		Info("logs.end", "compressed %d, removed %d rotated log(s)", gz, rm)
	}
	if cut.files > 0 {
		Info("logs.end", "of those, %d (%d bytes, the oldest left) went to hold the rotated logs under %d bytes (logs.max_total_bytes), whatever their age; they now sum %d", cut.files, cut.bytes, s.cfg.maxTotalBytes(), cut.left)
	}
}

func (s *Sink) installStream() {

	if def, cat, err := ParseDirective(os.Getenv(EnvDirective)); err == nil {
		SetLevels(def, cat)
	} else {
		defer func() { Warn("logs.error", "%v — the level is unchanged", err) }()
	}
	var file io.Writer
	if s.file != nil {
		file = s.file
	}
	slog.SetDefault(slog.New(newHandler(file, stderrOrNil())))

	s.stop = make(chan struct{})
	go digestEvery(time.Hour, s.stop)

}

func (s *Sink) Close() {
	if s != nil && s.stop != nil {
		close(s.stop)
		s.stop = nil
	}

	FlushDigest()
	slog.SetDefault(slog.New(newHandler(nil, stderrOrNil())))
	log.SetOutput(os.Stderr)
	if s == nil || s.file == nil {
		return
	}
	_ = s.file.Close()
	s.file = nil

	s.work.Wait()
}

func (s *Sink) Dir() string { return s.dir }

func rotateIfPresent(live string, held *os.File) (string, error) {
	fi, err := os.Stat(live)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	mine := false
	if held != nil {
		if hfi, herr := held.Stat(); herr == nil && os.SameFile(fi, hfi) {
			fi, mine = hfi, true
		}
	}
	if fi.Size() == 0 && !mine {
		return "", os.Remove(live)
	}
	dir := filepath.Dir(live)
	stamp := fi.ModTime().UTC().Format(rotatedStampLayout)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}

	last := -1
	for _, e := range entries {
		if !isRotatedName(e.Name()) {
			continue
		}
		if s, n := rotatedStamp(e.Name()); s == stamp && n > last {
			last = n
		}
	}
	name := rotatedPrefix + stamp + rotatedExt
	if last >= 0 {
		name = fmt.Sprintf("%s%s-%d%s", rotatedPrefix, stamp, last+1, rotatedExt)
	}
	return name, os.Rename(live, filepath.Join(dir, name))
}
