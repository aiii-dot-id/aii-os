package supervisor

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

func TestACallIsToldWithItsTimeAndWhetherItWasTheChildsFirst(t *testing.T) {
	capture := logsink.CaptureForTest(t)
	levels := seeLevels()
	before := time.Now().Truncate(time.Second)
	s := mustStart(t, childSpec("respond"), nil)
	after := time.Now()
	for i := 0; i < 2; i++ {
		if _, err := s.Invoke(context.Background(), []byte(testReq)); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	calls := linesWith(capture, "plugins.return: plugin org.example.respond: ")
	if len(calls) != 2 {
		t.Fatalf("two calls must be told in two lines, got %d:\n%s", len(calls), capture.String())
	}
	if !strings.Contains(calls[0], "a call answered in ") {
		t.Fatalf("the first call's line must say it answered and in how long: %s", calls[0])
	}
	startedWithin(t, calls[0], firstSince, before, after)
	if !strings.Contains(calls[1], "a call answered in ") || strings.Contains(calls[1], "first since") {
		t.Fatalf("the second call's line must say how long it took, and is not the child's first: %s", calls[1])
	}
	levels.allDebug(t, "a call ")
}

// A CALL THAT RAN PAST ITS TIME NAMES WHEN THE CHILD IT STOPPED HAD
// STARTED, in the one line that already tells the timeout (its error, which
// a caller writes down). And the first call to the child that replaces it
// is that child's first.
func TestATimedOutCallNamesWhenItsChildStarted(t *testing.T) {
	capture := logsink.CaptureForTest(t)
	levels := seeLevels()
	before := time.Now().Truncate(time.Second)
	s := mustStart(t, childSpec("sleep"), nil)
	after := time.Now()
	killed := s.Pid() // the child the timeout stops; its replacement has another
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	_, err := s.Invoke(ctx, []byte(testReq))
	cancel()
	var timeout *InvokeTimeoutError
	if !errors.As(err, &timeout) {
		t.Fatalf("a call the child never answers must run past its time: %v", err)
	}
	startedWithin(t, err.Error(), killedChild, before, after)
	calls := linesWith(capture, "plugins.return: plugin org.example.sleep: ")
	if len(calls) != 1 || !strings.Contains(calls[0], "a call failed after ") {
		t.Fatalf("the call that ran past its time must be told as failed, once: %d lines:\n%s", len(calls), capture.String())
	}
	startedWithin(t, calls[0], firstSince, before, after)

	before = time.Now().Truncate(time.Second)

	waitFor(t, "the child's revival", func() bool {
		st, _ := s.State()
		pid := s.Pid()
		return st == StateRunning && pid != 0 && pid != killed
	})
	after = time.Now()
	ctx, cancel = context.WithTimeout(context.Background(), 150*time.Millisecond)
	_, _ = s.Invoke(ctx, []byte(testReq))
	cancel()
	calls = linesWith(capture, "plugins.return: plugin org.example.sleep: ")
	if len(calls) != 2 {
		t.Fatalf("the revived child's call must be told: %d lines:\n%s", len(calls), capture.String())
	}
	startedWithin(t, calls[1], firstSince, before, after)
	levels.allDebug(t, "a call ")
}

var (
	firstSince  = regexp.MustCompile(`, the first since its child started (\d{4}/\d\d/\d\d \d\d:\d\d:\d\d)$`)
	killedChild = regexp.MustCompile(`invocation deadline exceeded; child \(started (\d{4}/\d\d/\d\d \d\d:\d\d:\d\d)\) killed for restart`)
)

// startedWithin reads the child's start from text and holds it to the
// span in which the child was started (the log's own resolution, a second).
func startedWithin(t *testing.T, text string, re *regexp.Regexp, from, to time.Time) {
	t.Helper()
	m := re.FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("%q does not name when the child started (want %s)", text, re)
	}
	at, err := time.ParseInLocation("2006/01/02 15:04:05", m[1], time.Local)
	if err != nil {
		t.Fatal(err)
	}
	if at.Before(from) || at.After(to) {
		t.Fatalf("the child's start is given as %s, outside %s..%s, when it was started", at, from, to)
	}
}

func linesWith(c *logsink.Capture, text string) []string {
	var out []string
	for _, l := range c.Lines() {
		if strings.Contains(l, text) {
			out = append(out, l)
		}
	}
	return out
}

// levelsSeen keeps the level of each line by its message, beside the
// capture that reads its text: the text does not say its level. It wraps
// the capture's handler; the capture's own cleanup restores what it found.
type levelsSeen struct {
	slog.Handler
	mu   sync.Mutex
	seen map[string]slog.Level
}

func seeLevels() *levelsSeen {
	l := &levelsSeen{Handler: slog.Default().Handler(), seen: map[string]slog.Level{}}
	slog.SetDefault(slog.New(l))
	return l
}

func (l *levelsSeen) Handle(ctx context.Context, r slog.Record) error {
	l.mu.Lock()
	l.seen[r.Message] = r.Level
	l.mu.Unlock()
	return l.Handler.Handle(ctx, r)
}

// allDebug holds every line whose message holds text to debug, and asks
// that there was at least one.
func (l *levelsSeen) allDebug(t *testing.T, text string) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for msg, level := range l.seen {
		if strings.Contains(msg, text) {
			n++
			if level != slog.LevelDebug {
				t.Errorf("%q is written at %s; a line a call is debug, never louder", msg, level)
			}
		}
	}
	if n == 0 {
		t.Errorf("no line holding %q was written", text)
	}
}
