// Package logsink — persistence for the runtime's own log stream.
//
// The engine logs everything through the default logger to stderr
// (ledger, witness, boot, plugin machinery — the only structured
// channel the runtime has). stderr is a terminal or /dev/null on most
// launches: the record scrolls away unseen. logsink tees that stream
// into files beside the identity, logrotate-style:
//
//   - aii.log is the live log; every boot renames the old one to
//     aii-<UTC-timestamp>.log so history is never overwritten
//   - rotated logs older than CompressDays are gzipped in place
//   - at most MaxBackups rotated logs are kept (newest first)
//
// Disabled by default: an empty Dir installs nothing and the stream
// stays stderr-only, exactly as before. The operator points it at a
// directory in Settings → Logs; the change applies next boot.
//
// Why the default logger and not a second one: one stream is the whole
// point. Every call site in the tree (200+) already funnels through
// log.Printf; one log.SetOutput(MultiWriter) captures all of them with
// no churn — and what the operator sees on stderr stays byte-identical
// to what lands in the file.
package logsink

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// LiveName is the file currently being written.
const LiveName = "aii.log"

// rotatedPrefix/rotatedExt define the archive naming scheme. The
// timestamp format (20060102-150405) sorts lexicographically, so name
// order IS age order — no mtime arithmetic needed to keep or evict.
const (
	rotatedPrefix = "aii-"
	rotatedExt    = ".log"
	gzipExt       = ".gz"
)

// Config is the operator surface (config.json "logs" object).
type Config struct {
	// Dir is the destination directory. Empty = disabled (stderr only).
	// The default config uses "log" — relative, resolved against the
	// identity home, the same convention as data/ledger.jsonl (the
	// ENTIRELY LOCAL operator law: the install directory is the
	// identity's whole world).
	Dir string
	// MaxBackups caps rotated logs kept (the live log is never counted
	// or evicted). 0 = the default (9); negative = keep all.
	MaxBackups int
	// CompressDays gzips rotated logs older than this many days.
	// 0 = the default (7); negative = never.
	CompressDays int
}

// Enabled reports whether the sink should install.
func (c Config) Enabled() bool { return c.Dir != "" }

// maxBackups resolves the default (0 → 9; negative → keep all → -1
// sentinel meaning "no pruning").
func (c Config) maxBackups() int {
	if c.MaxBackups < 0 {
		return -1
	}
	if c.MaxBackups == 0 {
		return 9
	}
	return c.MaxBackups
}

// compressDays resolves the default (0 → 7; negative → never → 0
// sentinel meaning "no compression").
func (c Config) compressDays() int {
	if c.CompressDays < 0 {
		return 0
	}
	if c.CompressDays == 0 {
		return 7
	}
	return c.CompressDays
}

// Sink is the installed tee. Owns the open file; Close returns the
// stream to stderr-only.
type Sink struct {
	cfg  Config
	dir  string
	file *os.File
}

// Install rotates any existing live log, opens a fresh one, tees the
// default logger into it (stderr keeps flowing — the tee is additive),
// then runs retention passes. An error means the operator's stated
// destination cannot be used; callers should refuse to start rather
// than silently fall back (the stated intent must be honored loudly or
// rejected — the WorkerBinary rule).
func Install(cfg Config) (*Sink, error) {
	if !cfg.Enabled() {
		return nil, nil
	}
	// Resolve relative destinations against the process working
	// directory (= the identity home, the same convention as
	// data/ledger.jsonl) so List/Tail keep working after any chdir.
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
	f, err := os.OpenFile(live, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return nil, fmt.Errorf("logsink: cannot open %s: %w", live, err)
	}
	s := &Sink{cfg: cfg, dir: dir, file: f}
	log.SetOutput(io.MultiWriter(os.Stderr, f))

	// Retention AFTER the tee: the report lands in the file it describes.
	if gz, rm, err := s.CompressOlder(); err != nil {
		log.Printf("LOGS: retention error: %v", err)
	} else if gz+rm > 0 {
		log.Printf("LOGS: compressed %d, removed %d rotated log(s)", gz, rm)
	}
	return s, nil
}

// Close restores stderr-only output and closes the file. The final
// "Shutting down..." lines are flushed by the caller's ordering (Close
// runs last in stop()).
func (s *Sink) Close() {
	log.SetOutput(os.Stderr)
	if s == nil || s.file == nil {
		return
	}
	_ = s.file.Close()
	s.file = nil
}

// Dir reports the resolved destination directory.
func (s *Sink) Dir() string { return s.dir }

// rotateIfPresent renames a non-empty live log to a timestamped name.
// An empty live log carries nothing worth a file — it is removed so
// the fresh one starts clean. Two rotations within the same second
// (rapid restart loop) would collide on the timestamp; the -N suffix
// guards the rare case instead of overwriting history.
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
			break // name is free
		}
		target = fmt.Sprintf("%s-%d%s", strings.TrimSuffix(base, rotatedExt), i, rotatedExt)
	}
	return os.Rename(live, target)
}
