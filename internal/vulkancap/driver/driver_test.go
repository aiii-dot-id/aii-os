package driver

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/vulkancap"
)

func TestProbeFailureStatuses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"loader", errLoaderUnavailable, vulkancap.ExitLoaderUnavailable},
		{"no discrete device", errNoDiscreteDevice, vulkancap.ExitNoDiscreteDevice},
		{"ambiguous devices", errAmbiguousDevices, vulkancap.ExitAmbiguousDevices},
		{"memory budget", errMemoryBudgetUnavailable, vulkancap.ExitMemoryBudgetUnavailable},
		{"unknown", errors.New("unclassified driver failure"), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, diagnostic bytes.Buffer
			calls := 0
			status := run(&out, &diagnostic, func() (Capacity, error) {
				calls++
				return Capacity{Total: 8192, Available: 4096}, fmt.Errorf("wrapped: %w", tc.err)
			})
			if status != tc.status || calls != 1 || out.Len() != 0 || diagnostic.Len() == 0 {
				t.Fatalf("status=%d calls=%d capacity=%q diagnostic=%q", status, calls, out.String(), diagnostic.String())
			}
			if tc.status != 1 && vulkancap.FailureReason(status) == "" {
				t.Fatal("recognized failure status has no safe explanation")
			}
		})
	}
	for _, status := range []int{-1, 0, 1, 3, 4, 255} {
		if why := vulkancap.FailureReason(status); why != "" {
			t.Errorf("unrecognized status %d has explanation %q", status, why)
		}
	}
}

func TestProbeSuccessAndWriteFailure(t *testing.T) {
	measure := func() (Capacity, error) { return Capacity{Total: 8192, Available: 4096}, nil }
	var out, diagnostic bytes.Buffer
	if status := run(&out, &diagnostic, measure); status != 0 || out.String() != "{\"total\":8192,\"available\":4096}\n" || diagnostic.Len() != 0 {
		t.Fatalf("status=%d capacity=%q diagnostic=%q", status, out.String(), diagnostic.String())
	}
	if status := run(rejectOutput{}, io.Discard, measure); status != 1 {
		t.Fatalf("failed capacity write exited %d, want failure", status)
	}
}

type rejectOutput struct{}

func (rejectOutput) Write([]byte) (int, error) { return 0, errors.New("output unavailable") }
