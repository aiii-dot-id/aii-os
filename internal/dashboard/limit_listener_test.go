package dashboard

import (
	"errors"
	"net"
	"testing"
	"time"
)

// The admission ceiling holds: at the cap, Accept parks; a closing
// connection frees a slot; a closing LISTENER unparks the parked
// Accept instead of stranding the serve goroutine (D48).
func TestLimitListenerCapAndShutdown(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := newLimitListener(raw, 2)
	defer ln.Close()

	accepted := make(chan net.Conn, 3)
	go func() {
		for {
			c, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			accepted <- c
		}
	}()

	dial := func() net.Conn {
		c, derr := net.DialTimeout("tcp", ln.Addr().String(), 2*time.Second)
		if derr != nil {
			t.Fatal(derr)
		}
		return c
	}
	d1, d2, d3 := dial(), dial(), dial()
	defer d1.Close()
	defer d2.Close()
	defer d3.Close()

	var got []net.Conn
	for i := 0; i < 2; i++ {
		select {
		case c := <-accepted:
			got = append(got, c)
		case <-time.After(3 * time.Second):
			t.Fatal("first two connections not accepted")
		}
	}
	// The third parks at the ceiling.
	select {
	case <-accepted:
		t.Fatal("third connection accepted past the cap")
	case <-time.After(200 * time.Millisecond):
	}
	// A slot frees; the third lands.
	got[0].Close()
	select {
	case c := <-accepted:
		defer c.Close()
	case <-time.After(3 * time.Second):
		t.Fatal("freed slot did not admit the parked connection")
	}
	got[1].Close()
}

// A parked Accept must return when the listener closes — otherwise
// shutdown at a full cap strands the serve goroutine forever.
func TestLimitListenerCloseUnparksAccept(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := newLimitListener(raw, 0) // zero slots: every Accept parks
	done := make(chan error, 1)
	go func() {
		_, aerr := ln.Accept()
		done <- aerr
	}()
	time.Sleep(50 * time.Millisecond)
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case aerr := <-done:
		if !errors.Is(aerr, net.ErrClosed) {
			t.Fatalf("want net.ErrClosed, got %v", aerr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Accept stayed parked across listener close")
	}
}
