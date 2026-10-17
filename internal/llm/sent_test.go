package llm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
)

// sentinelKey is a credential no body may hold: it travels in a header.
const sentinelKey = "sentinel-key-0b7c1d-never-in-a-body"

// sentinelCredential hands the client the sentinel as its bearer and as an
// extra header of its own, as an account credential carries one.
type sentinelCredential struct{}

func (sentinelCredential) Credential(context.Context) (Credential, error) {
	return Credential{Token: sentinelKey, Headers: map[string]string{"X-Account": sentinelKey}}, nil
}
func (sentinelCredential) Stale(context.Context, uint64) error { return nil }

// WHAT THE MODEL IS SENT IS THE BYTES THE PROVIDER RECEIVED, AND NOTHING OF
// THE HEADERS. For each dialect, every request a test server received is in
// the ring, byte for byte, newest first, with the entry, model, facility and
// turn it was sent under; the credential, which travelled in Authorization
// and a header of its own, is nowhere in it. A request retried after a
// server error is one request.
func TestWhatTheModelIsSentIsTheBytesTheProviderReceivedAndNoHeader(t *testing.T) {
	for _, dialect := range []struct{ provider, answer string }{
		{"", `{"id":"c","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"total_tokens":3}}`},
		{"anthropic", `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`},
	} {
		t.Run("provider "+dialect.provider, func(t *testing.T) {
			var mu sync.Mutex
			var received [][]byte
			var failOnce atomic.Bool
			failOnce.Store(true)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if failOnce.CompareAndSwap(true, false) {
					http.Error(w, "busy", http.StatusInternalServerError)
					return
				}
				mu.Lock()
				received = append(received, body)
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, dialect.answer)
			}))
			defer srv.Close()
			ring := NewSentRequests(func() (int, int) { return 20, 1 << 20 })
			c := New(&ClientConfig{Endpoint: srv.URL, Model: "a-model", Provider: dialect.provider, Credential: sentinelCredential{},
				Entry: "an-entry", Sent: ring, Retries: 1, RetryBackoffMS: 1, NoStream: true})
			ctx := wire.WithTapSource(context.Background(), "dream")
			if _, err := c.Chat(ctx, []Message{{Role: "system", Content: "THE SYSTEM TEXT, Ring 0 among it"}, {Role: "user", Content: "THE-QUESTION 😀 30.0"}}, ChatOptions{}); err != nil {
				t.Fatal(err)
			}
			list := ring.List()
			mu.Lock()
			defer mu.Unlock()
			if len(received) == 0 || len(list.Requests) != len(received) {
				t.Fatalf("the provider received %d request(s); the ring holds %d", len(received), len(list.Requests))
			}
			for i, got := range list.Requests {
				sent := received[len(received)-1-i]
				full, ok := ring.Get(got.N)
				if !ok || !bytes.Equal(full.Body, sent) || got.Bytes != len(sent) || got.Body != nil {
					t.Fatalf("request %d as kept is not the bytes received (%d kept, %d received)", got.N, len(full.Body), len(sent))
				}
				if got.Entry != "an-entry" || got.Model != "a-model" || got.Facility != "dream" || got.CallID == "" || got.At.IsZero() {
					t.Fatalf("request %d is listed as %+v", got.N, got)
				}
				if !strings.Contains(SystemText(full.Body), "THE SYSTEM TEXT, Ring 0 among it") {
					t.Fatalf("the system text of request %d reads %q", got.N, SystemText(full.Body))
				}
				if s := fmt.Sprintf("%+v", full); strings.Contains(s, sentinelKey) {
					t.Fatalf("the credential is in what was kept: %s", s)
				}
			}
		})
	}
}

