//go:build !windows

package supervisor

import (
	"bufio"
	"os"
	"strings"
	"testing"
	"time"
)

// THE WALL'S FILTER REACHES EVERY SPAWN WHOLE, ON THE DESCRIPTOR ITS ARGV
// NAMES. bubblewrap reads its system-call filter from a descriptor to the
// end of the file, so one open file handed to every spawn would be read
// once and found empty by the restart, and bubblewrap refuses an empty
// filter: the child would start once and never again. Each spawn gets a
// fresh pipe holding the bytes. Its descriptor is the first after the
// spec's own, and the audio pair is numbered after both.
//
// The child here copies its descriptor 4 to its descriptor 3 (the spec's
// extra file, a pipe the test reads), adds the audio pair's numbers, and
// waits. The test kills it; the supervisor starts another, and the second
// child must say the same.
func TestTheContainmentFilterReachesEverySpawnOnTheNextDescriptor(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close() // every spawn is handed it again
	const filter = "sixteen bytes of"
	s, err := Start(Spec{
		PluginID:        "filtered",
		Argv:            []string{"/bin/sh", "-c", `cat <&4 >&3; echo " audio=$AII_AUDIO_IN_FD,$AII_AUDIO_OUT_FD" >&3; read line`},
		ExtraFiles:      []*os.File{w},
		ArgvContainment: Containment{Filter: []byte(filter)},
		AudioPair:       true,
		SessionMode:     true,
		Backoff:         Backoff{Initial: 10 * time.Millisecond, Max: 20 * time.Millisecond, MaxRestarts: 5},
	}, nil)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Close()
	lines := make(chan string, 4)
	go func() {
		in := bufio.NewReader(r)
		for {
			line, err := in.ReadString('\n')
			if err != nil {
				return
			}
			lines <- strings.TrimSpace(line)
		}
	}()
	for spawn := 1; spawn <= 2; spawn++ {
		select {
		case line := <-lines:
			if line != filter+" audio=5,6" {
				t.Fatalf("spawn %d: the child read %q on its descriptor 4, want the filter whole and the audio pair after it", spawn, line)
			}
		case <-time.After(15 * time.Second):
			t.Fatalf("spawn %d said nothing: the filter did not arrive, or its end did not", spawn)
		}
		if spawn == 1 {
			if killed, err := s.KillChild(); err != nil || !killed {
				t.Fatalf("no live child was killed: %v", err)
			}
		}
	}
}

// A filter larger than a pipe takes unread would block the spawn with
// nobody reading; it is refused before any process exists.
func TestAContainmentFilterThatWouldBlockTheSpawnIsRefused(t *testing.T) {
	_, err := Start(Spec{
		PluginID:        "oversized",
		Argv:            []string{"/bin/sh", "-c", "read line"},
		ArgvContainment: Containment{Filter: make([]byte, filterLimit+8)},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("an oversized filter must be a refused spawn, got %v", err)
	}
}
