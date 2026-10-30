package dashboard

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/install"
)

func TestTheDashboardProvesItIsThisIdentitysToThisMachineOnly(t *testing.T) {
	dir := t.TempDir()
	s := New("127.0.0.1", 0, &WSHandler{})
	bound, err := s.Start("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(t.Context()) })
	_, portText, _ := net.SplitHostPort(bound)
	port, _ := strconv.Atoi(portText)
	if _, err := install.WriteServingKey(dir); err != nil {
		t.Fatal(err)
	}
	if err := install.WaitServing(port, dir, 300*time.Millisecond); err == nil {
		t.Fatal("a dashboard with no key was taken for the slot's")
	}
	key, err := install.WriteServingKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.SetServingKey(key)
	if err := install.WaitServing(port, dir, 2*time.Second); err != nil {
		t.Fatalf("the slot's own dashboard: %v", err)
	}
	if _, err := install.WriteServingKey(dir); err != nil { // another start of the slot
		t.Fatal(err)
	}
	if err := install.WaitServing(port, dir, 300*time.Millisecond); err == nil {
		t.Fatal("a dashboard holding a key the slot no longer has was taken for the slot's")
	}
	for _, c := range []struct {
		remote, challenge string
		want              int
	}{
		{"192.0.2.7:4242", "00112233445566778899aabbccddeeff", http.StatusForbidden},
		{"127.0.0.1:4242", "not-hex", http.StatusBadRequest},
		{"127.0.0.1:4242", "0011", http.StatusBadRequest},
		{"127.0.0.1:4242", "00112233445566778899aabbccddeeff", http.StatusOK},
	} {
		req := httptest.NewRequest(http.MethodGet, "http://"+bound+install.ServingRoute+"?c="+c.challenge, nil)
		req.RemoteAddr = c.remote
		w := httptest.NewRecorder()
		s.server.Handler.ServeHTTP(w, req)
		if w.Code != c.want || w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s asking %q: %d %q, cache %q; want %d, no-store", c.remote, c.challenge, w.Code, w.Body.String(), w.Header().Get("Cache-Control"), c.want)
		}
	}
}
