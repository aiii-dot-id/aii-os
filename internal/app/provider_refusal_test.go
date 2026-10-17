package app

import (
	"errors"
	"fmt"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/memory"
)

// A PROVIDER'S REFUSAL OF WHAT WAS SENT IS THE REQUEST'S, AND NOTHING ELSE
// IS. The backfill asks a refused batch one text at a time only when the
// source says the fault is in what it was handed. A provider says that
// with 400, 413 or 422. A credential it refuses, a limit of rate, a fault
// of its own or no answer at all say nothing about a text, and must not
// set a memory aside.
func TestOnlyAProvidersRefusalOfTheRequestIsATextsFault(t *testing.T) {
	for status, ofText := range map[int]bool{400: true, 413: true, 422: true, 401: false, 403: false, 404: false, 429: false, 500: false, 503: false} {
		err := ofTheRequest(fmt.Errorf("embeddings: %w", &llm.HTTPError{StatusCode: status, Body: "refused"}))
		var fault memory.TextFault
		got := errors.As(err, &fault) && fault.OfTheText() && !fault.EndsThePass()
		if got != ofText {
			t.Errorf("a provider's %d: a text's fault = %v, want %v", status, got, ofText)
		}
		var refusal *llm.HTTPError
		if !errors.As(err, &refusal) || refusal.StatusCode != status {
			t.Errorf("a provider's %d was not kept as what it is: %v", status, err)
		}
	}
	// A key Google does not accept arrives as a 400 that names it: the
	// credential's failure, under a text's status.
	badKey := &llm.HTTPError{StatusCode: 400, Body: `{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"API_KEY_INVALID"}]}}`}
	if err := ofTheRequest(fmt.Errorf("embeddings: %w", badKey)); errors.As(err, new(memory.TextFault)) {
		t.Errorf("a refused key under a 400 was taken for a text's fault: %v", err)
	}
	if err := ofTheRequest(errors.New("dial tcp: connection refused")); err == nil || errors.As(err, new(memory.TextFault)) {
		t.Errorf("no answer at all is not a text's fault: %v", err)
	}
	if ofTheRequest(nil) != nil {
		t.Error("no error must stay none")
	}
}
