package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

func fakeProvider(t *testing.T, promptTokens int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"r","model":"fake","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":`+itoa(promptTokens)+`,"completion_tokens":1,"total_tokens":`+itoa(promptTokens+1)+`}}`)
	}))
	t.Cleanup(srv.Close)
	return srv, &requests
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// .
// .
// .
// .
// .
func TestTheCheckAndTheSendAgreeAndTheCheckSendsNothing(t *testing.T) {
	srv, requests := fakeProvider(t, 10)
	system := strings.Repeat("the identity's authority rings. ", 20)
	base, err := EstimateInputTokens(simpleMessages(context.Background(), system, ""), nil)
	if err != nil {
		t.Fatal(err)
	}
	c := New(&ClientConfig{Endpoint: srv.URL, Model: "fake", MaxInputTokens: base + 40, NoStream: true})

	sent := int32(0)
	for n := 0; n < 300; n += 7 {
		user := strings.Repeat("é", n)
		check := c.CheckSimple(context.Background(), system, user)
		if got := requests.Load(); got != sent {
			t.Fatalf("a CHECK reached the provider (%d requests, %d sends)", got, sent)
		}
		_, _, send := c.ChatSimple(context.Background(), system, user)
		if (check == nil) != (send == nil) {
			t.Fatalf("%d code points: the check says %v and the send says %v", n, check, send)
		}
		var limit *ContextLimitError
		if check != nil && !errors.As(check, &limit) {
			t.Fatalf("the check refused with %v, want the ContextLimitError the send refuses with", check)
		}
		if send == nil {
			sent++
		}
	}
	if sent == 0 || int(sent) == 300/7+1 {
		t.Fatalf("the rig never crossed the ceiling (%d sends)", sent)
	}
	if got := requests.Load(); got != sent {
		t.Fatalf("the provider saw %d requests for %d admitted sends — a refused request left the host", got, sent)
	}
}

// .
// .
// .
// .
// .
// .
func TestAProviderCountAboveTheEstimateIsSaid(t *testing.T) {
	estimate, err := EstimateInputTokens(simpleMessages(context.Background(), "s", "u"), nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		reported int
		warns    bool
	}{
		"the provider counted more":     {estimate * 3, true},
		"the provider counted less":     {estimate - 1, false},
		"the provider counted the same": {estimate, false},
	} {
		srv, _ := fakeProvider(t, tc.reported)
		logs := logsink.CaptureForTest(t)
		c := New(&ClientConfig{Endpoint: srv.URL, Model: "fake", MaxInputTokens: 100000, NoStream: true})
		if _, _, err := c.ChatSimple(context.Background(), "s", "u"); err != nil {
			t.Fatal(err)
		}
		if got := logs.Contains("UNDER-counts"); got != tc.warns {
			t.Errorf("%s (%d against an estimate of %d): warned=%v, want %v\n%s", name, tc.reported, estimate, got, tc.warns, logs.String())
		}
	}
}
