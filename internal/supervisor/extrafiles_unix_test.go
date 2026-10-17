//go:build !windows

package supervisor

import (
	"bufio"
	"os"
	"strings"
	"testing"
	"time"
)

// THE SPEC'S EXTRA FILES REACH THE CHILD (T37 follow-up). A verified
// image that executes by descriptor (verified_linux.go) hands the child
// that descriptor this way, after the three standard ones: the child's
// fd 3 is the spec's first extra file. Here the child writes a line to
// its fd 3 and the parent reads it from the other end of the pipe.
func TestTheSpecsExtraFilesReachTheChild(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	s, err := Start(Spec{
		PluginID:   "extrafiles",
		Argv:       []string{"/bin/sh", "-c", "echo inherited >&3; read line"},
		ExtraFiles: []*os.File{w},
	}, nil)
	w.Close() // the child holds its own copy, or none
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Close()
	got := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(r).ReadString('\n')
		got <- line
	}()
	select {
	case line := <-got:
		if strings.TrimSpace(line) != "inherited" {
			t.Fatalf("the child's fd 3 is the spec's extra file: read %q", line)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("nothing arrived on the extra file")
	}
}
