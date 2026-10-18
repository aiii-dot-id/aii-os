//go:build windows

package pluginhost

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

// A MODEL IS READABLE BY ITS VIEW THE MOMENT IT IS LINKED. A view's first
// model was granted only when the whole view was done, after its second
// had arrived, so a child already running from the view could see the
// first and be refused it for as long as the second took (the 0.1.16
// native suite on the guest: "late=denied"). Here the second model's
// download is held while the first, linked, is asked whose capability it
// carries.
func TestAModelLinkedIntoAViewIsGrantedBeforeItAppears(t *testing.T) {
	w := openStore(t)
	one, two := bytes.Repeat([]byte("1"), 2048), bytes.Repeat([]byte("2"), 4096)
	d1, d2 := declOf("one", "one.bin", one), declOf("two", "two.bin", two)
	held := make(chan struct{})
	fetch := func(ctx context.Context, url string, offset int64, out io.Writer) (int64, error) {
		b := one
		if url == d2.URL {
			select {
			case <-held:
			case <-ctx.Done():
				return 0, ctx.Err()
			}
			b = two
		}
		n, err := out.Write(b[offset:])
		return int64(n), err
	}
	work := viewWork([]string{"one", "two"}, d1, d2)
	done := make(chan error, 1)
	go func() { done <- EnsureEntry(context.Background(), w, work, fetch, Adoption{}, nil) }()
	key := work.Entry[len("v/"):]
	view, err := w.View(key)
	if err != nil {
		t.Fatal(err)
	}
	first := modelFile(view, d1)
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Lstat(first); err == nil {
			break
		}
		if time.Now().After(deadline) {
			close(held)
			t.Fatal("rig: the first model was never linked")
		}
		time.Sleep(5 * time.Millisecond)
	}
	sid, err := plugstore.CapabilitySID(plugstore.Entry{Kind: plugstore.DirViews, ID: key})
	if err != nil {
		t.Fatal(err)
	}
	caps, err := plugstore.Capabilities(first)
	if err != nil {
		t.Fatal(err)
	}
	granted := false
	for _, c := range caps {
		granted = granted || windows.EqualSid(c, sid)
	}
	close(held)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !granted {
		t.Fatal("the first model was linked into the view before the view's capability could read it, while the second was still arriving")
	}
}
