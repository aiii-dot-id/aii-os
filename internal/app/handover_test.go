package app

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// A COPY STARTED TO TAKE THE IDENTITY OVER WAITS FOR THE PROCESS IT TOOK OVER
// FROM. On Windows a relaunch starts a copy and then exits, and the copy's
// first act is the identity's claim: a copy that got there before the old
// process had exited was refused "already running", and nothing started the
// identity again. Here a helper process stands in for the old one: it holds
// the claim until told to exit. The race is shown first (a claim asked while
// the helper lives is refused), then the copy's start: named the helper as
// the process it took over from, it waits for the helper's end and takes the
// claim, and the log names the helper. With a bound the helper outlives, the
// start is refused and the refusal names the helper and the setting.
//
// The helper is this test, run again with AII_HANDOVER_HELPER_DIR set.
func TestAHandedOverCopyWaitsForTheProcessItTookOverFrom(t *testing.T) {
	if dir := os.Getenv("AII_HANDOVER_HELPER_DIR"); dir != "" {
		if _, err := (&App{}).claimIdentity(dir); err != nil {
			t.Fatalf("the helper could not take the claim: %v", err)
		}
		fmt.Println("claim-held")
		// Held until the parent says to go; the process's exit lets it go.
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		return
	}

	helper := func(t *testing.T, dir string) (pid int, release func(), done <-chan struct{}) {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestAHandedOverCopyWaitsForTheProcessItTookOverFrom$")
		cmd.Env = append(os.Environ(), "AII_HANDOVER_HELPER_DIR="+dir)
		cmd.Stderr = os.Stderr
		in, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		exited := make(chan struct{})
		lines := bufio.NewScanner(out)
		ready := make(chan bool, 1)
		go func() {
			held := false
			for lines.Scan() {
				if strings.TrimSpace(lines.Text()) == "claim-held" && !held {
					held = true
					ready <- true
				}
			}
			if !held {
				ready <- false
			}
			_ = cmd.Wait() // reaps it: an exited child that is not waited for still answers as a process
			close(exited)
		}()
		select {
		case ok := <-ready:
			if !ok {
				t.Fatal("rig: the helper ended without taking the claim")
			}
		case <-time.After(30 * time.Second):
			t.Fatal("rig: the helper never said it held the claim")
		}
		t.Cleanup(func() { in.Close(); <-exited })
		return cmd.Process.Pid, func() { fmt.Fprintln(in, "go") }, exited
	}
	rig := func(t *testing.T, wait int) (*App, string) {
		t.Helper()
		dir := t.TempDir()
		cfg := safebootConfig(t, dir, "Handed", filepath.Join(dir, "identity.sec"), filepath.Join(dir, "ledger.jsonl"), filepath.Join(dir, "aii.db"))
		cfg.Updates.HandoverWaitSeconds = wait
		a := New(cfg)
		t.Cleanup(func() { stopApp(t, a) })
		return a, dir
	}

	t.Run("the old process ends within the wait", func(t *testing.T) {
		a, dir := rig(t, 30)
		pid, release, _ := helper(t, dir)
		standAsTheCopy(t)
		// THE RACE: a claim asked while the old process lives is refused.
		if _, err := (&App{}).claimIdentity(dir); !errors.Is(err, errIdentityRunning) {
			t.Fatalf("rig: a claim beside the helper must be refused, as the copy's was: %v", err)
		}
		t.Setenv(handoverFromEnv, strconv.Itoa(pid))
		logs := logsink.CaptureForTest(t)
		go func() { time.Sleep(300 * time.Millisecond); release() }()
		start := time.Now()
		if _, err := a.prepareStart(); err != nil {
			t.Fatalf("A COPY HANDED THE IDENTITY WAS REFUSED THE CLAIM its old process held: %v", err)
		}
		if waited := time.Since(start); waited < 300*time.Millisecond {
			t.Fatalf("the copy took the claim after %s, before the old process was told to end", waited)
		}
		if !strings.Contains(logs.String(), fmt.Sprintf("took the identity over from process %d, which had ended", pid)) {
			t.Fatalf("the start does not name the process it waited for:\n%s", logs.String())
		}
		if _, set := os.LookupEnv(handoverFromEnv); set {
			t.Fatal("the copy keeps the old process's name in its environment, for a copy it starts in turn")
		}
	})

	t.Run("the old process outlives the wait", func(t *testing.T) {
		a, dir := rig(t, 1)
		pid, release, done := helper(t, dir)
		standAsTheCopy(t)
		t.Setenv(handoverFromEnv, strconv.Itoa(pid))
		_, err := a.prepareStart()
		release()
		<-done
		if !errors.Is(err, errIdentityRunning) || !strings.Contains(err.Error(), fmt.Sprintf("the process this start took the identity over from, %d, had not ended after 1s: updates.handover_wait_seconds", pid)) {
			t.Fatalf("a start whose old process outlived the wait is not refused naming it and the setting: %v", err)
		}
	})
}

