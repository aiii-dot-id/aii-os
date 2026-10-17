package broker

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// retryAnswer is what the fixture service answers next: a status, and a
// Retry-After when set is true.
type retryAnswer struct {
	status int
	value  string
	set    bool
}

// retryFixture is a service that answers whatever the test stored last,
// counting every request that reached it, and cuts the connection of any
// request to /cut after reading it.
func retryFixture(t *testing.T) (*Binding, string, *atomic.Pointer[retryAnswer], *atomic.Int32) {
	t.Helper()
	var next atomic.Pointer[retryAnswer]
	var hits atomic.Int32
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		io.Copy(io.Discard, r.Body)
		a := next.Load()
		if a.set {
			w.Header().Set("Retry-After", a.value)
		}
		if r.URL.Path == "/cut" {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(a.status)
		fmt.Fprint(w, `{"said":"so"}`)
	}))
	t.Cleanup(ts.Close)
	host, port := tsHostPort(t, ts)
	hostPort := fmt.Sprintf("%s:%d", host, port)
	h := newHost(t, newStore(t), Config{Grants: map[string]Grant{"p": {Hosts: []string{hostPort}}}, Guard: guardFor(ts), Transport: ts.Client().Transport})
	return h.Bind("p", packagefmt.TierT1, []string{"net.outbound:" + hostPort}), ts.URL, &next, &hits
}

// retryResult is what one received answer came back as: the reply's
// operation_result and its receipt, with the status, reason and effect
// held to what the answer was — the hint changes none of them.
func retryResult(t *testing.T, name string, m map[string]json.RawMessage, status int) (map[string]json.RawMessage, map[string]json.RawMessage) {
	t.Helper()
	wantStatus, wantReason := statusSucceeded, ""
	if status >= 400 {
		wantStatus, wantReason = statusFailed, reasonNetRemoteFailed
	}
	rec := wantResult(t, m, wantStatus, wantReason)
	if eff := receiptField(t, rec, "effect"); eff != EffectPerformed {
		t.Errorf("%s: effect %q, want performed — an answer arrived", name, eff)
	}
	if string(rec["protocol_status"]) != fmt.Sprint(status) || string(rec["transport_outcome"]) != "true" {
		t.Errorf("%s: receipt protocol_status %s transport_outcome %s, want %d true", name, rec["protocol_status"], rec["transport_outcome"], status)
	}
	var or map[string]json.RawMessage
	if err := json.Unmarshal(m["operation_result"], &or); err != nil {
		t.Fatalf("%s: operation_result: %v (%s)", name, err, m["operation_result"])
	}
	if string(or["http_status"]) != fmt.Sprint(status) || string(or["body"]) != `{"said":"so"}` {
		t.Errorf("%s: operation_result %s lost the answer's status or body", name, m["operation_result"])
	}
	return or, rec
}

func TestARetryAfterRidesA429Or503AndChangesNothingElse(t *testing.T) {
	b, url, next, hits := retryFixture(t)
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		for _, c := range []struct {
			name  string
			value string
			set   bool
			want  int64 // -1: absent
		}{
			{"delta-seconds", "120", true, 120},
			{"a zero the service sent", "0", true, 0},
			{"no header", "", false, -1},
			{"an empty header", "", true, -1},
			{"a malformed value", "soon", true, -1},
			{"a fraction", "1.5", true, -1},
			{"an overflowing delta", "99999999999999999999", true, -1},
			{"a delta past what a duration holds", "9223372037", true, -1},
			{"a past date", time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat), true, -1},
		} {
			name := fmt.Sprintf("%d, %s", status, c.name)
			next.Store(&retryAnswer{status: status, value: c.value, set: c.set})
			before := hits.Load()
			or, rec := retryResult(t, name, dispatch(t, b, netParams(url+"/limited", "")), status)
			if n := hits.Load() - before; n != 1 {
				t.Errorf("%s: the service was asked %d times, want once", name, n)
			}
			raw, present := or["retry_after_s"]
			detail := receiptField(t, rec, "detail")
			if c.want < 0 {
				if present {
					t.Errorf("%s: retry_after_s %s rides an answer whose Retry-After cannot be read", name, raw)
				}
				if !strings.Contains(detail, "retry hint unavailable") {
					t.Errorf("%s: receipt detail %q does not say the hint is unavailable", name, detail)
				}
				continue
			}
			if !present || string(raw) != fmt.Sprint(c.want) {
				t.Errorf("%s: retry_after_s = %s (present %v), want %d", name, raw, present, c.want)
			}
			if detail != "" {
				t.Errorf("%s: a readable hint needs no detail, got %q", name, detail)
			}
		}
	}
}

