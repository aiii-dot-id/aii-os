package cognitive

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
// .

const priorSurfacing = "You may be noticing that the work keeps returning to the same file."

// .
// .
func dreamWith(t *testing.T, reply string, cfg DreamConfig) (*mockStore, *mockLedger, *mockRingWriter) {
	t.Helper()
	st := &mockStore{
		unprocessedCnt: 2,
		experiences: []store.Experience{
			{ID: "e1", Content: "Read a file", Raw: 1},
			{ID: "e2", Content: "Ran a command", Raw: 1},
		},
	}
	rw := &mockRingWriter{}
	rw.SetRingSection(ring.Ring3, "surfacing", priorSurfacing)
	lg := &mockLedger{st: st}
	if cfg.Threshold == 0 {
		cfg.Threshold = 1
	}
	d := NewDream(st, &mockLLM{override: reply}, lg, rw, cfg)
	if err := d.Execute(context.Background()); err != nil {
		t.Fatalf("execute: %v", err)
	}
	return st, lg, rw
}

// .
// .
// .
// .
// .
func TestAnEssayLengthReplyMintsNothingAndTheSurfacingStands(t *testing.T) {
	buf := captureLog(t)
	essay := "Let me carefully parse this task. " + strings.Repeat("The experiences mention a file and a command. ", 800)
	essay = essay[:36067] + "."
	st, lg, rw := dreamWith(t, essay, DreamConfig{})

	if len(lg.appended) != 0 {
		t.Fatalf("AN ESSAY WAS MINTED INTO THE RECORD: %v", lg.appended)
	}
	if got := rw.section(ring.Ring3, "surfacing"); got != priorSurfacing {
		t.Fatalf("THE ESSAY REACHED THE PROMPT: the surfacing is now %d characters", len(got))
	}
	if st.unprocessedCnt != 2 {
		t.Fatalf("a refused pass consumed its material: %d of 2 remain raw", st.unprocessedCnt)
	}
	line := buf.String()
	if !strings.Contains(line, "36068") || !strings.Contains(line, fmt.Sprint(defaultSurfacingMaxChars)) ||
		!strings.Contains(line, "prompt.surfacing_max_chars") {
		t.Fatalf("the refusal does not say how long the reply was, what the bound is, or whose number it is: %q", line)
	}
}

// .
// .
// .
// .
func TestNothingSurfacedConsumesTheInputsAndMintsNoNote(t *testing.T) {
	st, lg, rw := dreamWith(t, "  "+nothingSurfaced+"\n", DreamConfig{})

	if len(lg.appended) != 1 || lg.appended[0] != ledger.EventDreamRun {
		t.Fatalf("an empty pass must mint [dream.run] and nothing else, got %v", lg.appended)
	}
	runs := lg.runPayloads()
	if len(runs) != 1 || len(runs[0].Inputs) != 2 || len(runs[0].Outputs) != 0 {
		t.Fatalf("the marker must name both inputs and no output, got %+v", runs)
	}
	if lg.models[0] != "mock-model" {
		t.Fatalf("the marker lost its model provenance: %v", lg.models)
	}
	if st.unprocessedCnt != 0 {
		t.Fatalf("an empty pass must consume what it read, or it re-reads it forever: %d remain raw", st.unprocessedCnt)
	}
	if got := rw.section(ring.Ring3, "surfacing"); got != priorSurfacing {
		t.Fatalf("AN EMPTY PASS OVERWROTE THE SURFACING with %q", got)
	}
	if len(rw.written) != 1 {
		t.Fatalf("an empty pass wrote to the ring %d time(s)", len(rw.written)-1)
	}
}

// .
// .
// .
func TestOnlyTheExactWordIsAnEmptyPass(t *testing.T) {
	for _, reply := range []string{
		"Nothing surfaced.",
		"nothing surfaced",
		nothingSurfaced + " — the evidence is unchanged since the last pass.",
		"You may be noticing that " + nothingSurfaced + " is what you keep saying.",
	} {
		_, lg, rw := dreamWith(t, reply, DreamConfig{})
		if len(lg.appended) != 2 || lg.appended[0] != ledger.EventExperienceCreate {
			t.Fatalf("%q was taken for the empty-pass word: %v", reply, lg.appended)
		}
		if got := rw.section(ring.Ring3, "surfacing"); got != reply {
			t.Fatalf("%q was not rendered as the note it is: %q", reply, got)
		}
	}
}

