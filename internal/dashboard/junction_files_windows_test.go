//go:build windows

package dashboard

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAFileRouteDoesNotFollowAJunctionOutOfItsRoot(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, kept := base+`\project`, base+`\data`
	for f, body := range map[string]string{root + `\open.txt`: "OPEN\r\n", kept + `\secret.txt`: "KEPT SECRET\r\n"} {
		if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"), "/c", "mklink", "/J", root+`\j`, kept).CombinedOutput(); err != nil {
		t.Fatalf("a junction: %v: %s", err, out)
	}
	s := New("127.0.0.1", 0, &WSHandler{})
	types := map[string]string{".txt": "text/plain; charset=utf-8"}
	get := func(rel string) (int, string) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/p/x/"+rel, nil)
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		s.serveFileIn(w, r, root, rel, types, 1<<20, false)
		return w.Code, w.Body.String()
	}
	if code, body := get("open.txt"); code != http.StatusOK || !strings.Contains(body, "OPEN") {
		t.Fatalf("the root's own file is not served: %d %q", code, body)
	}
	for _, rel := range []string{"j/secret.txt", `j\secret.txt`} {
		if code, body := get(rel); code == http.StatusOK || strings.Contains(body, "KEPT SECRET") {
			t.Errorf("%s, through a junction out of the root, was served: %d %q", rel, code, body)
		}
	}
}
