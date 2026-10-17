//go:build (linux && !android) || (darwin && !ios)

package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// fakeContainment stands in for a sandbox whose BUILD can fail, as
// bubblewrap's does under mount churn: it counts its spawns in dir, fails
// the build on the attempts FAILS lists (without the built line, as bwrap
// exits before anything inside runs), and otherwise writes the line and
// execs what it wraps — exactly the shape containArgv gives a native child.
func fakeContainment(dir string, argv ...string) []string {
	const script = `n=0; [ -f "$1/n" ] && read n < "$1/n"; n=$((n+1)); echo "$n" > "$1/n"
case " $FAILS " in *" $n "*) echo "bwrap: Can't bind mount /oldroot/ on /newroot/: Invalid argument" >&2; exit 1;; esac
: > "$1/built"
echo event=contained >&2
shift; exec "$@"`
	return append([]string{"/bin/sh", "-c", script, "sh", dir}, argv...)
}

func builtSpec(t *testing.T, fails string, readyMark string, argv ...string) (Spec, string) {
	dir := t.TempDir()
	return Spec{
		PluginID:        "org.example.contained",
		Argv:            fakeContainment(dir, argv...),
		ArgvContainment: Containment{Description: "fake", BuiltMark: "event=contained"},
		Env:             []string{"SEV_PLUGIN_SOCKET=stdio:", "FAILS=" + fails},
		ReadyMark:       readyMark,
		Backoff:         Backoff{Initial: 20 * time.Millisecond, Max: 100 * time.Millisecond, MaxRestarts: 3},
	}, dir
}

func spawns(t *testing.T, dir string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "n"))
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// A SANDBOX THAT FAILED TO BUILD RAN NOTHING. With no readiness banner the
// child used to count as started at the spawn, so the first request went
// into a process still building its sandbox, and was lost with it ("admission
// unknown") when the build failed. Now the start waits for the built line,
// spawns again while the build fails, and counts none of it as a crash.
func TestAContainmentThatFailsToBuildIsSpawnedAgainBeforeAnythingIsSent(t *testing.T) {
	capture := logsink.CaptureForTest(t)
	spec, dir := builtSpec(t, "1 2", "", fakechildBin, "respond")
	s := mustStart(t, spec, nil)
	if _, err := os.Stat(filepath.Join(dir, "built")); err != nil {
		t.Fatalf("Start returned before the containment was built: %v", err)
	}
	if n := spawns(t, dir); n != 3 {
		t.Fatalf("two failed builds then one that held: want 3 spawns, got %d", n)
	}
	if s.Restarts() != 0 {
		t.Fatalf("a build that never ran the plugin is not its crash: restarts=%d", s.Restarts())
	}
	resp, err := s.Invoke(context.Background(), []byte(testReq))
	if err != nil || !strings.Contains(string(resp), `"answered":true`) {
		t.Fatalf("the first request must reach the built child: %v %s", err, resp)
	}
	if !strings.Contains(capture.String(), "containment not built (attempt 2 of 8), the plugin never ran; spawning again") {
		t.Fatalf("each spawn again is told:\n%s", capture.String())
	}
}

// A containment that cannot be built at all is refused after the bound,
// typed as such, with the mechanism's own words.
func TestAContainmentThatNeverBuildsIsRefusedAfterTheBound(t *testing.T) {
	spec, dir := builtSpec(t, "1 2 3 4 5 6 7 8 9", "", fakechildBin, "respond")
	_, err := Start(spec, nil)
	var exit *ChildExitError
	if !errors.As(err, &exit) || exit.Phase != PhaseContainment {
		t.Fatalf("want a containment-phase exit, got %v", err)
	}
	if !strings.Contains(strings.Join(exit.StderrTail, "\n"), "Can't bind mount") {
		t.Fatalf("the refusal carries the mechanism's words: %v", exit.StderrTail)
	}
	if n := spawns(t, dir); n != ContainmentBuildAttempts {
		t.Fatalf("want exactly %d spawns, got %d", ContainmentBuildAttempts, n)
	}
}

// Once built, the plugin ran: dying before its readiness banner is the
// plugin's own start failure, never spawned again on the containment's
// account.
func TestAChildThatDiesAfterItsContainmentIsBuiltIsNotRetried(t *testing.T) {
	spec, dir := builtSpec(t, "", "child-ready", "/bin/sh", "-c", "exit 3")
	_, err := Start(spec, nil)
	var exit *ChildExitError
	if !errors.As(err, &exit) || exit.Phase != "start" || exit.Code != 3 {
		t.Fatalf("want the plugin's own start failure (code 3), got %v", err)
	}
	if n := spawns(t, dir); n != 1 {
		t.Fatalf("the plugin ran, so nothing is spawned again: got %d spawns", n)
	}
}

// A restart builds a containment too: a crash counts once, and the builds
// that fail on the way back count nothing.
func TestARestartRidesOutAFailedBuildUncounted(t *testing.T) {
	spec, dir := builtSpec(t, "2 3", "", fakechildBin, "crash-after-respond")
	s := mustStart(t, spec, nil)
	if _, err := s.Invoke(context.Background(), []byte(testReq)); err != nil {
		t.Fatalf("first invoke: %v", err)
	}
	waitFor(t, "the restarted child running", func() bool {
		st, _ := s.State()
		return st == StateRunning && s.Restarts() == 1 && spawns(t, dir) == 4
	})
	if s.Restarts() != 1 {
		t.Fatalf("one crash, one restart: got %d", s.Restarts())
	}
	if resp, err := s.Invoke(context.Background(), []byte(testReq)); err != nil || !strings.Contains(string(resp), "answered") {
		t.Fatalf("the restarted child serves: %v %s", err, resp)
	}
}
