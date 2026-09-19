// .
// .
// .
// .
// .
// .
// .

package pluginhost

import (
	"testing"
	"time"
)

func TestAuditRecoveredCompletionDoesNotDisappearBehindWireWatermark(t *testing.T) {
	p := newEpochProbe(t)
	p.open("s1")
	probeEvent(p, `{"type":"playback_progress","session_id":"s1","sequence":4}`)
	if e := take(t, p.v); e.Sequence != 4 {
		t.Fatalf("control: %+v", e)
	}
	go func() { p.reply(p.read("speech.session.status"), snapshot480) }()
	if _, err := p.v.Status(p.ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-p.v.Observe():
		if e.Type != "input_finished" || e.Sequence != 3 {
			t.Fatalf("recovery wrong: %+v", e)
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("validated completion seq3 was discarded behind wire seq4; application was never notified")
	}
	p.v.mu.Lock()
	seq := p.v.lastSeq
	p.v.mu.Unlock()
	if seq != 4 {
		t.Fatalf("historical status changed the real wire watermark: %d", seq)
	}
	probeEvent(p, `{"type":"playback_progress","session_id":"s1","sequence":5}`)
	if e := take(t, p.v); e.Type != "playback_progress" || e.Sequence != 5 {
		t.Fatalf("later genuine wire event lost: %+v", e)
	}
}
