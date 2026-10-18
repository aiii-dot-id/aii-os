//go:build windows

package app

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// standAsTheCopy makes the test stand where a copy started by its helper
// stands: created after it (thisProcessCreated). A helper is started by the
// test, so without this it is a process created after this one, which is
// what a number given again looks like.
func standAsTheCopy(t *testing.T) {
	t.Helper()
	prev := thisProcessCreated
	t.Cleanup(func() { thisProcessCreated = prev })
	now := time.Now()
	thisProcessCreated = func() (time.Time, bool) { return now, true }
}

// THE COPY A WINDOWS HAND-OVER STARTS NAMES THIS PROCESS, so it waits for
// this process to end before it asks for the identity's claim
// (TestAHandedOverCopyWaitsForTheProcessItTookOverFrom shows the wait).
func TestTheWindowsHandOverNamesTheProcessItLeaves(t *testing.T) {
	cmd, err := handOverCommand()
	if err != nil {
		t.Fatal(err)
	}
	want := handoverFromEnv + "=" + strconv.Itoa(os.Getpid())
	if !slices.Contains(cmd.Env, want) {
		t.Fatalf("THE COPY A HAND-OVER STARTS DOES NOT NAME THE PROCESS IT TAKES OVER FROM: %q is not in its environment", want)
	}
}

// A NUMBER GIVEN AGAIN IS SAID SO, AND NOT WAITED FOR. Windows reuses a
// process id once its process has ended and every handle on it is closed,
// so the number a copy was handed can name an unrelated program by the time
// the copy looks. The copy waited for that program to the end of the wait,
// and said the process it took over from "had not ended". Here a process
// this test starts, so created after it, stands for the later program
// under the old number: the copy does not wait for it, says the old process
// had ended and its number was given again, and takes the claim.
func TestAHandOverToANumberGivenAgainIsSaidSo(t *testing.T) {
	if os.Getenv("AII_HANDOVER_LATER") != "" {
		fmt.Println("started")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestAHandOverToANumberGivenAgainIsSaidSo$")
	cmd.Env = append(os.Environ(), "AII_HANDOVER_LATER=1")
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
	t.Cleanup(func() { in.Close(); _ = cmd.Wait() })
	if line, err := bufio.NewReader(out).ReadString('\n'); err != nil || strings.TrimSpace(line) != "started" {
		t.Fatalf("rig: the later process did not start: %q %v", line, err)
	}

	dir := t.TempDir()
	cfg := safebootConfig(t, dir, "Later", filepath.Join(dir, "identity.sec"), filepath.Join(dir, "ledger.jsonl"), filepath.Join(dir, "aii.db"))
	cfg.Updates.HandoverWaitSeconds = 5
	a := New(cfg)
	t.Cleanup(func() { stopApp(t, a) })
	t.Setenv(handoverFromEnv, strconv.Itoa(cmd.Process.Pid))
	logs := logsink.CaptureForTest(t)
	start := time.Now()
	if _, err := a.prepareStart(); err != nil {
		t.Fatalf("the copy was refused the claim: %v", err)
	}
	if waited := time.Since(start); waited >= 5*time.Second {
		t.Fatalf("A COPY WAITED %s FOR A LATER PROCESS UNDER THE OLD NUMBER", waited)
	}
	want := fmt.Sprintf("this start took the identity over from process %d, and the process that number names now was created after this one", cmd.Process.Pid)
	if !strings.Contains(logs.String(), want) || strings.Contains(logs.String(), "had not ended") {
		t.Fatalf("the start does not say the number was given again:\n%s", logs.String())
	}
}
