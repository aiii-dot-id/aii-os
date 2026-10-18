package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The server knows when to come back better than a fixed backoff does —
// but only within reason, and only when it says something we understand.
func TestServerRetryAfter(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		want        time.Duration
	}{
		{"absent", "", 0},
		{"seconds", "12", 12 * time.Second},
		{"zero is not guidance", "0", 0},
		{"absurd is ignored", "99999", 0},
		{"negative is ignored", "-5", 0},
		{"garbage is ignored", "soon", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.value != "" {
				h.Set("Retry-After", tc.value)
			}
			if got := serverRetryAfter(h); got != tc.want {
				t.Fatalf("Retry-After %q -> %v, want %v", tc.value, got, tc.want)
			}
		})
	}
	// An HTTP-date in the near future is honoured.
	h := http.Header{}
	h.Set("Retry-After", time.Now().Add(30*time.Second).UTC().Format(http.TimeFormat))
	if got := serverRetryAfter(h); got <= 0 || got > 31*time.Second {
		t.Fatalf("http-date form -> %v, want ~30s", got)
	}
}

func TestRetryableHTTPErrorIsTyped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("x-should-retry", "true")
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"Error"}}`))
	}))
	defer srv.Close()

	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "key", Model: "model", Retries: -1})
	_, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("rate limit must remain structurally classifiable, got %v", err)
	}
	if httpErr.StatusCode != http.StatusTooManyRequests || !httpErr.Retryable || httpErr.RetryAfter != 3*time.Second {
		t.Fatalf("typed rate limit lost provider guidance: %+v", httpErr)
	}
}

func TestNonRetryableRateLimitIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("x-should-retry", "false")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"type":"quota_error","message":"no balance"}}`))
	}))
	defer srv.Close()

	c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "key", Model: "model", Retries: 3})
	_, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.Retryable {
		t.Fatalf("non-retryable 429 lost its typed refusal: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("non-retryable 429 made %d requests, want one", got)
	}
}

func TestInferenceDoesNotFollowRedirects(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetCalls.Add(1)
	}))
	defer target.Close()

	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()

	client := New(&ClientConfig{Endpoint: redirect.URL, Provider: "anthropic", APIKey: "secret", Model: "model"})
	_, err := client.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	if err == nil || !strings.Contains(err.Error(), "API returned 307") {
		t.Fatalf("redirect was not refused at the configured endpoint: %v", err)
	}
	if targetCalls.Load() != 0 {
		t.Fatal("redirect target received an inference request")
	}
}

// Some providers report an expired credential as a 400. Recognising that
// lets the source reload an owner-updated credential, but an ordinary bad
// request must never be mistaken for one.
func TestBodyIsAuthFailure(t *testing.T) {
	for _, body := range []string{
		`{"error":{"code":"invalid_token"}}`,
		`{"error":{"type":"authentication_error"}}`,
		`{"error":{"type":"invalid_authentication_error"}}`,
		`{"error":"invalid_grant"}`,
		`{"code":"expired_token"}`,
		`{"code":"unauthorized"}`,
	} {
		if !bodyIsAuthFailure([]byte(body)) {
			t.Errorf("should be recognised as an auth failure: %s", body)
		}
	}
	for _, body := range []string{
		`{"detail":"Token expired"}`,
		`{"message":"Invalid API key provided"}`,
		`{"error":{"message":"invalid_token"}}`,
		`{"detail":"Input must be a list"}`,
		`{"detail":"Store must be set to false"}`,
		`{"error":{"message":"model not found"}}`,
		`{"error":{"message":"context length exceeded"}}`,
		``,
	} {
		if bodyIsAuthFailure([]byte(body)) {
			t.Errorf("ordinary bad request must NOT mark the credential stale: %s", body)
		}
	}
}

