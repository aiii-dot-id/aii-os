package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A SESSION IS NOT ITS SOCKET. Every function that means "this session"
// takes a carrier, so that a session's messages can come to be carried
// another way without each of them learning of it. This refuses the
// socket's own type anywhere in the package outside the file that says the
// socket is a carrier: a function written against the socket would work
// today and silently leave the next carrier's sessions out.
func TestTheSessionCodeNamesTheCarrierNotTheSocket(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") || name == "carrier.go" {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		for i, line := range strings.Split(string(src), "\n") {
			if strings.Contains(line, "websocket.Conn") && !strings.HasPrefix(strings.TrimSpace(line), "//") {
				t.Errorf("%s:%d names the socket's own type; a session is a carrier: %s", name, i+1, strings.TrimSpace(line))
			}
		}
	}
	if checked < 20 {
		t.Fatalf("read %d source files of the package: the walk is not reading it", checked)
	}
}
