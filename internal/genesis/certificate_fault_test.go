package genesis

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/tlsfault"
	"github.com/aiii-dot-id/aii-os/internal/tlsfault/tlsfaulttest"
)

// heard is what a client's refusal notice was told, in order.
type heard []Refusal

func (h *heard) notice(r Refusal) { *h = append(*h, r) }

func TestAFoundingServersRefusedCertificateIsReturnedAsThat(t *testing.T) {
	defer SetFetchBackoffForTest(time.Millisecond)()
	now := time.Now()
	srv, pool, handshakes := tlsfaulttest.Server(t, []string{"mail.example"}, now.Add(-90*24*time.Hour), now.Add(-3*time.Hour))
	var told heard
	c := NewClient(srv.URL, srv.URL, srv.URL).WithPatience(2, 0).WithRefusalNotice(told.notice)
	c.httpClient.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}
	_, err := c.FetchRing5()
	var fault *tlsfault.Fault
	if !errors.As(err, &fault) || fault.Reason != tlsfault.Expired || fault.Host != "127.0.0.1" || fault.NamesHost {
		t.Fatalf("the Ring 5 fetch from a server whose certificate has expired: %v (%+v)", err, fault)
	}
	// Verified again against the client's own authorities, which trust it:
	// it is not said to be from an authority this machine does not trust.
	if fault.AlsoUntrusted {
		t.Fatalf("a certificate the client's authorities trust is said to be untrusted: %v", err)
	}
	if !strings.Contains(err.Error(), "presented a certificate that expired on") || strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("the words: %v", err)
	}
	if n := handshakes.Load(); n != 2 {
		t.Fatalf("the server was asked %d times; told two attempts", n)
	}
	// Refused at every attempt, it is said once: no "F; before it, F".
	if n := strings.Count(err.Error(), "presented a certificate that"); n != 1 {
		t.Fatalf("the refusal is said %d times: %v", n, err)
	}
	if len(told) != 2 || told[0].Fault == nil || told[0].Attempt != 1 || told[0].Attempts != 2 || told[1].Fault == nil || told[1].Attempt != 2 || !strings.HasSuffix(told[0].URL, "/v1/ring5/pubkey.bundle") {
		t.Fatalf("each refusal is told as it is met: %+v", told)
	}
}

// A HOST PART WAY THROUGH A RENEWAL ANSWERS THE NEXT ATTEMPT. The first
// handshake is shown the expired certificate and the second the renewed
// one: the artifact is had, and the notice that told the refusal is told
// that it no longer holds.
func TestAFoundingServerPartWayThroughARenewalIsHadOnTheNextAttempt(t *testing.T) {
	defer SetFetchBackoffForTest(time.Millisecond)()
	now := time.Now()
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	srv, pool, handshakes := tlsfaulttest.Serve(t, ok,
		tlsfaulttest.Certificate(t, []string{"127.0.0.1"}, now.Add(-90*24*time.Hour), now.Add(-3*time.Hour)),
		tlsfaulttest.Certificate(t, []string{"127.0.0.1"}, now.Add(-time.Hour), now.Add(90*24*time.Hour)))
	var told heard
	c := NewClient(srv.URL, srv.URL, srv.URL).WithPatience(3, 0).WithRefusalNotice(told.notice)
	c.httpClient.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}
	resp, err := c.getWithRetry(req(t, srv.URL))
	if err != nil {
		t.Fatalf("the renewed certificate was not taken: %v", err)
	}
	resp.Body.Close()
	if n := handshakes.Load(); n != 2 {
		t.Errorf("the server was asked %d times; want the refused one and the renewed one", n)
	}
	if len(told) != 2 || told[0].Fault == nil || told[0].Fault.Reason != tlsfault.Expired || told[0].Attempt != 1 || told[1].Fault != nil || told[1].Attempt != 2 {
		t.Fatalf("the refusal and the answer after it, as they happened: %+v", told)
	}
}

// A REFUSED CERTIFICATE THAT NO LATER ANSWER UNDID STAYS THE CAUSE SAID.
// The first attempt is refused for its certificate and the next is not
// answered at all: the error says both, and is read as the fault.
func TestARefusedCertificateIsNotForgottenForALaterFailure(t *testing.T) {
	defer SetFetchBackoffForTest(time.Millisecond)()
	now := time.Now()
	srv, pool, _ := tlsfaulttest.Server(t, []string{"mail.example"}, now.Add(-90*24*time.Hour), now.Add(-3*time.Hour))
	c := NewClient(srv.URL, srv.URL, srv.URL).WithPatience(2, 0)
	c.httpClient.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}
	first := true
	c.WithRefusalNotice(func(Refusal) {
		if first {
			first = false
			srv.Close() // the next attempt finds nothing listening
		}
	})
	_, err := c.getWithRetry(req(t, srv.URL))
	var fault *tlsfault.Fault
	if !errors.As(err, &fault) || fault.Reason != tlsfault.Expired || !strings.Contains(err.Error(), "; before it, the server 127.0.0.1 presented a certificate that expired on") {
		t.Fatalf("a refusal and then no answer: %v", err)
	}
}

