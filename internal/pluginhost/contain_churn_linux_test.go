//go:build linux && !android

package pluginhost

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

func TestContainedEnginesStartAndOpenUnderMountChurn(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bubblewrap is absent: containment refuses instead, which TestContainmentRefusesRatherThanRunningBare covers")
	}
	skipWhereTheSandboxCannotBeEstablished(t)
	if os.Geteuid() != 0 {
		t.Skip("mount churn needs mounts in a private mount namespace (root); the supervisor's own tests cover the rule without it")
	}
	if _, err := exec.LookPath("mount"); err != nil {
		t.Skip("no mount(8) to churn with")
	}

	runtime.LockOSThread() // never unlocked: the thread and its namespace leave with the test
	if err := syscall.Unshare(syscall.CLONE_NEWNS); err != nil {
		t.Skipf("cannot enter a private mount namespace here: %v", err)
	}
	for _, flags := range []uintptr{syscall.MS_REC | syscall.MS_PRIVATE, syscall.MS_REC | syscall.MS_SHARED} {
		if err := syscall.Mount("", "/", "", flags, ""); err != nil {
			t.Fatalf("propagation of / in the test's namespace: %v", err)
		}
	}
	base := t.TempDir()
	// A churner stops itself between iterations (killing the shell could
	// leave a mount(8) it started to land after the cleanup), and what is
	// still mounted is then detached: an umount fails EBUSY while a
	// sandbox being built holds its propagated copy.
	stop := filepath.Join(base, "stop")
	var churners []*exec.Cmd
	t.Cleanup(func() {
		_ = os.WriteFile(stop, nil, 0o600)
		for _, c := range churners {
			_ = c.Wait()
		}
		matches, _ := filepath.Glob(filepath.Join(base, "m*"))
		for _, m := range matches {
			for syscall.Unmount(m, syscall.MNT_DETACH) == nil {
			}
		}
	})
	for _, name := range []string{"m1", "m2"} {
		dir := filepath.Join(base, name)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		c := exec.Command("/bin/sh", "-c", `until [ -e "$2" ]; do mount -t tmpfs churn "$1" 2>/dev/null; umount "$1" 2>/dev/null; done`, "churn", dir, stop)
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		churners = append(churners, c)
	}

	// CONTROL: the churn breaks bare builds of the same root, or a
	// journey passing below proves nothing.
	broken := 0
	for i := 0; i < 300 && broken == 0; i++ {
		if exec.Command("bwrap", "--unshare-all", "--die-with-parent", "--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc", "--", "/bin/true").Run() != nil {
			broken++
		}
	}
	if broken == 0 {
		t.Fatal("rig: 300 bare builds under churn all held, so this host's bwrap does not race (bubblewrap 0.13+ applies the read-only flags atomically) or the churn is not reaching it — the journey below would prove nothing")
	}

	capture := logsink.CaptureForTest(t)
	const journeys = 40
	for i := 0; i < journeys; i++ {
		namespaceProven.Store(false) // the preflight builds the same root: prove it each time
		argv, wall, err := containArgv(context.Background(), []string{fakechildBin, "session-audio"}, nil, nativeFiles{})
		if err != nil {
			t.Fatalf("journey %d: containment refused under mount churn: %v", i, err)
		}
		if err := startChurnJourney(supervisor.Spec{Argv: argv, ArgvContainment: wall}); err != nil {
			t.Fatalf("journey %d: %v", i, err)
		}
	}
	t.Logf("%d journeys under churn; %d builds spawned again", journeys, strings.Count(capture.String(), "containment not built"))
}

// startChurnJourney starts the contained loopback engine, opens a session
// with audio, and closes everything: the 0.1.14 failure's journey.
func startChurnJourney(spec supervisor.Spec) error {
	spec.PluginID, spec.SessionMode, spec.AudioPair = "audio.voice", true, true
	sup, err := supervisor.Start(spec, nopDispatcher{})
	if err != nil {
		return err
	}
	defer sup.Close()
	ap := &ActivePlugin{ID: "audio.voice", sup: sup}
	if err := ap.bindVoiceSession(sup); err != nil {
		return err
	}
	defer ap.sessionCancel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	plane := audio.NewPlane()
	src, err := audio.NewFileSource(bytes.NewReader(pcmRamp(100)), mono16k, 320)
	if err != nil {
		return err
	}
	if err := plane.Register(&audio.Endpoint{ID: "mic", Source: src}); err != nil {
		return err
	}
	if err := plane.Register(&audio.Endpoint{ID: "spk", Sink: audio.NewCaptureSink(mono16k)}); err != nil {
		return err
	}
	b, err := plane.Bind("s1", "mic", "spk", true)
	if err != nil {
		return err
	}
	if err := ap.Voice.Load().OpenWithAudio(ctx, "s1", b, nil); err != nil {
		return err
	}
	return ap.Voice.Load().Close(ctx, "abort", "done")
}
