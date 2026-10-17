package machine

import (
	"path/filepath"
	"testing"
)

// A SERVICE ASKED TO STOP STOPS CLEANLY: Serve answers nil, and the link
// and the lock are free for the next instance.
func TestAStoppedServiceAnswersNilAndFreesItsLink(t *testing.T) {
	root := shortDir(t)
	dir := filepath.Join(root, "m")
	r := serve(t, Options{Dir: dir})
	r.stop()
	if err := r.waitEnd(t, 5e9); err != nil {
		t.Fatalf("a stopped service must answer nil: %v", err)
	}
	h, err := Listen(dir)
	if err != nil {
		t.Fatalf("the next instance must take the link: %v", err)
	}
	h.Close()
}
