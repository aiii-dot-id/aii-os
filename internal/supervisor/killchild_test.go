package supervisor

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// A KILL OF THE CHILD REACHES THE CHILD AND NOTHING ELSE. Tests kill a
// plugin's child as a crash would, and once did it by the child's number,
// which can name another process by the time it is used (a number given
// again on Windows; on Unix, 0 is the caller's whole process group). The
// kill goes through the process the supervisor started. Here a process
// this test started beside the supervisor's child, as the go tool, an ssh
// session or a console host stand beside a test, is alive after the kill;
// so is this process, which goes on to see the child replaced and answer.
//
// The bystander is this test binary, run again with
// AII_KILLCHILD_BYSTANDER set: it says it is up and waits on its input.
func TestKillChildReachesTheChildAndNothingElse(t *testing.T) {
	if os.Getenv("AII_KILLCHILD_BYSTANDER") != "" {
		os.Stdout.WriteString("bystander-up\n")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		return
	}
	bystander := exec.Command(os.Args[0], "-test.run=^TestKillChildReachesTheChildAndNothingElse$")
	bystander.Env = append(os.Environ(), "AII_KILLCHILD_BYSTANDER=1")
	in, err := bystander.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := bystander.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := bystander.Start(); err != nil {
		t.Fatal(err)
	}
	ended := make(chan struct{})
	up := make(chan bool, 1)
	go func() {
		lines := bufio.NewScanner(out)
		said := false
		for lines.Scan() {
			if strings.TrimSpace(lines.Text()) == "bystander-up" && !said {
				said = true
				up <- true
			}
		}
		if !said {
			up <- false
		}
		_ = bystander.Wait()
		close(ended)
	}()
	t.Cleanup(func() { in.Close(); <-ended })
	select {
	case ok := <-up:
		if !ok {
			t.Fatal("rig: the bystander ended before it was up")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("rig: the bystander never said it was up")
	}

	s := mustStart(t, childSpec("respond"), nil)
	if _, err := s.Invoke(context.Background(), []byte(testReq)); err != nil {
		t.Fatalf("first invoke: %v", err)
	}
	first := s.Pid()
	if killed, err := s.KillChild(); err != nil || !killed {
		t.Fatalf("no live child was killed: %v", err)
	}
	waitFor(t, "the child replaced and answering", func() bool {
		resp, ierr := s.Invoke(context.Background(), []byte(testReq))
		return ierr == nil && strings.Contains(string(resp), "answered") && s.Pid() != first
	})
	select {
	case <-ended:
		t.Fatal("A KILL OF THE SUPERVISOR'S CHILD ENDED A PROCESS THE SUPERVISOR DID NOT START")
	case <-time.After(200 * time.Millisecond):
	}

	// No child live: nothing is killed, and nothing is said to be.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if killed, err := s.KillChild(); killed || err != nil {
		t.Fatalf("a closed supervisor said it killed a child: %v %v", killed, err)
	}
	select {
	case <-ended:
		t.Fatal("A KILL WITH NO CHILD LIVE ENDED A PROCESS THE SUPERVISOR DID NOT START")
	default:
	}
}
