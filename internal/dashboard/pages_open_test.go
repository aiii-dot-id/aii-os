package dashboard

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// THE ROUTE OFF THE DASHBOARD ASKS THIS SERVER WHETHER A PAGE IS OPEN
// (S-3), and is told when the last one goes — however it goes: its own
// close, or a write it could not take.

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Several pages: one closing is not the last; the last closing says so,
// once, and no page is open after it.
func TestPageOpenAndTheLastPageClosing(t *testing.T) {
	var closed atomic.Int32
	s := New("127.0.0.1", 0, &WSHandler{PagesClosed: func() { closed.Add(1) }})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	if s.PageOpen() {
		t.Fatal("a page is open before any connected")
	}
	one, two := dialWS(t, addr), dialWS(t, addr)
	waitForConns(t, s, 2)
	if !s.PageOpen() {
		t.Fatal("two pages are open and the server says none is")
	}
	one.Close(websocket.StatusNormalClosure, "")
	waitForConnsAtMost(t, s, 1)
	if !s.PageOpen() || closed.Load() != 0 {
		t.Fatalf("one of two pages closing: open %v, last-page calls %d", s.PageOpen(), closed.Load())
	}
	two.Close(websocket.StatusNormalClosure, "")
	waitFor(t, "the last page's closing was never said", func() bool { return closed.Load() == 1 })
	if s.PageOpen() {
		t.Fatal("no page is open, and the server says one is")
	}
	three := dialWS(t, addr)
	waitForConns(t, s, 1)
	three.Close(websocket.StatusNormalClosure, "")
	waitFor(t, "a page reopened and closed is a last page again", func() bool { return closed.Load() == 2 })
}

// A page that cannot take a write is dropped, and when it was the last,
// the walk is told: the notice that write carried was never marked, and
// is the route's to take now.
func TestAFailedWriteDroppingTheLastPageSaysSo(t *testing.T) {
	big := strings.Repeat("x", 1<<20)
	var seq atomic.Int64
	var closed, marked atomic.Int32
	s := New("127.0.0.1", 0, &WSHandler{
		TakeOutbox: func() ([]OutboxItem, func(bool), error) {
			return []OutboxItem{{ID: fmt.Sprintf("n%d", seq.Add(1)), To: "operator", Content: big}}, func(shown bool) {
				if shown {
					marked.Add(1)
				}
			}, nil
		},
		PagesClosed: func() { closed.Add(1) },
	})
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	stalled := dialWS(t, addr) // never reads after the handshake
	_ = stalled
	waitForConns(t, s, 1)
	deadline := time.Now().Add(30 * time.Second)
	for s.PageOpen() {
		if time.Now().After(deadline) {
			t.Fatal("the stalled page was never dropped")
		}
		s.PokeOutbox()
		time.Sleep(300 * time.Millisecond)
	}
	waitFor(t, "the page a failed write dropped was the last, and nothing said so", func() bool { return closed.Load() == 1 })
	passes := seq.Load()
	if int64(marked.Load()) >= passes {
		t.Fatalf("every pass was marked (%d of %d): the write that failed marked its notice", marked.Load(), passes)
	}
	time.Sleep(100 * time.Millisecond)
	if closed.Load() != 1 {
		t.Fatalf("the last page's going was said %d times", closed.Load())
	}
}
