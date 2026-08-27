package llm

import (
	"context"
	"errors"
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

// LIVE, 2026-08-24: a substrate probe against Claude hit 529
// "Overloaded", retried four times at the configured 50ms, exhausted
// its attempts in a fifth of a second, and refused a working provider —
// telling the operator their credential could not complete a minimal
// inference request when Anthropic was simply busy. Four retries inside
// 200ms is not a policy against congestion; it is the same request sent
// five times.
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
