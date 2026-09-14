package dashboard

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
func TestCancellationIsNotReportedAsAnIdentityError(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		reported bool
	}{
		{"operator stop", context.Canceled, false},
		{"wrapped operator stop", fmt.Errorf("LLM call: read response body: %w", context.Canceled), false},
		{"deadline is a real fault", context.DeadlineExceeded, true},
		{"provider failure is a real fault", errors.New("API returned 500"), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := !errors.Is(c.err, context.Canceled)
			if got != c.reported {
				verb := "suppressed"
				if got {
					verb = "reported as an identity error"
				}
				t.Fatalf("%v was %s; want reported=%v", c.err, verb, c.reported)
			}
		})
	}
}

// .
// .
// .
func TestDeadlineStillReaches(t *testing.T) {
	wrapped := fmt.Errorf("LLM call: %w", context.DeadlineExceeded)
	if errors.Is(wrapped, context.Canceled) {
		t.Fatal("a deadline was mistaken for a cancellation — the operator would hear nothing when the model stopped answering")
	}
}
