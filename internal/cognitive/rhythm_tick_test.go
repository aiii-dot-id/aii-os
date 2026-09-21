package cognitive

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// .
// .
// .
// .
// .
// .
// .
func TestAnUneventfulPassIsCountedAndToldByTheHour(t *testing.T) {
	c := logsink.CaptureForTest(t)
	raw := &fakeRaw{n: 0}
	dream := &fakeOwner{name: "dream"}
	consolidate := &fakeOwner{name: "consolidate"}
	selfModel := &fakeOwner{name: "self_model"}
	review := &fakeOwner{name: "identity_review"}
	r := NewRhythm(raw, freeGate(), dream, consolidate, selfModel, review)
	r.lastConsolidate = time.Now()

	const passes = 6
	for i := 0; i < passes; i++ {
		if res := r.OnAlarm(context.Background(), "rhythm", "wall", 0, ""); !res.Accepted {
			t.Fatalf("pass %d must be accepted", i)
		}
	}
	if strings.Contains(c.String(), "pass complete") {
		t.Fatalf("an uneventful pass must not write a line of its own:\n%s", c.String())
	}

	logsink.FlushDigest()
	out := c.String()
	if !strings.Contains(out, "quiet:") || !strings.Contains(out, "rhythm.pass 6 passes, none due") {
		t.Fatalf("the hour must say how many passes owed nothing, got:\n%s", out)
	}
	if got := len(c.Lines()); got != 1 {
		t.Fatalf("six passes must be one line, got %d:\n%s", got, out)
	}
}