// AN ANSWER AFTER A REFUSAL UNDOES IT, even when the answer is no artifact.
// The first attempt is refused for its certificate, the second is answered
// under the renewed one with 503, and the third finds nothing listening:
// the error is the last attempt's, and no refused certificate rides it.
func TestARefusalThatALaterAnswerUndidIsNotTheCauseSaid(t *testing.T) {
	defer SetFetchBackoffForTest(time.Millisecond)()
	now := time.Now()
	unwell := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
	srv, pool, _ := tlsfaulttest.Serve(t, unwell,
		tlsfaulttest.Certificate(t, []string{"127.0.0.1"}, now.Add(-90*24*time.Hour), now.Add(-3*time.Hour)),
		tlsfaulttest.Certificate(t, []string{"127.0.0.1"}, now.Add(-time.Hour), now.Add(90*24*time.Hour)))
	var dials atomic.Int32
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		if dials.Add(1) == 3 {
			return nil, &net.OpError{Op: "dial", Net: network, Err: errors.New("connection refused")}
		}
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	var told heard
	c := NewClient(srv.URL, srv.URL, srv.URL).WithPatience(3, 0).WithRefusalNotice(told.notice)
	c.httpClient.Transport = &http.Transport{DialContext: dial, TLSClientConfig: &tls.Config{RootCAs: pool}}
	_, err := c.getWithRetry(req(t, srv.URL))
	var fault *tlsfault.Fault
	if err == nil || errors.As(err, &fault) || strings.Contains(err.Error(), "presented a certificate") {
		t.Fatalf("a refusal undone by an answer is still the cause said: %v", err)
	}
	if len(told) != 2 || told[0].Fault == nil || told[1].Fault != nil {
		t.Fatalf("the refusal and the answer after it: %+v", told)
	}
}

// A STOP DOES NOT WAIT OUT A FOUNDING SERVER'S ATTEMPTS. The client's
// lifetime ends a pause between attempts at once, and a request under way.
func TestAFoundingFetchEndsWhenItsLifetimeDoes(t *testing.T) {
	var asked atomic.Int32
	unwell := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer unwell.Close()
	ctx, cancel := context.WithCancel(context.Background())
	c := NewClient(unwell.URL, unwell.URL, unwell.URL).WithPatience(5, 0).WithLifetime(ctx)
	time.AfterFunc(200*time.Millisecond, cancel) // inside the 2 s pause before the second attempt
	began := time.Now()
	_, _, err := c.fetchBundle(unwell.URL, "ring5")
	if took := time.Since(began); took > 1500*time.Millisecond || !errors.Is(err, context.Canceled) || asked.Load() != 1 {
		t.Fatalf("a fetch whose lifetime ended went on: %s, asked %d times: %v", took, asked.Load(), err)
	}

	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	defer close(release)
	ctx, cancel = context.WithCancel(context.Background())
	c = NewClient(slow.URL, slow.URL, slow.URL).WithPatience(1, time.Minute).WithLifetime(ctx)
	time.AfterFunc(200*time.Millisecond, cancel)
	began = time.Now()
	if _, _, err := c.fetchBundle(slow.URL, "ring5"); !errors.Is(err, context.Canceled) || time.Since(began) > 5*time.Second {
		t.Fatalf("a request under way outlived its lifetime by %s: %v", time.Since(began), err)
	}
}

// A SERVER'S REFUSAL IS SAID IN ITS OWN WORDS, BOUNDED. The body of a
// refusal reaches the SAFE line, the identity's posture and the log: a
// page with line breaks is said printable and cut, and a plain one as it
// is.
func TestAFoundingServersRefusalBodyIsBounded(t *testing.T) {
	page := "<html>\nforged log line\r\n\u202e" + strings.Repeat("x", 5000)
	for _, tc := range []struct{ body, want string }{
		{page, "<html> forged log line   "},
		{`{"error":"Genesis token not valid for this client"}`, `{"error":"Genesis token not valid for this client"}`},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(tc.body))
		}))
		_, _, err := NewClient(srv.URL, srv.URL, srv.URL).fetchBundle(srv.URL, "ring5")
		srv.Close()
		var refused *StatusError
		if !errors.As(err, &refused) || !strings.HasPrefix(refused.Body, tc.want) || strings.ContainsAny(err.Error(), "\n\r\u202e") || len(refused.Body) > tlsfault.MaxSaid+8 {
			t.Fatalf("a refusal's body: %q", err)
		}
	}
}