// An HTTP-date is converted against the host clock, rounded up to the
// second, so it never says earlier than the service asked. The broker
// reads its own clock, so the value is held between what the date says
// from just before the call and from just after it.
func TestAnHTTPDateRetryAfterIsTheSecondsUntilIt(t *testing.T) {
	b, url, next, _ := retryFixture(t)
	ceil := func(d time.Duration) int64 { return int64(math.Ceil(d.Seconds())) }
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t0 := time.Now()
		when := t0.Add(2 * time.Minute).UTC().Truncate(time.Second)
		next.Store(&retryAnswer{status: status, value: when.Format(http.TimeFormat), set: true})
		or, rec := retryResult(t, fmt.Sprint(status), dispatch(t, b, netParams(url+"/limited", "")), status)
		t1 := time.Now()
		var got int64
		if err := json.Unmarshal(or["retry_after_s"], &got); err != nil {
			t.Fatalf("%d: retry_after_s %s: %v", status, or["retry_after_s"], err)
		}
		if lo, hi := ceil(when.Sub(t1)), ceil(when.Sub(t0)); got < lo || got > hi || got <= 0 {
			t.Fatalf("%d: retry_after_s = %d, want within [%d, %d]", status, got, lo, hi)
		}
		if d := receiptField(t, rec, "detail"); d != "" {
			t.Fatalf("%d: a readable date needs no detail, got %q", status, d)
		}
	}
}

// The hint is the service's word on any answer received whole, whatever
// its status: a 200 or a 500 that sends one carries it, and nothing else
// changes. Only a 429 or 503, whose Retry-After is the service's
// instruction, says when it is missing; other answers say nothing.
func TestARetryAfterOnAnyOtherAnswerRidesAndIsNotMissed(t *testing.T) {
	b, url, next, _ := retryFixture(t)
	for _, c := range []struct {
		status int
		value  string
		set    bool
		want   int64 // -1: absent
	}{
		{http.StatusOK, "30", true, 30},
		{http.StatusOK, "", false, -1},
		{http.StatusOK, "soon", true, -1},
		{http.StatusInternalServerError, "30", true, 30},
		{http.StatusInternalServerError, "", false, -1},
		{http.StatusBadRequest, "soon", true, -1},
	} {
		name := fmt.Sprintf("%d with %q (set %v)", c.status, c.value, c.set)
		next.Store(&retryAnswer{status: c.status, value: c.value, set: c.set})
		or, rec := retryResult(t, name, dispatch(t, b, netParams(url+"/any", "")), c.status)
		raw, present := or["retry_after_s"]
		if c.want < 0 && present {
			t.Errorf("%s: retry_after_s %s rides an unreadable hint", name, raw)
		}
		if c.want >= 0 && string(raw) != fmt.Sprint(c.want) {
			t.Errorf("%s: retry_after_s = %s, want %d", name, raw, c.want)
		}
		if d := receiptField(t, rec, "detail"); d != "" {
			t.Errorf("%s: detail %q — only a 429 or 503 says its hint is missing", name, d)
		}
	}
}

// THE HINT NEVER LICENSES REPEATING AN UNCERTAIN ACT. A 429 that says
// "0" is answered once, with the hint, whether or not the call declared
// itself idempotent: the broker repeats nothing on the service's word.
// And an exchange whose answer was lost is unknown, with no answer to
// carry a hint — whatever the service meant to send.
func TestTheHintNeverRepeatsASend(t *testing.T) {
	b, url, next, hits := retryFixture(t)
	for _, args := range []string{
		`{"body":"x","content_type":"text/plain"}`,
		`{"body":"x","content_type":"text/plain","idempotent":true}`,
	} {
		next.Store(&retryAnswer{status: http.StatusTooManyRequests, value: "0", set: true})
		before := hits.Load()
		or, _ := retryResult(t, args, dispatch(t, b, verbParams("http.post", url+"/send", args)), http.StatusTooManyRequests)
		if n := hits.Load() - before; n != 1 {
			t.Fatalf("%s: a 429 with Retry-After 0 was sent %d times, want once", args, n)
		}
		if string(or["retry_after_s"]) != "0" {
			t.Fatalf("%s: retry_after_s = %s, want 0", args, or["retry_after_s"])
		}
	}

	next.Store(&retryAnswer{status: http.StatusTooManyRequests, value: "0", set: true})
	before := hits.Load()
	m := dispatch(t, b, verbParams("http.post", url+"/cut", `{"body":"x","content_type":"text/plain"}`))
	rec := wantResult(t, m, statusFailed, reasonNetEffectUnknown)
	if receiptField(t, rec, "effect") != EffectUnknown {
		t.Fatalf("a lost answer: effect %s, want unknown", rec["effect"])
	}
	if n := hits.Load() - before; n != 1 {
		t.Fatalf("a send whose answer was lost was sent %d times, want once", n)
	}
	if or, ok := m["operation_result"]; ok && strings.Contains(string(or), "retry_after_s") {
		t.Fatalf("an unknown exchange carries a hint: %s", or)
	}
}
