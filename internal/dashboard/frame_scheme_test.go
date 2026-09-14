package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
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

			// .
			// .
			// .
			// .
			for _, bad := range []string{"new WebSocket('", `new WebSocket("`} {
				if strings.Contains(body, bad) {
					t.Errorf("%s dials a socket from a literal scheme (%s) — derive it from location.protocol, which cannot disagree with the page", path, bad)
				}
			}

			// .
			// .
			// .
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
	// .
	// .
	if !sawDerivation {
		t.Error("no frame file derives its scheme from location.protocol — the socket is either gone or hardcoded again")
	}
	t.Logf("checked %d frame files", checked)
}
