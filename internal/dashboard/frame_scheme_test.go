package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The frame must DERIVE the scheme it dials from the page it was loaded
// by, never name one. A literal here is a second copy of the server's
// scheme and is free to drift from it — which is exactly what happened.
// This failure has no error message:
// the page loads, the socket never opens, and the reconnect retries
// forever from every open tab while the operator watches a UI that will
// not connect.
//
// The Go side of this was caught by tests that speak the real transport.
// The BROWSER side was not, because no test reads the shipped frame —
// so the operator found it, in the logs, as a flood. This is the test
// that would have.
func TestTheShippedFrameDerivesItsSchemeRatherThanNamingOne(t *testing.T) {
	roots := []string{
		filepath.Join("static"),
		filepath.Join("static", "views"),
	}
	checked, sawDerivation := 0, false
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".js") && !strings.HasSuffix(e.Name(), ".html") {
				continue
			}
			path := filepath.Join(root, e.Name())
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			checked++
			body := string(raw)

			// The original bug, and its mirror image. A socket handed a
			// literal scheme is wrong whichever scheme it names: ws://
			// failed the day TLS landed, and wss:// would fail the day
			// anything else is served. Neither announces itself.
			for _, bad := range []string{"new WebSocket('", `new WebSocket("`} {
				if strings.Contains(body, bad) {
					t.Errorf("%s dials a socket from a literal scheme (%s) — derive it from location.protocol, which cannot disagree with the page", path, bad)
				}
			}

			// A plaintext scheme may appear only as one branch of a
			// derivation. Standing alone it is a plaintext fetch on an
			// https page: blocked, silently.
			for _, plain := range []string{"'ws://", `"ws://`, "'http://", `"http://`} {
				if strings.Contains(body, plain) && !strings.Contains(body, "location.protocol") {
					t.Errorf("%s names a plaintext scheme (%s) without deriving from location.protocol", path, plain)
				}
			}
			if strings.Contains(body, "location.protocol") {
				sawDerivation = true
			}
		}
	}
	if checked == 0 {
		t.Fatal("no frame files were read; this test proves nothing")
	}
	// Without this the whole test passes on a frame that opens no socket
	// at all, which is precisely the state the bug produced.
	if !sawDerivation {
		t.Error("no frame file derives its scheme from location.protocol — the socket is either gone or hardcoded again")
	}
	t.Logf("checked %d frame files", checked)
}
