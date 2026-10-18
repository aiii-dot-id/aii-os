//go:build !windows

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAShellRunThatWasEndedFailedAndSaysWhatEndedIt(t *testing.T) {
	const beat = 1500 * time.Millisecond
	for _, c := range []struct {
		name          string
		ceiling, turn time.Duration // turn is the turn's own time; 0 is none
		stop          bool          // the turn is stopped once the command has begun
		command, want string
		code          string
	}{
		{name: "the shell's ceiling", ceiling: beat, command: "echo begun; sleep 30",
			want: "begun\n[timed out after 1.5s — no exit status; output above is partial]", code: ReasonShellTimeout},
		{name: "the shell's ceiling, nothing written", ceiling: beat, command: "sleep 30",
			want: "[timed out after 1.5s — no exit status; no output]", code: ReasonShellTimeout},
		{name: "the turn's stop", ceiling: 20 * time.Second, stop: true, command: "echo begun; touch started; sleep 30",
			want: "begun\n[the turn was stopped before this command finished — no exit status; output above is partial]"},
		{name: "the turn's own time", ceiling: 20 * time.Second, turn: beat, command: "echo begun; sleep 30",
			want: "begun\n[the turn ran out of time before this command finished — no exit status; output above is partial]"},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			st := &ShellTool{timeout: c.ceiling, sandbox: home}
			ctx, stop := context.WithCancel(context.Background())
			defer stop()
			if c.turn > 0 {
				var done context.CancelFunc
				ctx, done = context.WithTimeout(ctx, c.turn)
				defer done()
			}
			if c.stop {
				go func() {
					for ctx.Err() == nil {
						if _, err := os.Stat(filepath.Join(home, "started")); err == nil {
							stop()
							return
						}
						time.Sleep(10 * time.Millisecond)
					}
				}()
			}
			began := time.Now()
			res, err := st.Execute(ctx, map[string]any{"command": c.command})
			if err != nil {
				t.Fatal(err)
			}
			if held := time.Since(began); held > 10*time.Second {
				t.Fatalf("the call held for %s after what ended it", held)
			}
			if !res.Interrupted || !res.Failed() {
				t.Fatalf("a run that was ended is a failed call: %+v", res)
			}
			if res.Output != c.want {
				t.Fatalf("what the identity is told:\n got %q\nwant %q", res.Output, c.want)
			}
			// Still the output, never an error in its place.
			if res.Error != "" || res.Text() != c.want {
				t.Fatalf("what the command wrote must still be shown: error %q, text %q", res.Error, res.Text())
			}
			if res.ReasonCode != c.code {
				t.Fatalf("reason code %q, want %q", res.ReasonCode, c.code)
			}
		})
	}
}

// A run that finished was not ended: a non-zero exit is an answer, and so
// is a clean exit whose child still holds the output.
func TestAShellRunThatFinishedIsNotCalledEnded(t *testing.T) {
	st := &ShellTool{timeout: 20 * time.Second, sandbox: t.TempDir()}
	for _, c := range []struct{ name, command, want string }{
		{"a non-zero exit", "echo begun; exit 3", "begun\n[exit status 3]"},
		{"a silent success", "true", ""},
		{"a clean exit with a child still holding the output", "sleep 4 & echo begun; exit 0", "still held its output"},
	} {
		t.Run(c.name, func(t *testing.T) {
			res, err := st.Execute(context.Background(), map[string]any{"command": c.command})
			if err != nil {
				t.Fatal(err)
			}
			if res.Interrupted || res.Failed() || res.ReasonCode != "" {
				t.Fatalf("a run that finished is not a failed call: %+v", res)
			}
			if res.Output != c.want && (c.want == "" || !strings.Contains(res.Output, c.want)) {
				t.Fatalf("got %q, want %q", res.Output, c.want)
			}
		})
	}
}