// THE RING IS HELD TO ITS TWO NUMBERS, OLDEST WHOLE REQUEST FIRST. Over the
// count, the oldest go; over the bytes, the oldest go until it fits, but the
// newest is kept alone, whole, however large, and the list says so; kept 0
// keeps nothing, and a ring lowered to 0 lets go of what it held.
func TestTheRingKeepsTheNewestWholeRequestsWithinItsNumbers(t *testing.T) {
	kept, budget := 3, 100
	ring := NewSentRequests(func() (int, int) { return kept, budget })
	add := func(size int) {
		ring.add(SentRequest{Model: "m", Bytes: size, Body: bytes.Repeat([]byte("x"), size)})
	}
	ns := func() string {
		var b []string
		for _, r := range ring.List().Requests {
			b = append(b, fmt.Sprint(r.N))
		}
		return strings.Join(b, ",")
	}
	for i := 0; i < 5; i++ {
		add(10)
	}
	if got := ns(); got != "5,4,3" {
		t.Fatalf("five kept under a count of three: %s", got)
	}
	add(60) // 6: 10+10+60 = 80, within 100
	add(30) // 7: 10+60+30 = 100, within; 5 dropped by the count
	if got := ns(); got != "7,6,5" || ring.List().Over {
		t.Fatalf("within the count and the bytes: %s (over %v)", got, ring.List().Over)
	}
	add(50) // 8: 60+30+50 > 100: 6 goes, then 30+50 fits
	if got := ns(); got != "8,7" {
		t.Fatalf("over the bytes, the oldest whole requests go: %s", got)
	}
	add(250) // 9: larger than the budget alone
	if l := ring.List(); ns() != "9" || !l.Over || l.MaxBytes != 100 {
		t.Fatalf("a request over the budget is not kept alone and said: %s, %+v", ns(), l)
	}
	if r, ok := ring.Get(9); !ok || len(r.Body) != 250 {
		t.Fatal("the request over the budget was not kept whole")
	}
	add(10) // 10: the big one goes, the newest stays
	if l := ring.List(); ns() != "10" || l.Over {
		t.Fatalf("after a small request: %s, %+v", ns(), l)
	}
	kept = 0
	if l := ring.List(); len(l.Requests) != 0 || l.Kept != 0 {
		t.Fatalf("lowered to keep none, the ring still holds %s", ns())
	}
	add(10)
	if _, ok := ring.Get(11); ok || ns() != "" || ring.keeping() {
		t.Fatal("kept 0 kept a request")
	}
}

// A CLIENT KEEPS NOTHING WHEN THE RING KEEPS NOTHING: with kept at 0 no body
// is read back for it.
func TestARingThatKeepsNoneReadsNoBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"c","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	ring := NewSentRequests(func() (int, int) { return 0, 1 << 20 })
	c := New(&ClientConfig{Endpoint: srv.URL, Model: "m", Sent: ring, NoStream: true})
	if _, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "q"}}, ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	if l := ring.List(); len(l.Requests) != 0 {
		t.Fatalf("a ring keeping none holds %d", len(l.Requests))
	}
}

// THE SYSTEM TEXT IS READ FROM EVERY SHAPE THE DIALECTS SEND, WHOLE.
func TestTheSystemTextIsReadFromEveryDialectsShape(t *testing.T) {
	for body, want := range map[string]string{
		`{"system":[{"type":"text","text":"stable "},{"type":"text","text":"rest"}],"messages":[]}`: "stable rest",
		`{"system":"plain"}`:                                              "plain",
		`{"instructions":"the instructions","input":[]}`:                  "the instructions",
		`{"systemInstruction":{"parts":[{"text":"gem"},{"text":"ini"}]}}`: "gemini",
		`{"messages":[{"role":"system","content":"one"},{"role":"user","content":"q"},{"role":"developer","content":[{"type":"text","text":"two"}]}]}`: "one\n\ntwo",
		`{"messages":[{"role":"user","content":"q"}]}`: "",
		`not json`: "",
	} {
		if got := SystemText([]byte(body)); got != want {
			t.Errorf("%s: read %q, want %q", body, got, want)
		}
	}
}
