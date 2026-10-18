package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// WHAT A FACILITY CALL SPENT COMES BACK WITH IT, whatever became of the
// reply: an answer carries the usage it reported; a reply cut off at the
// output limit is refused and still carries it, because the call was
// spent; a call that failed carries none, and says so — nothing reported,
// beside its earlier attempts of unknown outcome — so it folds as one
// silent call and never as zero.
func TestTheFacilityDoorsReturnWhatTheCallObserved(t *testing.T) {
	tool := ToolDefinition{Type: "function"}
	tool.Function.Name = "commit"
	answer := func(reason string) string {
		return `{"id":"r","model":"fake","choices":[{"index":0,"message":{"role":"assistant","content":"a noticing"},"finish_reason":` + reason + `}]` + usage
	}
	reported := Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, Reported: true}

	for name, tc := range map[string]struct {
		body   string
		failed bool
	}{
		"an answer":           {answer(`"stop"`), false},
		"a reply cut off":     {answer(`"length"`), true},
		"no choices, usage":   {`{"id":"r","model":"fake","choices":[]` + usage, true},
		"an answer, no usage": {`{"id":"r","model":"fake","choices":[{"index":0,"message":{"role":"assistant","content":"a noticing"},"finish_reason":"stop"}]}`, false},
	} {
		want := reported
		if name == "an answer, no usage" {
			want = Usage{}
		}
		_, _, got, err := replying(t, tc.body).ChatSimple(context.Background(), "s", "u")
		if (err != nil) != tc.failed || got != want {
			t.Errorf("ChatSimple, %s: usage %+v err %v, want %+v failed=%v", name, got, err, want, tc.failed)
		}
		_, _, _, got, err = replying(t, tc.body).ChatStructured(context.Background(), "s", "u", tool)
		if (err != nil) != tc.failed || got != want {
			t.Errorf("ChatStructured, %s: usage %+v err %v, want %+v failed=%v", name, got, err, want, tc.failed)
		}
	}

	// Two server faults: the first is an earlier attempt of unknown
	// outcome, the second is the call that failed.
	var sends int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		sends++
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	c := New(&ClientConfig{Endpoint: srv.URL, Model: "fake", NoStream: true, Retries: 1, RetryBackoffMS: 1})
	_, _, got, err := c.ChatSimple(context.Background(), "s", "u")
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || sends != 2 {
		t.Fatalf("fixture: %d sends, err %v", sends, err)
	}
	if want := (Usage{UnknownAttempts: 1}); got != want {
		t.Fatalf("a failed call returned usage %+v, want %+v", got, want)
	}
	_, _, _, got, err = c.ChatStructured(context.Background(), "s", "u", tool)
	if err == nil || got != (Usage{UnknownAttempts: 1}) {
		t.Fatalf("a failed structured call returned usage %+v (err %v)", got, err)
	}
}