// A caller outside this package asks the refusal itself whether it was the
// credential's (birth does, before sending the founding prompt in another
// shape): by status, or by the same typed body, and never by a message.
func TestHTTPErrorAuthFailure(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   bool
	}{
		{http.StatusUnauthorized, ``, true},
		{http.StatusForbidden, `{"error":{"message":"forbidden"}}`, true},
		{http.StatusBadRequest, `{"error":{"code":"invalid_api_key"}}`, true},
		{http.StatusBadRequest, `{"error":{"message":"invalid_api_key","type":"invalid_request_error"}}`, false},
		{http.StatusUnprocessableEntity, `{"detail":"Token expired"}`, false},
	} {
		if got := (&HTTPError{StatusCode: tc.status, Body: tc.body}).AuthFailure(); got != tc.want {
			t.Errorf("%d %s: AuthFailure = %t, want %t", tc.status, tc.body, got, tc.want)
		}
	}
}

type staleCountingCredential struct{ calls atomic.Int32 }

func (*staleCountingCredential) Credential(context.Context) (Credential, error) {
	return Credential{Token: "token", Gen: 1}, nil
}

func (c *staleCountingCredential) Stale(context.Context, uint64) error {
	c.calls.Add(1)
	return nil
}

func TestStructured400AdvancesSourceOnce(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":{"code":"invalid_token"}}`))
			return
		}
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer srv.Close()

	credential := &staleCountingCredential{}
	client := New(&ClientConfig{Endpoint: srv.URL, Model: "model", Credential: credential})
	if _, err := client.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	if credential.calls.Load() != 1 || requests.Load() != 2 {
		t.Fatalf("stale calls=%d requests=%d, want 1 and 2", credential.calls.Load(), requests.Load())
	}
}

