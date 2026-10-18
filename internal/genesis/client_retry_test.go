package genesis

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/genesis/genesislive"
)

// The shipped backoff is two seconds per attempt; proving the policy
// does not require waiting it out.
func fastBackoff(t *testing.T) {
	t.Helper()
	saved := fetchBackoff
	fetchBackoff = time.Millisecond
	t.Cleanup(func() { fetchBackoff = saved })
}

// getWithRetry IS THE ONLY THING BETWEEN A BIRTH AND A FLAKY NETWORK.
// It was at 50%: the happy path was exercised and the retry was not, so
// nothing proved that a 500 is retried, that a transport error is
// retried, or — the one that matters in the other direction — that a
// 4xx is NOT retried. Retrying a refusal turns "this identity may not
// be born" into three of the same answer and a longer wait.

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
		// A server that is briefly unwell is the case retry exists for.
		{"a 500 that clears", 2, http.StatusInternalServerError, 3, true},
		{"a 503 that clears", 1, http.StatusServiceUnavailable, 2, true},
		// A REFUSAL IS AN ANSWER. Retrying it wastes the operator's time
		// and tells them nothing new.
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

// A SERVER THAT NEVER RECOVERS MUST STOP, and must say what it last saw
// rather than returning a bare nil.
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

// FetchBootstrap REFUSES BEFORE IT REACHES THE NETWORK when nothing is
// configured. Birth must not hang on a server nobody named.
func TestFetchBootstrapRefusesWithoutAServer(t *testing.T) {
	c := NewClient("https://genesis.example", "", "")
	if _, err := c.FetchBootstrap(); err == nil {
		t.Fatal("FetchBootstrap proceeded with no bootstrap server configured")
	}
}

// A REFUSAL IS READ BY ITS TYPE: which artifact was asked for and the
// status it was answered with. The founding fetch stages Ring 0 and the
// packet again when the bootstrap server refuses the token (402), and it
// must tell that apart from a missing key chain or a missing packet
// without reading the server's sentence. The text every caller prints is
// what it was.
func TestARefusedFetchSaysWhichArtifactAndWhichStatus(t *testing.T) {
	live, err := genesislive.Fetch()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, artifact, text string
		keyStatus, status    int
	}{
		{"the packet, the token refused", "bootstrap", `bootstrap server error: bundle fetch returned 402: {"error":"Genesis token not valid for this client"}`, 200, http.StatusPaymentRequired},
		{"the key chain, not found", "bootstrap.pubkey", `bootstrap.pubkey domain-key chain unavailable: bundle fetch returned 404: {"error":"Genesis token not valid for this client"}`, http.StatusNotFound, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/bootstrap/pubkey.bundle" && tc.keyStatus == 200 {
					_, _ = w.Write(live.BootstrapKey)
					return
				}
				w.WriteHeader(max(tc.status, tc.keyStatus))
				fmt.Fprint(w, `{"error":"Genesis token not valid for this client"}`)
			}))
			defer srv.Close()
			_, err := NewClient("", "", srv.URL).FetchBootstrap()
			var refused *StatusError
			if !errors.As(err, &refused) {
				t.Fatalf("the refusal carries no type to read it by: %v", err)
			}
			if want := max(tc.status, tc.keyStatus); refused.Artifact != tc.artifact || refused.StatusCode != want {
				t.Fatalf("refused %q with %d, want %q with %d", refused.Artifact, refused.StatusCode, tc.artifact, want)
			}
			if err.Error() != tc.text {
				t.Fatalf("the refusal's text changed: %q", err)
			}
		})
	}
}

