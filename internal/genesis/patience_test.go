package genesis

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// HOW HARD THE CLIENT TRIES IS ITS CALLER'S TO SAY (WithPatience). Named
// nothing, it asks three times, as it always did; told a number, it asks
// that many; and one request is held to the time it is given.
func TestAFoundingArtifactIsAskedForAsOftenAndAsLongAsItsCallerSays(t *testing.T) {
	restore := SetFetchBackoffForTest(time.Millisecond)
	defer restore()
	var asked atomic.Int32
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked.Add(1)
		http.Error(w, "not now", http.StatusServiceUnavailable)
	}))
	defer failing.Close()
	for _, tc := range []struct{ told, want int32 }{{0, DefaultFetchAttempts}, {1, 1}, {5, 5}} {
		asked.Store(0)
		c := NewClient(failing.URL, failing.URL, failing.URL).WithPatience(int(tc.told), 0)
		if _, _, err := c.fetchBundle(failing.URL, "ring5"); err == nil {
			t.Fatal("a server that answers 503 every time gave an artifact")
		}
		if got := asked.Load(); got != tc.want {
			t.Errorf("told %d attempts, the server was asked %d times; want %d", tc.told, got, tc.want)
		}
	}

	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer slow.Close()
	defer close(release)
	c := NewClient(slow.URL, slow.URL, slow.URL).WithPatience(1, 100*time.Millisecond)
	began := time.Now()
	if _, _, err := c.fetchBundle(slow.URL, "ring5"); err == nil {
		t.Fatal("a server that never answers gave an artifact")
	}
	// The bound is the caller's 100 ms and not the default's thirty seconds;
	// the allowance is for a busy host, and is far from either.
	if took := time.Since(began); took > 10*time.Second {
		t.Errorf("one request told 100 ms took %s", took)
	}
}
