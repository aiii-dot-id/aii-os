package logsink

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestSink installs a sink into a temp dir and restores the global
// logger on cleanup — Install tees log's default output, and that must
// never leak into other packages' test output.
func newTestSink(t *testing.T, cfg Config) *Sink {
	t.Helper()
	if cfg.Dir == "" {
		cfg.Dir = t.TempDir()
	}
	s, err := Install(cfg)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if s == nil {
		t.Fatal("Install returned nil sink for enabled config")
	}
	t.Cleanup(s.Close)
	return s
}

// writeLive appends lines to the live log through its real file.
func writeLive(t *testing.T, s *Sink, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(s.Dir(), LiveName), os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		t.Fatalf("open live: %v", err)
	}
	defer f.Close()
	for _, l := range lines {
		if _, err := f.WriteString(l + "\n"); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
}

// rotatedNames lists what actually exists on disk, oldest first.
func rotatedNames(t *testing.T, dir string) []string {
	t.Helper()
	names, err := (&Sink{dir: dir}).rotated()
	if err != nil {
		t.Fatalf("rotated: %v", err)
	}
	return names
}

func TestConfigResolution(t *testing.T) {
	tests := []struct {
		name                   string
		cfg                    Config
		wantEnabled            bool
		wantMax, wantCompressD int
	}{
		{"empty dir disables", Config{}, false, 9, 7},
		{"dir enables", Config{Dir: "log"}, true, 9, 7},
		{"explicit backup cap", Config{Dir: "log", MaxBackups: 3}, true, 3, 7},
		{"keep-all sentinel", Config{Dir: "log", MaxBackups: -1}, true, -1, 7},
		{"explicit compress age", Config{Dir: "log", CompressDays: 2}, true, 9, 2},
		{"never-compress sentinel", Config{Dir: "log", CompressDays: -1}, true, 9, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.Enabled(); got != tt.wantEnabled {
				t.Errorf("Enabled() = %v, want %v", got, tt.wantEnabled)
			}
			if got := tt.cfg.maxBackups(); got != tt.wantMax {
				t.Errorf("maxBackups() = %d, want %d", got, tt.wantMax)
			}
			if got := tt.cfg.compressDays(); got != tt.wantCompressD {
				t.Errorf("compressDays() = %d, want %d", got, tt.wantCompressD)
			}
		})
	}
}

func TestInstallDisabledReturnsNil(t *testing.T) {
	s, err := Install(Config{})
	if s != nil || err != nil {
		t.Fatalf("Install(disabled) = (%v, %v), want (nil, nil)", s, err)
	}
}

func TestInstallCreatesLiveLog(t *testing.T) {
	s := newTestSink(t, Config{})
	if _, err := os.Stat(filepath.Join(s.Dir(), LiveName)); err != nil {
		t.Fatalf("live log missing after Install: %v", err)
	}
}

func TestInstallRefusesUnusableDir(t *testing.T) {
	// A file where the directory should be: MkdirAll must fail loudly
	// (the stated intent is honored or rejected, never silently
	// ignored).
	dir := t.TempDir()
	blocked := filepath.Join(dir, "occupied")
	if err := os.WriteFile(blocked, []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(Config{Dir: filepath.Join(blocked, "log")}); err == nil {
		t.Fatal("Install under a file must fail")
	}
}

func TestRotateOnReinstall(t *testing.T) {
	dir := t.TempDir()
	s1, err := Install(Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	writeLive(t, s1, "boot one", "boot one end")
	s1.Close()

	s2, err := Install(Config{Dir: dir})
	if err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	t.Cleanup(s2.Close)

	names := rotatedNames(t, dir)
	if len(names) != 1 || !strings.HasPrefix(names[0], rotatedPrefix) {
		t.Fatalf("expected one rotated log, got %v", names)
	}
	lines, err := s2.Tail(names[0], 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0] != "boot one" {
		t.Errorf("rotated content = %v, want both boot-one lines", lines)
	}
}

func TestRotateEmptyLiveRemoved(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, LiveName), nil, 0o640); err != nil {
		t.Fatal(err)
	}
	s := newTestSink(t, Config{Dir: dir})
	_ = s
	if names := rotatedNames(t, dir); len(names) != 0 {
		t.Errorf("empty live log must not become a rotated file, got %v", names)
	}
}

func TestRotateSameTimestampCollision(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, LiveName)
	if err := os.WriteFile(live, []byte("history\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2009, 11, 10, 23, 0, 0, 0, time.UTC)
	if err := os.Chtimes(live, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	// Occupy the natural rotation name for that mtime.
	if err := os.WriteFile(filepath.Join(dir, "aii-20091110-230000.log"), []byte("occupied\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	// CompressDays:-1 isolates rotation from retention — with the
	// default policy, Install would gzip the aged file that same
	// second (proven by this test's first failing run).
	s := newTestSink(t, Config{Dir: dir, CompressDays: -1, MaxBackups: -1})

	want := "aii-20091110-230000-1.log"
	if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
		t.Fatalf("collision suffix missing: %v (names: %v)", err, rotatedNames(t, dir))
	}
	lines, err := s.Tail(want, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0] != "history" {
		t.Errorf("collided rotation lost history: %v", lines)
	}
}

func TestPruneKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	var oldest, newest string
	for i := 1; i <= 12; i++ {
		n := "aii-20250101-00" + pad2(i) + ".log"
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o640); err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			oldest = n
		}
		if i == 12 {
			newest = n
		}
	}
	s := &Sink{cfg: Config{MaxBackups: 3}, dir: dir}
	removed, err := s.Prune()
	if err != nil {
		t.Fatal(err)
	}
	if removed != 9 {
		t.Errorf("removed %d, want 9", removed)
	}
	names := rotatedNames(t, dir)
	if len(names) != 3 || names[0] != "aii-20250101-0010.log" || names[2] != newest {
		t.Errorf("survivors = %v, want the three newest", names)
	}
	if _, err := os.Stat(filepath.Join(dir, oldest)); !os.IsNotExist(err) {
		t.Errorf("oldest log survived pruning")
	}
	_ = newest
}