// A RATE LIMIT SAYS WHEN TO COME BACK, AND THE REFUSAL CARRIES IT. The
// genesis service limits Ring 0 downloads per address per hour and answers
// 429 with Retry-After in seconds; the first boot reads the wait from the
// typed field, and a header that is absent, is not seconds, or names more
// than a day reads as none: a number no server means is not printed as its
// word.
func TestARateLimitedFetchCarriesTheServersWait(t *testing.T) {
	for header, want := range map[string]time.Duration{
		"1800": 30 * time.Minute, "86400": 24 * time.Hour, "": 0, "-5": 0, "Wed, 21 Oct 2026 07:28:00 GMT": 0,
		"86401": 0, "99999999999": 0, "9223372036854775807": 0,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if header != "" {
				w.Header().Set("Retry-After", header)
			}
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		_, _, err := NewClient(srv.URL, "", "").fetchBundle(srv.URL, "ring0")
		srv.Close()
		var refused *StatusError
		if !errors.As(err, &refused) || refused.StatusCode != http.StatusTooManyRequests || refused.RetryAfter != want {
			t.Errorf("Retry-After %q: %+v (%v), want a typed 429 with a wait of %s", header, refused, err, want)
		}
	}
}

// A CLIENT PINNED TO A FAMILY THIS MACHINE CANNOT USE DOES NOT WAIT ON IT.
// The first boot stages the founding pair again over IPv4 alone and then
// IPv6 alone; on a machine with one family, the other's dial fails at once
// (no address of that family, or no route), and sitting through the
// backoff for a network that is not coming would be six seconds of nothing.
// Only that class, and only when pinned: a refused connection is still
// tried three times, and a client that is not pinned retries as it did.
func TestAPinnedClientDoesNotRetryAFamilyItCannotUse(t *testing.T) {
	fastBackoff(t)
	closed, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	refusing := "http://" + closed.Addr().String()
	closed.Close()
	noAddress := &net.OpError{Op: "dial", Net: "tcp", Err: &net.AddrError{Err: "no suitable address found", Addr: "host"}}
	for _, tc := range []struct {
		name, family, url string
		dialErr           error
		dials             int
	}{
		{"pinned to IPv6, the host has an IPv4 address alone", "tcp6", refusing, nil, 1},
		{"pinned to IPv4, the connection is refused", "tcp4", refusing, nil, DefaultFetchAttempts},
		{"not pinned, the same failure", "", refusing, noAddress, DefaultFetchAttempts},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dials := 0
			c := NewClient("", "", "")
			c.SetTransportForTest(&http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				dials++
				if tc.dialErr != nil {
					return nil, tc.dialErr
				}
				return (&net.Dialer{}).DialContext(ctx, network, addr)
			}})
			if tc.family != "" {
				c = c.OverFamily(tc.family)
			}
			if _, _, err := c.fetchBundle(tc.url, "ring0"); err == nil {
				t.Fatal("a server nothing listens on answered")
			}
			if dials != tc.dials {
				t.Fatalf("dialed %d time(s), want %d", dials, tc.dials)
			}
		})
	}
}

// The class is read from the net errors' types: the network unreachable,
// no address of the family, a name that is not there. A refused or timed
// out connection is a host that did not answer, not a family this machine
// lacks.
func TestFamilyUnavailableIsReadFromTheErrorsType(t *testing.T) {
	dial := func(err error) error {
		return &url.Error{Op: "Get", URL: "http://host", Err: &net.OpError{Op: "dial", Net: "tcp6", Err: err}}
	}
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{dial(os.NewSyscallError("connect", netUnreachable)), true},
		{dial(&net.AddrError{Err: "no suitable address found", Addr: "host"}), true},
		{dial(&net.DNSError{Err: "no such host", Name: "host", IsNotFound: true}), true},
		{dial(&net.DNSError{Err: "i/o timeout", Name: "host", IsTimeout: true}), false},
		{dial(os.NewSyscallError("connect", syscall.ECONNREFUSED)), false},
		{dial(errors.New("network is unreachable")), false},
	} {
		if got := familyUnavailable(tc.err); got != tc.want {
			t.Errorf("%v: familyUnavailable = %t, want %t", tc.err, got, tc.want)
		}
	}
}
