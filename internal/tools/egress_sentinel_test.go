package tools

import (
	"context"
	"errors"
	"testing"
)

// .
// .
// .
func TestFetchGuardRefusalsWrapTheEgressSentinel(t *testing.T) {
	for _, u := range []string{
		"http://127.0.0.1/x",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.5/x",
		"http://[::1]/x",
	} {
		err := FetchGuard(context.Background(), u)
		if err == nil {
			t.Fatalf("%s was not refused", u)
		}
		if !errors.Is(err, ErrEgressBlocked) {
			t.Fatalf("%s refused without the typed sentinel: %v", u, err)
		}
	}
}
