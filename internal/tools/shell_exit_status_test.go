//go:build !windows

package tools

import (
	"context"
	"testing"
	"time"
)

func TestAShellRunSaysTheStatusItExitedWith(t *testing.T) {
	status := func(n int) *int { return &n }
	for _, c := range []struct {
		name, command string
		ceiling       time.Duration
		stop          bool
		want          *int
	}{
		{"a clean exit", "true", 20 * time.Second, false, status(0)},
		{"a clean exit that wrote", "echo done", 20 * time.Second, false, status(0)},
		{"a non-zero exit", "echo begun; exit 3", 20 * time.Second, false, status(3)},
		{"a search that found nothing", "echo a | grep b", 20 * time.Second, false, status(1)},
		{"a clean exit with a child still holding the output", "sleep 4 & echo begun; exit 0", 20 * time.Second, false, status(0)},
		{"ended by the ceiling", "echo begun; sleep 30", 1500 * time.Millisecond, false, nil},
		{"ended with its turn", "sleep 30", 20 * time.Second, true, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := &ShellTool{timeout: c.ceiling, sandbox: t.TempDir()}
			ctx, stop := context.WithCancel(context.Background())
			defer stop()
			if c.stop {
				time.AfterFunc(300*time.Millisecond, stop)
			}
			res, err := st.Execute(ctx, map[string]any{"command": c.command})
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case c.want == nil && res.ExitStatus != nil:
				t.Fatalf("a run that did not exit reports the status %d: %+v", *res.ExitStatus, res)
			case c.want != nil && (res.ExitStatus == nil || *res.ExitStatus != *c.want):
				t.Fatalf("exit status %v, want %d: %+v", res.ExitStatus, *c.want, res)
			}
			if (c.want == nil) != res.Interrupted {
				t.Fatalf("a run has a status or was ended, and never both or neither: %+v", res)
			}
		})
	}
}
