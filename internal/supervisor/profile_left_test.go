package supervisor

import (
	"errors"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// A PROFILE LEFT IS SAID ONCE, BY PLUGIN, AND THE RETIREMENT IS CLEAN; ANY
// OTHER CLEANUP ANSWER PASSES AS IT CAME (the operator's words, 2026-11-07).
// The Windows test (appcontainer_cleanup_windows_test.go) drives the real
// wall; this holds the supervisor's half on every platform.
func TestAProfileLeftIsSaidOnceByPluginAndRetiresClean(t *testing.T) {
	capture := logsink.CaptureForTest(t)
	s := &Supervisor{spec: Spec{PluginID: "com.example.voice"}}
	left := &ProfileLeftError{Name: "aiios.abc", Reason: "HRESULT 0x80070020", Err: errors.New("delete AppContainer aiios.abc: HRESULT 0x80070020")}
	cleanup := s.profileLeftSaid(func() error { return left })
	for i := 0; i < 2; i++ {
		if err := cleanup(); err != nil {
			t.Fatalf("a profile left held the retirement: %v", err)
		}
	}
	want := "plugin com.example.voice: its Windows container profile aiios.abc could not be removed (HRESULT 0x80070020); it is empty and unused, and uninstalling the plugin removes it"
	if n := strings.Count(capture.String(), want); n != 1 {
		t.Fatalf("the approved line was said %d times, not once:\n%s", n, capture.String())
	}
	// The controls: a revoke or job failure, and a profile left joined to
	// another failure, are held as they came.
	held := errors.New("revoke the container's read grant: Access is denied")
	if err := s.profileLeftSaid(func() error { return held })(); err != held {
		t.Fatalf("another cleanup failure was not passed as it came: %v", err)
	}
	joined := errors.Join(left, held)
	if err := s.profileLeftSaid(func() error { return joined })(); err != joined {
		t.Fatalf("a profile left joined to another failure was swallowed: %v", err)
	}
}