func pad2(i int) string {
	if i < 10 {
		return "0" + string(rune('0'+i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}

func TestPruneKeepAllSentinel(t *testing.T) {
	dir := t.TempDir()
	for i := 1; i <= 12; i++ {
		n := "aii-20250101-00" + pad2(i) + ".log"
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	s := &Sink{cfg: Config{MaxBackups: -1}, dir: dir}
	removed, err := s.Prune()
	if err != nil || removed != 0 {
		t.Fatalf("Prune(keep-all) = (%d, %v), want (0, nil)", removed, err)
	}
	if names := rotatedNames(t, dir); len(names) != 12 {
		t.Errorf("keep-all evicted %d logs", 12-len(names))
	}
}

func TestCompressOlderByAge(t *testing.T) {
	dir := t.TempDir()
	oldName := "aii-20250101-0001.log"
	freshName := "aii-20250102-0002.log"
	for _, n := range []string{oldName, freshName} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("line one\nline two\n"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	// Age only the first file past the cutoff; the clock is fakeable
	// with Chtimes, no injection needed.
	old := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, oldName), old, old); err != nil {
		t.Fatal(err)
	}

	s := &Sink{cfg: Config{CompressDays: 7, MaxBackups: -1}, dir: dir}
	gz, rm, err := s.CompressOlder()
	if err != nil {
		t.Fatal(err)
	}
	if gz != 1 || rm != 0 {
		t.Fatalf("CompressOlder = (%d, %d), want (1, 0)", gz, rm)
	}
	if _, err := os.Stat(filepath.Join(dir, oldName)); !os.IsNotExist(err) {
		t.Errorf("original survived compression")
	}
	lines, err := s.Tail(oldName+gzipExt, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[1] != "line two" {
		t.Errorf("decompressed content = %v", lines)
	}
	if _, err := os.Stat(filepath.Join(dir, freshName)); err != nil {
		t.Errorf("fresh log compressed or removed: %v", err)
	}
}

func TestCompressNeverSentinel(t *testing.T) {
	dir := t.TempDir()
	n := "aii-20250101-0001.log"
	if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-365 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, n), old, old); err != nil {
		t.Fatal(err)
	}
	s := &Sink{cfg: Config{CompressDays: -1, MaxBackups: -1}, dir: dir}
	gz, rm, err := s.CompressOlder()
	if err != nil || gz != 0 || rm != 0 {
		t.Fatalf("CompressOlder(never) = (%d, %d, %v), want (0, 0, nil)", gz, rm, err)
	}
}

func TestListOrderAndFlags(t *testing.T) {
	s := newTestSink(t, Config{MaxBackups: -1})
	writeLive(t, s, "live line")
	dir := s.Dir()
	for _, n := range []string{"aii-20250101-0001.log", "aii-20250101-0002.log"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "aii-20250101-0000.log.gz"), []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	files, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{LiveName, "aii-20250101-0002.log", "aii-20250101-0001.log", "aii-20250101-0000.log.gz"}
	if len(files) != len(want) {
		t.Fatalf("List() returned %d files, want %d: %v", len(files), len(want), files)
	}
	for i, w := range want {
		if files[i].Name != w {
			t.Errorf("files[%d] = %q, want %q", i, files[i].Name, w)
		}
	}
	if !files[3].Compressed {
		t.Errorf(".gz file not flagged compressed")
	}
	if files[0].Size == 0 {
		t.Errorf("live log size not reported")
	}
}

func TestTailLastN(t *testing.T) {
	s := newTestSink(t, Config{})
	var lines []string
	for i := 1; i <= 10; i++ {
		lines = append(lines, "l"+pad2(i))
	}
	writeLive(t, s, lines...)
	got, err := s.Tail(LiveName, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != "l08" || got[2] != "l10" {
		t.Errorf("Tail(3) = %v, want last three lines", got)
	}
}

func TestTailDefaultCap(t *testing.T) {
	s := newTestSink(t, Config{})
	var all []string
	for i := 1; i <= 450; i++ {
		all = append(all, fmt.Sprintf("l%04d", i))
	}
	writeLive(t, s, all...)
	got, err := s.Tail(LiveName, 0) // n<=0 → default 400
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 400 {
		t.Fatalf("Tail(0) returned %d lines, want the 400 default", len(got))
	}
	if got[0] != "l0051" || got[399] != "l0450" {
		t.Errorf("default-cap window wrong: first=%q last=%q", got[0], got[399])
	}
}

func TestTailRejectsForeignNames(t *testing.T) {
	s := newTestSink(t, Config{})
	writeLive(t, s, "data")
	for _, name := range []string{
		"../config.json",
		"/etc/passwd",
		"sub/aii-x.log",
		"ledger.jsonl",
		"aii-notes.txt",
	} {
		if _, err := s.Tail(name, 10); err == nil {
			t.Errorf("Tail(%q) accepted a name the sink does not own", name)
		}
	}
}

func TestTailMissingFile(t *testing.T) {
	s := newTestSink(t, Config{})
	if _, err := s.Tail("aii-20990101-000000.log", 10); err == nil {
		t.Error("Tail(missing) must error")
	}
}