func TestProse400DoesNotRefreshOrTruncate(t *testing.T) {
	body := `{"error":{"message":"token expired"},"padding":"` + strings.Repeat("x", 9000) + `TAIL"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(body))
	}))
	defer srv.Close()

	credential := &staleCountingCredential{}
	client := New(&ClientConfig{Endpoint: srv.URL, Model: "model", Credential: credential})
	_, err := client.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, ChatOptions{})
	if err == nil || !strings.Contains(err.Error(), "TAIL") {
		t.Fatalf("ordinary 400 lost its complete body: %v", err)
	}
	if credential.calls.Load() != 0 {
		t.Fatalf("prose-only 400 marked the source stale %d times", credential.calls.Load())
	}
}

func TestBackoffSeparatesCongestionFromTransport(t *testing.T) {
	base := 50 * time.Millisecond

	// A local transport hiccup keeps the operator's flat pause: the
	// failure was local, and doubling it only slows recovery from
	// something already fixed.
	for _, attempt := range []int{2, 3, 4, 5} {
		if got := backoffFor(base, attempt, false); got != base {
			t.Fatalf("transport retry %d paused %v, want the configured %v", attempt, got, base)
		}
	}

	// Server congestion doubles from a floor, because nobody can pick a
	// sensible number for someone else's load.
	first := backoffFor(base, 2, true)
	if first < congestionFloor {
		t.Fatalf("first congestion pause %v is below the floor %v", first, congestionFloor)
	}
	second := backoffFor(base, 3, true)
	if second <= first {
		t.Fatalf("congestion backoff did not grow: %v then %v", first, second)
	}
	if third := backoffFor(base, 4, true); third <= second {
		t.Fatalf("congestion backoff stopped growing: %v then %v", second, third)
	}

	// Four retries must now span seconds, not a fifth of a second.
	var total time.Duration
	for attempt := 2; attempt <= 5; attempt++ {
		total += backoffFor(base, attempt, true)
	}
	if total < 5*time.Second {
		t.Fatalf("four congestion retries span only %v — the live failure all over again", total)
	}

	// And it is bounded: a wedged provider must not hold a turn open
	// for minutes.
	if got := backoffFor(base, 20, true); got > 30*time.Second {
		t.Fatalf("congestion backoff unbounded: %v", got)
	}

	// A larger operator base is respected rather than floored down.
	if got := backoffFor(10*time.Second, 2, true); got != 10*time.Second {
		t.Fatalf("an operator base above the floor was overridden: %v", got)
	}
}

// GOOGLE'S REFUSALS ARE READ AS WHAT THEY ARE. A Google API answers in
// google.rpc.Status: its code is a number, and what it refused is named by
// its status and by the reason of an ErrorInfo detail. A key the Gemini
// API does not accept arrives as a 400 whose detail says so, and is the
// credential's failure, not the request's. A rate limit carries when to
// come back in its body (a RetryInfo detail) with no Retry-After header:
// it was never retried, and is now waited out for as long as Google says.
// A spent daily quota names a wait of hours, which is no instruction to a
// call, and is not retried.
func TestGoogleRefusalsAreClassified(t *testing.T) {
	const badKey = `{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"API_KEY_INVALID","domain":"googleapis.com"}]}}`
	const badRequest = `{"error":{"code":400,"message":"Invalid JSON payload received.","status":"INVALID_ARGUMENT"}}`
	rateLimited := func(delay string) string {
		return `{"error":{"code":429,"message":"Quota exceeded.","status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[{"quotaId":"x"}]},{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"` + delay + `"}]}}`
	}
	if !bodyIsAuthFailure([]byte(badKey)) || bodyIsAuthFailure([]byte(badRequest)) || bodyIsAuthFailure([]byte(rateLimited("1s"))) {
		t.Error("only the refused key is a credential failure")
	}
	if !bodyIsAuthFailure([]byte(`{"error":{"code":401,"message":"Request had invalid authentication credentials.","status":"UNAUTHENTICATED"}}`)) {
		t.Error("UNAUTHENTICATED is a credential failure")
	}

	if !bodyIsAuthFailure([]byte("["+badKey+"]")) || bodyIsAuthFailure([]byte("["+badRequest+"]")) || googleRetryDelay([]byte("["+rateLimited("34s")+"]")) != 34*time.Second {
		t.Error("a Status wrapped in a list is read as the Status it is")
	}
	for delay, want := range map[string]time.Duration{"34s": 34 * time.Second, "0.25s": 250 * time.Millisecond, "86400s": 0, "0s": 0, "-3s": 0, "soon": 0} {
		if got := googleRetryDelay([]byte(rateLimited(delay))); got != want {
			t.Errorf("retryDelay %q read as %s, want %s", delay, got, want)
		}
	}
	if googleRetryDelay([]byte(badKey)) != 0 || googleRetryDelay([]byte("not json")) != 0 {
		t.Error("a refusal that names no delay names none")
	}

	// On the wire: each reply in turn, the last one again when they run out.
	chat := func(t *testing.T, retries int, replies ...string) (requests int, err error) {
		t.Helper()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reply := replies[min(requests, len(replies)-1)]
			requests++
			if status := http.StatusOK; strings.HasPrefix(reply, `{"error"`) {
				fmt.Sscanf(reply, `{"error":{"code":%d`, &status)
				w.WriteHeader(status)
			}
			w.Write([]byte(reply))
		}))
		defer srv.Close()
		c := New(&ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", NoStream: true, Retries: retries, RetryBackoffMS: 1})
		err = chatOnce(c)
		return requests, err
	}
	var refusal *HTTPError
	if n, err := chat(t, 1, badKey); !errors.As(err, &refusal) || !refusal.AuthFailure() || n != 1 {
		t.Errorf("a refused key: %v after %d request(s); want one request and a credential failure", err, n)
	}
	if n, err := chat(t, 1, badRequest); !errors.As(err, &refusal) || refusal.AuthFailure() || n != 1 {
		t.Errorf("a bad request: %v after %d request(s); want one request and no credential failure", err, n)
	}
	started := time.Now()
	if n, err := chat(t, 1, rateLimited("0.2s"), openAIResp); err != nil || n != 2 || time.Since(started) < 200*time.Millisecond {
		t.Errorf("a rate limit that names its wait: %v after %d request(s) in %s; want it waited out and retried", err, n, time.Since(started))
	}
	// The wait is Google's, not this client's own backoff: with no retry
	// left, the refusal carries it.
	if _, err := chat(t, -1, rateLimited("34s")); !errors.As(err, &refusal) || refusal.RetryAfter != 34*time.Second || !refusal.Retryable {
		t.Errorf("a rate limit with no retry left: %v (retry after %s); want the typed refusal carrying Google's 34s", err, refusal.RetryAfter)
	}
	if n, err := chat(t, 1, rateLimited("7200s"), openAIResp); !errors.As(err, &refusal) || refusal.StatusCode != 429 || refusal.Retryable || n != 1 {
		t.Errorf("a spent daily quota: %v after %d request(s); want the refusal, not retried", err, n)
	}
}