// THE LOCK OF A PROCESS SEEN TO END CAN BE LET GO LATE, and a copy asks for
// the claim again until its wait runs out. On Windows an ended process's
// lock goes when its handles are closed, which may come after the process
// is signalled as ended; the copy that asked in that moment was refused as
// a second start, with nothing to start the identity again. Here the
// process the copy took over from has ended, and a claim that outlives it
// (another handle on the claim's file, in this process) is let go 300 ms
// later: the copy takes the claim then, and the log says how late. A claim
// held past the wait is refused, saying the old process had ended and
// another holds the claim.
func TestAClaimLetGoLateAfterTheHandOverIsTakenWithinTheWait(t *testing.T) {
	ended := func(t *testing.T) int {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^$")
		if err := cmd.Run(); err != nil {
			t.Fatalf("rig: a process that ends: %v", err)
		}
		standAsTheCopy(t)
		return cmd.Process.Pid
	}
	rig := func(t *testing.T, wait int) (*App, *App) {
		t.Helper()
		dir := t.TempDir()
		cfg := safebootConfig(t, dir, "Late", filepath.Join(dir, "identity.sec"), filepath.Join(dir, "ledger.jsonl"), filepath.Join(dir, "aii.db"))
		cfg.Updates.HandoverWaitSeconds = wait
		a, holder := New(cfg), &App{}
		t.Cleanup(func() { holder.releaseIdentityClaim(); stopApp(t, a) })
		if _, err := holder.claimIdentity(dir); err != nil {
			t.Fatalf("rig: the claim that outlives the old process: %v", err)
		}
		return a, holder
	}

	t.Run("let go within the wait", func(t *testing.T) {
		a, holder := rig(t, 30)
		pid := ended(t)
		t.Setenv(handoverFromEnv, strconv.Itoa(pid))
		logs := logsink.CaptureForTest(t)
		// The release is ordered before the rig's cleanup, which releases
		// the claim again: cleanups run last in, first out, so this one
		// waits for the goroutine first. The claim's file lock is the
		// copy's only sign of the release, and it orders nothing in Go.
		released := make(chan struct{})
		go func() { defer close(released); time.Sleep(300 * time.Millisecond); holder.releaseIdentityClaim() }()
		t.Cleanup(func() { <-released })
		start := time.Now()
		if _, err := a.prepareStart(); err != nil {
			t.Fatalf("A COPY WAS REFUSED A CLAIM LET GO LATE BY THE PROCESS IT TOOK OVER FROM: %v", err)
		}
		if waited := time.Since(start); waited < 300*time.Millisecond {
			t.Fatalf("the copy took the claim after %s, while it was still held", waited)
		}
		if !strings.Contains(logs.String(), fmt.Sprintf("the identity's claim was still held when process %d was seen to end, and was let go ", pid)) {
			t.Fatalf("the start does not say the claim was let go late:\n%s", logs.String())
		}
	})
	t.Run("held past the wait", func(t *testing.T) {
		a, _ := rig(t, 1)
		pid := ended(t)
		t.Setenv(handoverFromEnv, strconv.Itoa(pid))
		start := time.Now()
		_, err := a.prepareStart()
		if !errors.Is(err, errIdentityRunning) || !strings.Contains(err.Error(), fmt.Sprintf("the process this start took the identity over from, %d, had ended, and the claim was still held at the end of the wait, 1s: another process holds it", pid)) {
			t.Fatalf("a claim held past the wait is not refused naming the ended process and the wait: %v", err)
		}
		if waited := time.Since(start); waited < 900*time.Millisecond {
			t.Fatalf("the copy gave up after %s, inside a wait of a second", waited)
		}
	})
}

// updates.handover_wait_seconds is the wait's number: zero or absent for
// the default of thirty seconds, refused negative at load.
func TestTheHandOversWaitIsTheOperatorsNumber(t *testing.T) {
	if got := New(defaultConfig()).handoverWait(); got != 30*time.Second {
		t.Fatalf("with nothing in the file the wait is %s, want 30s", got)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"updates":{"handover_wait_seconds":90}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := New(cfg).handoverWait(); got != 90*time.Second {
		t.Fatalf("the file says ninety seconds and the wait is %s", got)
	}
	if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"updates":{"handover_wait_seconds":%d}}`, maxHandoverWaitSeconds)), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, err := LoadConfig(path); err != nil || New(cfg).handoverWait() != time.Hour {
		t.Fatalf("the largest wait, an hour, must load and be taken: %v", err)
	}
	// Past an hour, and past what Windows waits by (32 bits of
	// milliseconds, about 49 days), the file is refused by its key: a
	// wait that wrapped round would have been a wait of minutes.
	for _, bad := range []int{-1, maxHandoverWaitSeconds + 1, 4294968} {
		if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"updates":{"handover_wait_seconds":%d}}`, bad)), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path); err == nil || !strings.HasPrefix(err.Error(), "updates.handover_wait_seconds is ") {
			t.Fatalf("a wait of %d seconds must be refused by its key: %v", bad, err)
		}
	}
	held := defaultConfig()
	held.Updates.HandoverWaitSeconds = 4294968
	if got := New(held).handoverWait(); got != time.Hour {
		t.Fatalf("a configuration built in code past the bound waits %s, not the bound", got)
	}
}
