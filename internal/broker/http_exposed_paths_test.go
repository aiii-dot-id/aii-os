package broker

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// THE "LOST" HALF OF AN UNKNOWN EFFECT (T37 follow-up). An idempotent
// call is sent again once when an attempt fails. When the first attempt
// was written and answered nothing, and the second never got as far as
// writing — its connection refused — the LAST attempt wrote nothing, yet
// the remote effect is still unknown: the first may have been performed.
// The receipt must say unknown, never not-performed.
func TestAnEarlierAttemptWrittenAndLostLeavesTheEffectUnknown(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	var requests atomic.Int32
	served := make(chan struct{})
	go func() {
		defer close(served)
		c, err := l.Accept()
		if err != nil {
			return
		}
		if req, err := http.ReadRequest(bufio.NewReader(c)); err == nil {
			_, _ = io.Copy(io.Discard, req.Body)
			requests.Add(1)
		}
		// The listener goes first, so the resend finds nothing to
		// connect to; then the written request's connection, unanswered.
		l.Close()
		c.Close()
	}()
	hostPort := l.Addr().String()
	target := "http://" + hostPort + "/charge"
	guard := func(_ context.Context, rawURL string) error {
		if strings.HasPrefix(rawURL, target) {
			return nil
		}
		return fmt.Errorf("not this test's target: %s", rawURL)
	}
	h := newHost(t, newStore(t), Config{Grants: map[string]Grant{"p": {Hosts: []string{hostPort}}}, Guard: guard,
		Transport: http.DefaultTransport.(*http.Transport).Clone()})
	b := h.Bind("p", packagefmt.TierT1, []string{"net.outbound:" + hostPort})

	rec := wantResult(t, dispatch(t, b, verbParams("http.post", target, `{"body":"x","content_type":"text/plain","idempotent":true}`)), statusFailed, reasonNetEffectUnknown)
	if receiptField(t, rec, "effect") != EffectUnknown {
		t.Fatalf("an earlier attempt written and lost leaves the effect unknown: effect %s", rec["effect"])
	}
	<-served
	if requests.Load() != 1 {
		t.Fatalf("exactly one attempt reached the server: %d", requests.Load())
	}
}

// A REDIRECT ANSWER TO A STREAMED CALL IS STREAMED. Only an error status
// (400 and above) is read whole; a mutation's redirect is an answer, not
// an instruction (it is not followed), so a streamed one leaves its body
// on the connection like any other answer, its status and Location on
// the head.
func TestAStreamedRedirectAnswerIsAStream(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/receipt/7")
		w.WriteHeader(http.StatusSeeOther)
		fmt.Fprint(w, "see the receipt")
	}))
	defer ts.Close()
	host, port := tsHostPort(t, ts)
	hostPort := fmt.Sprintf("%s:%d", host, port)
	h := newHost(t, newStore(t), Config{Grants: map[string]Grant{"p": {Hosts: []string{hostPort}}}, Guard: guardFor(ts), Transport: ts.Client().Transport})
	b := h.Bind("p", packagefmt.TierT1, []string{"net.outbound:" + hostPort})

	id, head := openStream(t, b, "http.post", ts.URL+"/pay", `"body":"{}","content_type":"application/json"`)
	if id == "" || !strings.Contains(string(head["operation_result"]), `"http_status":303`) {
		t.Fatalf("a 303 answer to a streamed call is a stream whose head carries the status: %s", head["operation_result"])
	}
	c, _ := readChunk(t, b, id, "")
	if string(mustB64(t, c.DataB64)) != "see the receipt" {
		t.Fatalf("the redirect's body is pulled from the stream: %+v", c)
	}
}
