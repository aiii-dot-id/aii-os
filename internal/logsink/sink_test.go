package logsink

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeTB struct{ cleanups []func() }

func (f *fakeTB) Helper()           {}
func (f *fakeTB) Cleanup(fn func()) { f.cleanups = append(f.cleanups, fn) }
func (f *fakeTB) runCleanups() {
	for i := len(f.cleanups) - 1; i >= 0; i-- {
		f.cleanups[i]()
	}
}

// installForTest points the default logger at two buffers, the way the
// real sink points it at the file and the console.
func installForTest(t *testing.T) (file, console *bytes.Buffer) {
	t.Helper()
	file, console = &bytes.Buffer{}, &bytes.Buffer{}
	prev := slog.Default()
	prevFlags, prevWriter := log.Flags(), log.Writer()
	slog.SetDefault(slog.New(newHandler(file, console)))
	SetLevels(slog.LevelInfo, nil)
	t.Cleanup(func() {
		slog.SetDefault(prev)
		log.SetOutput(prevWriter)
		log.SetFlags(prevFlags)
		SetLevels(slog.LevelInfo, nil)
	})
	return file, console
}

// THE FILE TAKES EVERYTHING; THE CONSOLE TAKES THE THRESHOLD. An
// incident must not depend on having guessed the level before it
// happened, so what is kept and what is shown are different questions.
func TestTheFileKeepsWhatTheConsoleFilters(t *testing.T) {
	file, console := installForTest(t)

	Debug("voice", "frame 412 admitted")
	if !strings.Contains(file.String(), "frame 412 admitted") {
		t.Fatalf("the file must keep a debug line: %q", file.String())
	}
	if console.Len() != 0 {
		t.Fatalf("the console must not show it at info: %q", console.String())
	}

	SetLevels(slog.LevelInfo, map[string]slog.Level{"voice": slog.LevelDebug})
	Debug("voice", "frame 413 admitted")
	if !strings.Contains(console.String(), "frame 413 admitted") {
		t.Fatalf("raising one category must show it: %q", console.String())
	}
	if strings.Contains(console.String(), "frame 412") {
		t.Fatal("raising the level must not reprint the past")
	}
}

// NO SETTING SILENCES AN ERROR. The floor is what a reader
// reconstructing an incident depends on.
func TestNoSettingSilencesAnError(t *testing.T) {
	_, console := installForTest(t)
	SetLevels(slog.Level(12), nil) // above error, as high as an operator can go
	Info("voice", "this should vanish")
	Error("safe", "SAFE MODE: entering — the ledger did not materialize")
	out := console.String()
	if strings.Contains(out, "should vanish") {
		t.Fatalf("an info line survived a threshold above it: %q", out)
	}
	if !strings.Contains(out, "SAFE MODE") {
		t.Fatalf("an error was silenced by a setting: %q", out)
	}
}

// SIXTY TICKS ARE ONE LINE THAT SAYS SIXTY.
func TestSixtyTicksBecomeOneDigest(t *testing.T) {
	file, _ := installForTest(t)
	for i := 0; i < 60; i++ {
		Tick("rhythm", "passes, none due", 5*time.Millisecond)
	}
	for i := 0; i < 7; i++ {
		Tick("route", "renewals unchanged", 0)
	}
	if file.Len() != 0 {
		t.Fatalf("a tick must not write a line: %q", file.String())
	}
	FlushDigest()
	lines := strings.Count(strings.TrimSuffix(file.String(), "\n"), "\n") + 1
	if lines != 1 {
		t.Fatalf("the quiet must be one line, got %d:\n%s", lines, file.String())
	}
	got := file.String()
	if !strings.Contains(got, "rhythm 60 passes, none due") || !strings.Contains(got, "route 7 renewals unchanged") {
		t.Fatalf("the digest must carry the counts: %q", got)
	}
	if !strings.Contains(got, " logs.pass: ") {
		t.Fatalf("the digest is told under its declared name: %q", got)
	}
	file.Reset()
	FlushDigest()
	if file.Len() != 0 {
		t.Fatalf("a second flush with nothing to say must be silent: %q", file.String())
	}
}

