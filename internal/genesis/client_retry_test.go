package genesis

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// .
// .
func fastBackoff(t *testing.T) {
	t.Helper()
	saved := fetchBackoff
	fetchBackoff = time.Millisecond
	t.Cleanup(func() { fetchBackoff = saved })
}

// .
// .
// .
// .
// .
// .

func req(t *testing.T, url string) *http.Request {
	t.Helper()
	r, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestGetWithRetryRetriesWhatIsWorthRetrying(t *testing.T) {
	for _, tc := range []struct {
		name        string
		failFirst   int
		status      int
		wantCalls   int32
		wantSuccess bool
	}{
		// .
		{"a 500 that clears", 2, http.StatusInternalServerError, 3, true},
		{"a 503 that clears", 1, http.StatusServiceUnavailable, 2, true},
		// .
		// .
		{"a 404 is not retried", 99, http.StatusNotFound, 1, true},
		{"a 401 is not retried", 99, http.StatusUnauthorized, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fastBackoff(t)
			var calls int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := atomic.AddInt32(&calls, 1)
				if int(n) <= tc.failFirst {
					w.WriteHeader(tc.status)
					return
				}
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, "ok")
			}))
			defer srv.Close()

			c := NewClient(srv.URL, "", "")
			resp, err := c.getWithRetry(req(t, srv.URL))
			if tc.wantSuccess {
				if err != nil {
					t.Fatalf("expected a response, got %v", err)
				}
				resp.Body.Close()
			} else if err == nil {
				resp.Body.Close()
				t.Fatal("expected a refusal")
			}
			if got := atomic.LoadInt32(&calls); got != tc.wantCalls {
				t.Fatalf("server saw %d requests, want %d — %s", got, tc.wantCalls, tc.name)
			}
		})
	}
}

// .
// .
func TestGetWithRetryGivesUpAndNamesTheCause(t *testing.T) {
	fastBackoff(t)
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "", "")
	resp, err := c.getWithRetry(req(t, srv.URL))
	if err == nil {
		resp.Body.Close()
		t.Fatal("a server that never recovered was treated as a success")
	}
	if got := atomic.LoadInt32(&calls); got < 2 {
		t.Fatalf("gave up after %d attempts — a transient failure would not be survived", got)
	}
	if err.Error() == "" {
		t.Fatal("the failure carries no cause for the operator to act on")
	}
}

// .
// .
func TestFetchBootstrapRefusesWithoutAServer(t *testing.T) {
	c := NewClient("https://genesis.example", "", "")
	if _, err := c.FetchBootstrap(); err == nil {
		t.Fatal("FetchBootstrap proceeded with no bootstrap server configured")
	}
}
