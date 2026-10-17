//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

// THE FETCHER SAYS WHOSE FAILURE A FAILURE IS (G1): a server's answer as its
// status, the network's as the network's, a refused address and a full disk
// as what they are — which is all the download rule needs to retry the one
// and end at the others.
func TestTheModelFetcherSaysWhoseFailureItIs(t *testing.T) {
	body := bytes.Repeat([]byte("x"), 4096)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/busy":
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/gone":
			w.WriteHeader(http.StatusNotFound)
		case "/expired":
			w.WriteHeader(http.StatusForbidden)
		case "/whole":
			_, _ = w.Write(body) // ignores any range
		case "/cut":
			// Promise the whole body, send part of it, and drop the line.
			w.Header().Set("Content-Length", fmt.Sprint(len(body)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(body[:100])
			w.(http.Flusher).Flush()
			if hj, ok := w.(http.Hijacker); ok {
				if c, _, err := hj.Hijack(); err == nil {
					_ = c.Close()
				}
			}
		default:
			_, _ = w.Write(body)
		}
	}))
	t.Cleanup(srv.Close)
	allow := func(context.Context, string) error { return nil }
	get := func(path string, offset int64, w io.Writer) (int64, error) {
		return guardedFetchWith(t.Context(), allow, srv.Client(), srv.URL+path, offset, w)
	}
	var status *FetchStatusError
	var network *NetworkError
	if _, err := get("/busy", 0, io.Discard); !errors.As(err, &status) || status.Status != 503 {
		t.Fatalf("a 503 is the server's answer: %v", err)
	}
	if _, err := get("/gone", 0, io.Discard); !errors.As(err, &status) || status.Status != 404 {
		t.Fatalf("a 404 is the server's answer: %v", err)
	}
	if _, err := get("/expired", 0, io.Discard); !errors.As(err, &status) || status.Status != 403 {
		t.Fatalf("a 403 is the server's answer: %v", err)
	}
	if _, err := get("/whole", 100, io.Discard); !errors.Is(err, ErrNoResume) {
		t.Fatalf("a server that ignores the range: %v", err)
	}
	var got bytes.Buffer
	if _, err := get("/cut", 0, &got); !errors.As(err, &network) || got.Len() != 100 {
		t.Fatalf("a body cut off is the network's (kept %d bytes): %v", got.Len(), err)
	}
	full := &refusingWriter{err: &net.OpError{Op: "write", Err: syscall.ENOSPC}}
	if _, err := get("/ok", 0, full); err == nil || errors.As(err, &network) {
		t.Fatalf("a write this machine refused passed for the network's: %v", err)
	}
	refused := func(context.Context, string) error { return fmt.Errorf("refusing: %w", firewall.ErrEgressBlocked) }
	if _, err := guardedFetchWith(t.Context(), refused, srv.Client(), srv.URL+"/ok", 0, io.Discard); errors.As(err, &network) || !errors.Is(err, firewall.ErrEgressBlocked) {
		t.Fatalf("the egress guard's refusal is its own: %v", err)
	}
	dead := httptest.NewServer(http.NotFoundHandler())
	addr := dead.URL
	dead.Close()
	if _, err := guardedFetchWith(t.Context(), allow, http.DefaultClient, addr+"/x", 0, io.Discard); !errors.As(err, &network) {
		t.Fatalf("a connection refused is the network's: %v", err)
	}
}

// refusingWriter is a writer that refuses, as a full disk does.
type refusingWriter struct{ err error }

func (f *refusingWriter) Write([]byte) (int, error) { return 0, f.err }
