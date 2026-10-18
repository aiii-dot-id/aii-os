//go:build linux

package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// Force the child to allocate before the parent's prlimit call, without a
// scheduling lottery. Lowering RLIMIT_AS leaves those mappings intact; the
// admission check must refuse AND reap that child, not serve it as bounded.
func TestAnAlreadyOversizedChildIsRefusedAndReaped(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "allocated")
	saved := applyLimit
	t.Cleanup(func() { applyLimit = saved })
	pid := 0
	applyLimit = func(childPID int, limit uint64) (string, error) {
		pid = childPID
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(marker); err == nil {
				return saved(childPID, limit)
			} else if !errors.Is(err, os.ErrNotExist) {
				return "", err
			}
			time.Sleep(time.Millisecond)
		}
		return "", errors.New("fixture did not signal its pre-limit allocation")
	}
	spec := childSpec("alloc")
	spec.Env = append(spec.Env, "AII_TEST_ALLOCATED="+marker)
	spec.RLimitASBytes = 1 << 30
	s, err := Start(spec, nil)
	if s != nil {
		t.Cleanup(func() { _ = s.Close() })
	}
	var refused *SpawnRefusedError
	if !errors.As(err, &refused) || !errors.Is(err, errAddressSpaceExceeded) {
		t.Fatalf("pre-limit allocation must produce a typed envelope refusal, got %v", err)
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("refused child %d was not reaped: %v", pid, err)
	}
}

// The admission readback cannot replace the kernel ceiling: a child that is
// small when admitted may grow later. Ordinary startup and invocation remain
// usable; only the over-ceiling allocation is stopped.
func TestTheEnvelopeStillBoundsAllocationAfterReadiness(t *testing.T) {
	for _, tc := range []struct {
		name  string
		limit uint64
	}{{"ordinary", 0}, {"bounded", 3 << 30}} {
		t.Run(tc.name, func(t *testing.T) {
			capture := logsink.CaptureForTest(t)
			spec := childSpec("alloc-on-request")
			spec.RLimitASBytes = tc.limit
			s := mustStart(t, spec, nil)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			result, err := s.Invoke(ctx, []byte(testReq))
			if tc.limit == 0 {
				if err != nil || !strings.Contains(string(result), `"allocated":true`) {
					t.Fatalf("ordinary child could not allocate and respond: %s %v", result, err)
				}
			} else if err == nil || strings.Contains(capture.String(), "alloc-ok") {
				t.Fatalf("over-limit allocation succeeded after readiness: %s %v\n%s", result, err, capture.String())
			}
		})
	}
}
