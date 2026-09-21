package cognitive

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

// .
// .
func captureLog(t *testing.T) *logsink.Capture {
	t.Helper()
	buf := logsink.CaptureForTest(t)
	return buf
}

// .
// .
func backedEnvelope(view string) string {
	return `{"operations": [{"op": "upsert", "id": "n1", "statement": "Three experiences share a pattern", ` +
		`"confidence": 0.6, "evidence": ["e1", "e2", "e3"]}], "ring3_view": "` + view + `"}`
}

func TestAnEssayLengthViewIsRefusedAndTheStoreRendersInstead(t *testing.T) {
	buf := captureLog(t)
	essay := strings.Repeat("a", 63_000)
	_, rw, _ := consolidateWith(t, backedEnvelope(essay))

	got := rw.section(ring.Ring3, "working_truth")
	if strings.Contains(got, essay) {
		t.Fatalf("a %d-char view became the working truth: the accordion drops WHOLE PARTS, "+
			"so a ring this size is omitted entire on every compose and the identity runs with no working truth at all", len(essay))
	}
	// .
	if !strings.Contains(got, "something already known") {
		t.Fatalf("Ring 3 was not rendered from the beliefs that do exist: %q", got)
	}
	// .
	// .
	line := buf.String()
	if !strings.Contains(line, fmt.Sprint(len(essay))) || !strings.Contains(line, fmt.Sprint(defaultRing3MaxChars)) ||
		!strings.Contains(line, "prompt.ring3_max_chars") {
		t.Fatalf("the refusal names neither the size nor the bound: %q", line)
	}
}

func TestAnOrdinaryViewIsWrittenUnchanged(t *testing.T) {
	view := strings.Repeat("b", 3_000)
	_, rw, _ := consolidateWith(t, backedEnvelope(view))

	if got := rw.section(ring.Ring3, "working_truth"); got != view {
		t.Fatalf("an ordinary %d-char view did not land whole (%d chars written): the bound must admit every ordinary pass",
			len(view), len(got))
	}
}

// .
// .
// .
func TestAnUnsetBoundResolvesToTheDefault(t *testing.T) {
	_, rw, _ := consolidateWith(t, backedEnvelope(strings.Repeat("c", defaultRing3MaxChars)))
	if got := rw.section(ring.Ring3, "working_truth"); len(got) != defaultRing3MaxChars {
		t.Fatalf("a view of exactly the default (%d) was not kept: %d chars written", defaultRing3MaxChars, len(got))
	}

	_, over, _ := consolidateWith(t, backedEnvelope(strings.Repeat("c", defaultRing3MaxChars+1)))
	if got := over.section(ring.Ring3, "working_truth"); len(got) > defaultRing3MaxChars {
		t.Fatalf("a view one char past the default (%d) stood: the unset bound is not the default", defaultRing3MaxChars+1)
	}
}

// .
// .
// .
func TestTheDeterministicRenderDropsWholeLinesAndDeclaresTheRoute(t *testing.T) {
	st := &mockStore{unprocessedCnt: 3}
	for i := 0; i < 60; i++ {
		st.experiences = append(st.experiences, store.Experience{ID: fmt.Sprintf("e%d", i), Content: "raw", Raw: 1})
		st.beliefs = append(st.beliefs, store.Belief{
			ID:        fmt.Sprintf("b%d", i),
			Statement: fmt.Sprintf("belief %02d, long enough to matter for the bound (end)", i),
			Ring:      3,
		})
	}
	rw := &mockRingWriter{}
	// .
	// .
	c := NewConsolidate(st, &mockLLM{err: errors.New("substrate down")}, &mockLedger{st: st}, rw,
		ConsolidateConfig{Threshold: 3, Ring3MaxChars: 900})
	if err := c.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}

	got := rw.section(ring.Ring3, "working_truth")
	if len(got) > 900 {
		t.Fatalf("the deterministic render passed its own bound: %d chars", len(got))
	}
	if !strings.Contains(got, "not in view; recall (source=ledger) reaches the rest") {
		t.Fatalf("the dropped beliefs were not declared with their route: %q", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "- ") && !strings.HasSuffix(line, "(end)") {
			t.Fatalf("a belief was cut mid-line: %q — a statement severed reads as though it survived", line)
		}
	}
}
