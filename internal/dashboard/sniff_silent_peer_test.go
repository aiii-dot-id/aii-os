package dashboard

import (
	"io"
	"net"
	"testing"
	"time"
)

// A peer that connects to the TLS port and says nothing must not hold up
// the next connection. The first byte used to be read inside Accept, which
// the server calls from one goroutine: twenty silent peers were forty
// seconds in which no page could connect.
func TestASilentPeerDoesNotHoldUpTheNextConnection(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := newSniffListener(inner, func(net.Addr) string { return "https://unused.example.test" })
	t.Cleanup(func() { ln.Close() })
	dial := func() net.Conn {
		t.Helper()
		c, err := net.Dial("tcp", inner.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	const silent = 20
	for i := 0; i < silent; i++ {
		dial()
	}
	hello := []byte{0x16, 0x03, 0x01}
	talker := dial()
	if _, err := talker.Write(hello); err != nil {
		t.Fatal(err)
	}
	want := talker.LocalAddr().String()

	type accepted struct {
		c   net.Conn
		err error
	}
	got := make(chan accepted, 1)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil || c.RemoteAddr().String() == want {
				got <- accepted{c, err}
				return
			}
			c.Close() // a silent peer, handed on when its watch ended
		}
	}()
	// The budget is for a loaded machine, and is well under what the
	// silent peers took when they were read one after another.
	const budget = silent * sniffWait * 3 / 4
	select {
	case a := <-got:
		if a.err != nil {
			t.Fatalf("accept: %v", a.err)
		}
		defer a.c.Close()
		// The peer that spoke is the one handed on, with its first byte
		// replayed ahead of the rest.
		head := make([]byte, len(hello))
		_ = a.c.SetReadDeadline(time.Now().Add(budget))
		if _, err := io.ReadFull(a.c, head); err != nil || string(head) != string(hello) {
			t.Fatalf("the connection handed on read %x (%v), want the handshake's bytes %x from their first", head, err, hello)
		}
	case <-time.After(budget):
		t.Fatalf("A SILENT PEER HELD UP THE NEXT CONNECTION: %d peers that said nothing, and a handshake behind them was not accepted in %s", silent, budget)
	}

	// The server ends its accept loop by closing the listener.
	ln.Close()
	ended := make(chan error, 1)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				ended <- err
				return
			}
			c.Close() // a silent peer whose watch ended as the listener closed
		}
	}()
	select {
	case <-ended:
	case <-time.After(budget):
		t.Fatal("Accept did not end when the listener was closed: the server's shutdown would wait on it")
	}
}
