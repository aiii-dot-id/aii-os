package app

import (
	"context"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"os"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
// .
func TestMain(m *testing.M) {
	harnessLane = func() (string, []string, error) { return "", nil, nil }
	// .
	// .
	modalityGuard = func(context.Context, string) error { return tools.ErrEgressBlocked }
	os.Exit(m.Run())
}