// .
// .
// .
func TestTheBoundCountsCharactersNotBytes(t *testing.T) {
	atBound := strings.Repeat("é", defaultSurfacingMaxChars)
	if len(atBound) <= defaultSurfacingMaxChars || utf8.RuneCountInString(atBound) != defaultSurfacingMaxChars {
		t.Fatal("fixture: the note must be longer in bytes than in characters")
	}
	_, lg, rw := dreamWith(t, atBound, DreamConfig{})
	if len(lg.appended) != 2 || rw.section(ring.Ring3, "surfacing") != atBound {
		t.Fatalf("a note of exactly the bound, in characters, was refused: %v", lg.appended)
	}
	_, lg, rw = dreamWith(t, atBound+"é", DreamConfig{})
	if len(lg.appended) != 0 || rw.section(ring.Ring3, "surfacing") != priorSurfacing {
		t.Fatalf("a note one character past the bound stood: %v", lg.appended)
	}
}

// .
// .
func TestTheSurfacingBoundIsTheOperatorsNumber(t *testing.T) {
	note := strings.Repeat("n", 51)
	_, lg, _ := dreamWith(t, note, DreamConfig{MaxChars: 50})
	if len(lg.appended) != 0 {
		t.Fatalf("the operator's bound of 50 did not govern a note of 51: %v", lg.appended)
	}
	_, lg, _ = dreamWith(t, note, DreamConfig{MaxChars: 51})
	if len(lg.appended) != 2 {
		t.Fatalf("the operator's bound of 51 refused a note of 51: %v", lg.appended)
	}
}

// .
// .
func TestTheNoteIsMintedAndRenderedTrimmed(t *testing.T) {
	_, lg, rw := dreamWith(t, "\n\n  You may be noticing a rhythm.  \n", DreamConfig{})
	if got := rw.section(ring.Ring3, "surfacing"); got != "You may be noticing a rhythm." {
		t.Fatalf("rendered %q", got)
	}
	p, ok := lg.payloads[0].(map[string]interface{})
	if !ok || p["content"] != "You may be noticing a rhythm." || p["raw"] != false || p["provenance"] != "dream" {
		t.Fatalf("minted %+v", lg.payloads[0])
	}
}

// .
// .
// .
func TestANoteTheRecordRefusedIsNotRendered(t *testing.T) {
	st := &mockStore{unprocessedCnt: 1, experiences: []store.Experience{{ID: "e1", Content: "x", Raw: 1}}}
	rw := &mockRingWriter{}
	rw.SetRingSection(ring.Ring3, "surfacing", priorSurfacing)
	lg := &mockLedger{st: st, refuse: func(et ledger.EventType) error {
		if et == ledger.EventExperienceCreate {
			return fmt.Errorf("the door refused the note")
		}
		return nil
	}}
	d := NewDream(st, &mockLLM{override: "You may be noticing a rhythm."}, lg, rw, DreamConfig{Threshold: 1})
	if err := d.Execute(context.Background()); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := rw.section(ring.Ring3, "surfacing"); got != priorSurfacing {
		t.Fatalf("A NOTE THE RECORD REFUSED WAS RENDERED: %q", got)
	}
	if st.unprocessedCnt != 1 {
		t.Fatal("a pass whose note was refused consumed its material")
	}
}

// .
// .
func TestTheDreamPromptAsksForTheExactWord(t *testing.T) {
	for _, want := range []string{"reply with exactly " + nothingSurfaced + " and nothing else", "not your\nreasoning about the task", "refused whole"} {
		if !strings.Contains(dreamSystemPrompt, want) {
			t.Errorf("the dream prompt lost %q", want)
		}
	}
}
