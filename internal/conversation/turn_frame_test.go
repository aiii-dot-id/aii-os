package conversation

import (
	"strings"
	"testing"
)

// TurnBlockFrame is exactly what TurnBlock adds around this turn's facts —
// the frame and the history note — so the app can reserve it without a
// stand-in for the facts the composer prices.
func TestTheFrameIsWhatTheBlockAddsAroundTheFacts(t *testing.T) {
	const facts = "### Rhythm — 1 turn"
	for _, omitted := range []int{0, 7} {
		block := TurnBlock(facts, omitted, 0, false, false)
		if got, frame := strings.Replace(block, facts, "", 1), TurnBlockFrame(omitted, false); got != frame {
			t.Fatalf("omitted=%d: block minus its facts %q, frame %q", omitted, got, frame)
		}
	}
	if !strings.Contains(TurnBlockFrame(7, false), "7 older conversation turns are not shown") {
		t.Fatal("the frame with omitted history lacks the history note")
	}
}
