package broker

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// A STORAGE CALL THAT TOOK LONG IS SAID, WITH ITS OPERATION AND ITS TIME. A
// plugin that bounds its own wait for the host's storage gives up when
// the disk is slow, and the host's log had nothing to say which call it
// was or how long it took. With the operator's threshold at a nanosecond
// every call is long: a write through the broker is said, by the plugin,
// the operation and the key the number comes from. With nothing in the
// file the same write, which takes milliseconds, is not.
func TestAStorageCallThatTookLongIsSaidWithItsOperation(t *testing.T) {
	for _, tc := range []struct {
		name string
		from time.Duration
		said bool
	}{
		{"every call is long", time.Nanosecond, true},
		{"the default", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lines := logsink.CaptureForTest(t)
			h := newHost(t, newStore(t), Config{MaxFilesBytes: 4096, SlowStorageCall: tc.from})
			b := h.Bind("p", packagefmt.TierT0, []string{"fs.private"})
			b.SetFiles(filepath.Join(t.TempDir(), "p"), 0)
			wantResult(t, dispatch(t, b, fsParams("fs.write", "private", "notes/a.txt", `{"data":"hello"}`)), statusSucceeded, "")
			out := lines.String()
			said := strings.Contains(out, "broker.budget: plugin p: fs.write took ") && strings.Contains(out, "plugins.runtime.slow_storage_call_ms")
			if said != tc.said {
				t.Fatalf("a write with the threshold at %s: said %v, want %v\n%s", tc.from, said, tc.said, out)
			}
		})
	}
}

// THE THRESHOLD IS WHERE THE LINE BEGINS. A call that took exactly the
// threshold is said, one a millisecond under it is not, and the default
// is one second.
func TestTheSlowStorageLineBeginsAtTheThreshold(t *testing.T) {
	h := newHost(t, newStore(t), Config{SlowStorageCall: 200 * time.Millisecond})
	b := h.Bind("p", packagefmt.TierT0, nil)
	for _, tc := range []struct {
		took time.Duration
		said bool
	}{{199 * time.Millisecond, false}, {200 * time.Millisecond, true}, {3 * time.Second, true}} {
		lines := logsink.CaptureForTest(t)
		b.noteSlowStorage("fs.publish", tc.took)
		if said := strings.Contains(lines.String(), "fs.publish took "+tc.took.String()); said != tc.said {
			t.Fatalf("a call of %s under a threshold of 200ms: said %v, want %v (%s)", tc.took, said, tc.said, lines.String())
		}
	}
	d := newHost(t, newStore(t), Config{}).Bind("p", packagefmt.TierT0, nil)
	lines := logsink.CaptureForTest(t)
	d.noteSlowStorage("kv.put", 999*time.Millisecond)
	d.noteSlowStorage("kv.get", time.Second)
	if out := lines.String(); strings.Contains(out, "kv.put") || !strings.Contains(out, "kv.get took 1s") {
		t.Fatalf("with no threshold named the line begins at one second: %s", out)
	}
}
