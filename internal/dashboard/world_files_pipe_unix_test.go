//go:build unix

package dashboard

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A PIPE NAMED LIKE AN IMAGE IS NOT OPENED. Opening one waits for its
// writer, so a reply that names it would hold the route, and the page
// that asks with no click, until somebody wrote to it. It is asked what
// it is before it is opened, and answered as any other thing that is not
// a file.
func TestAPipeNamedLikeAnImageDoesNotHoldTheRoute(t *testing.T) {
	world := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(world, "pipe.png"), 0o600); err != nil {
		t.Skipf("no pipe can be made here: %v", err)
	}
	s := New("127.0.0.1", 0, &WSHandler{AdmitFile: func(p string) (string, string, bool) { return world, p, true }})
	done := make(chan int, 1)
	go func() {
		rr := httptest.NewRecorder()
		s.handleWorldFile(rr, httptest.NewRequest(http.MethodGet, "/w?p=pipe.png", nil))
		done <- rr.Code
	}()
	select {
	case code := <-done:
		if code != http.StatusNotFound {
			t.Fatalf("a pipe was answered %d, want the uniform 404", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the route waited on a pipe nobody writes to")
	}
}
