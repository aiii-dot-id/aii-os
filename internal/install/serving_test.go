package install

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"
)

// answering serves the serving challenge on a port of the test's own with
// key, as a dashboard does (internal/dashboard), and answers its port.
func answering(t *testing.T, key []byte) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := hex.DecodeString(r.URL.Query().Get("c"))
		if r.URL.Path != ServingRoute || err != nil {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintln(w, hex.EncodeToString(ServingProof(key, c)))
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().(*net.TCPAddr).Port
}

// A START IS PROVED ONLY BY THE SLOT'S OWN ANSWER. A dashboard holding the
// key the slot wrote proves it; one holding another key — another slot's,
// another account's, or this slot's from before its last start — answers
// and does not; a port nothing answers on is not serving, and the bound is
// said.
func TestAStartIsProvedOnlyByTheSlotsOwnAnswer(t *testing.T) {
	dir := t.TempDir()
	key, err := WriteServingKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := WaitServing(answering(t, key), dir, time.Second); err != nil {
		t.Fatalf("the slot's own dashboard: %v", err)
	}
	other, err := WriteServingKey(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var ns *NotServingError
	if err := WaitServing(answering(t, other), dir, 300*time.Millisecond); !errors.As(err, &ns) || !ns.Other {
		t.Fatalf("a dashboard with another key: %v", err)
	}
	stale := answering(t, key)
	if _, err := WriteServingKey(dir); err != nil { // the slot started again
		t.Fatal(err)
	}
	if err := WaitServing(stale, dir, 300*time.Millisecond); !errors.As(err, &ns) || !ns.Other {
		t.Fatalf("a dashboard with the slot's key from before its last start: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	quiet := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	if err := WaitServing(quiet, dir, 300*time.Millisecond); !errors.As(err, &ns) || ns.Other || ns.Error() != fmt.Sprintf("the identity did not start serving on port %d within 0.3s", quiet) {
		t.Fatalf("a port nothing answers on: %v", err)
	}
}