// AN UNCONVERTED CALL SITE KEEPS ITS SHAPE. 638 of them bridge through
// slog untouched; if the line changed, the conversion would not be
// incremental, it would be a flag day.
func TestAnUnconvertedLogPrintfIsUnchanged(t *testing.T) {
	file, _ := installForTest(t)
	log.Printf("VOICE: session vs-7 opened by the page")
	got := strings.TrimSuffix(file.String(), "\n")
	want := regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} VOICE: session vs-7 opened by the page$`)
	if !want.MatchString(got) {
		t.Fatalf("the bridged line changed shape: %q", got)
	}
}

// A CONVERTED LINE CARRIES ITS CATEGORY, AND ITS TURN WHILE ONE RUNS.
func TestACategoryAndATurnAppearInTheLine(t *testing.T) {
	file, _ := installForTest(t)
	Info("voice", "session vs-7 opened")
	if !strings.Contains(file.String(), " voice: session vs-7 opened") {
		t.Fatalf("category missing: %q", file.String())
	}
	file.Reset()
	ctx := WithSource(context.Background(), Source{Turn: "t7f3"})
	InfoCtx(ctx, "llm", "iteration 2 finish=tool_calls")
	if !strings.Contains(file.String(), " llm[t7f3]: iteration 2") {
		t.Fatalf("turn id missing: %q", file.String())
	}
	file.Reset()
	WarnCtx(WithWorker(ctx, "sa-2"), "llm", "the sub-agent retried")
	if !strings.Contains(file.String(), " llm[t7f3/sa-2]: the sub-agent retried") {
		t.Fatalf("worker missing from the stamp: %q", file.String())
	}
	file.Reset()
	Info("llm", "no owner is known here")
	if strings.Contains(file.String(), "[") {
		t.Fatalf("a line with no owner must carry no stamp: %q", file.String())
	}
}

// CONCURRENT OWNERS ARE EACH STAMPED WITH THEIR OWN.
//
// This is why the owner moved into the context. A process-wide turn id
// could not be right once `agency.max_parallel_subagents` runs several
// sub-agents at once: the last writer stamps everybody, and its clear
// wipes the rest. The failure that produces is the bad kind — a line
// attributed to the wrong owner, read later by an identity trying to
// find out what it did.
//
// Run with -race, this also proves no two owners share state.
func TestConcurrentOwnersAreEachStampedWithTheirOwn(t *testing.T) {
	var out lockedBuffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(newHandler(&out, nil)))

	const workers = 8
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := WithSource(context.Background(), Source{
				Turn:   fmt.Sprintf("t%d", i),
				Worker: fmt.Sprintf("sa-%d", i),
			})
			for n := 0; n < 20; n++ {
				InfoCtx(ctx, "llm", "worker %d line %d", i, n)
			}
		}(i)
	}
	wg.Wait()

	// Every line must carry ITS OWN worker, never another's.
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var i, n int
		if _, err := fmt.Sscanf(line[strings.Index(line, "worker "):], "worker %d line %d", &i, &n); err != nil {
			t.Fatalf("unreadable line: %q", line)
		}
		want := fmt.Sprintf("llm[t%d/sa-%d]:", i, i)
		if !strings.Contains(line, want) {
			t.Fatalf("line from worker %d is stamped with another owner: %q (want %q)", i, line, want)
		}
	}
}

// lockedBuffer is a writer several goroutines may share, so the test
// above measures the stamp and not the buffer.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// A CAPTURE LEAVES THE BRIDGE AS IT FOUND IT. Eight tests in this tree
// read the log by re-pointing the standard logger; after the bridge is
// installed that either misses the lines or de-installs the bridge for
// everything after it.
func TestACaptureLeavesTheBridgeAsItFoundIt(t *testing.T) {
	file, _ := installForTest(t)
	before := slog.Default()

	tb := &fakeTB{}
	c := CaptureForTest(tb)
	Info("plugins", "id.aiii.voice activated")
	if !c.Contains("id.aiii.voice activated") {
		t.Fatalf("the capture saw nothing: %q", c.String())
	}
	if file.Len() != 0 {
		t.Fatalf("a capture must not also write the installed file: %q", file.String())
	}
	tb.runCleanups()

	if slog.Default() != before {
		t.Fatal("the capture did not restore the handler it found")
	}
	Info("plugins", "after the capture")
	if !strings.Contains(file.String(), "after the capture") {
		t.Fatalf("the bridge did not survive the capture: %q", file.String())
	}
}
