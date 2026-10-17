package prompt

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ring"
)

// ring3Content builds Ring 3 exactly as compose does: the composer's
// frame, then the three parts in render order.
func ring3Content() string {
	secs := []ring.Section{
		{Name: "surfacing", Content: strings.Repeat("DREAM rewrites this on every pass.\n\n", 4)},
		{Name: "operator", Content: strings.Repeat("The person I work with, accumulated across sessions.\n\n", 4)},
		{Name: "working_truth", Content: strings.Repeat("Readback before claim is law. Narration is not bytes.\n\n", 4)},
	}
	return frameRing3 + "\n\n" + RenderRing3Body(secs)
}

func TestRing3KeepsCoreShedsEphemeral(t *testing.T) {
	got := summarize("ring3", ring3Content(), budgetRoute)

	if !strings.Contains(got, "Readback before claim is law") {
		t.Error("working_truth did not survive the fold — the ruling is inverted")
	}
	if strings.Contains(got, "DREAM rewrites this on every pass") {
		t.Error("surfacing survived: the most regenerable part must yield first")
	}
	if !strings.Contains(got, "## What You're Working With") {
		t.Error("working_truth's header went with its body")
	}
}

// TestRing3FrameSurvives: the frame is the composer's own words about
// what Ring 3 IS, not a part's content. Losing it would leave the
// surviving parts with no statement of what they are.
func TestRing3FrameSurvives(t *testing.T) {
	got := summarize("ring3", ring3Content(), budgetRoute)
	if !strings.Contains(got, "# What You Are Discovering") {
		t.Error("the Ring 3 frame was dropped with the parts")
	}
}

// TestRing3ReceiptNamesWhatWent: a count with no cue is a receipt that
// cannot be redeemed. The omission must say WHICH part is gone and
// carry a live route to it.
func TestRing3ReceiptNamesWhatWent(t *testing.T) {
	got := summarize("ring3", ring3Content(), budgetRoute)

	if !strings.Contains(got, SummaryMarker) {
		t.Fatal("no summary declaration: a reduced section must say it was reduced")
	}
	if !strings.Contains(got, "What You're Noticing") {
		t.Error("receipt does not NAME the dropped part — a count alone is not redeemable")
	}
	if !strings.Contains(got, "recall (source=experiences)") {
		t.Error("receipt carries no live route back to the content")
	}
	if !strings.Contains(got, "2 of 3 parts kept") {
		t.Errorf("receipt does not state the extent honestly: %q", got)
	}
}

// TestRing3FoldIsDeterministic: the same input must fold the same way
// every time — a digest that moves under identical pressure is not a
// digest.
func TestRing3FoldIsDeterministic(t *testing.T) {
	content := ring3Content()
	once, again := summarize("ring3", content, budgetRoute), summarize("ring3", content, budgetRoute)
	if once != again {
		t.Error("ring3 fold is not deterministic")
	}
}

// TestRing3FoldShrinksRealisticSection: at production sizes — parts of
// hundreds of tokens — dropping a whole part must net-reduce even after
// the receipt and its route are added.
func TestRing3FoldShrinksRealisticSection(t *testing.T) {
	secs := []ring.Section{
		{Name: "surfacing", Content: strings.Repeat("DREAM rewrites this on every pass, and the next pass rewrites it again.\n\n", 30)},
		{Name: "operator", Content: strings.Repeat("The person I work with, accumulated slowly across many sessions.\n\n", 30)},
		{Name: "working_truth", Content: strings.Repeat("Readback before claim is law. Narration is not bytes, in any tense.\n\n", 30)},
	}
	content := frameRing3 + "\n\n" + RenderRing3Body(secs)
	if got, want := estimateTokens(summarize("ring3", content, budgetRoute)), estimateTokens(content); got >= want {
		t.Errorf("realistic ring3 fold did not shrink: %d >= %d", got, want)
	}
}

// TestRing3TinyFoldIsRefusedNotApplied is the edge the first draft of
// this file got wrong. When the parts are small, the receipt and its
// route cost MORE than the part they replace, so summarize returns
// something larger than its input. That is not a bug in summarize — it
// is why FoldAndTrim checks before accepting. The ladder must refuse
// the digest and move on, never grow the prompt while claiming to
// reduce it.
func TestRing3TinyFoldIsRefusedNotApplied(t *testing.T) {
	content := ring3Content() // deliberately small
	if estimateTokens(summarize("ring3", content, budgetRoute)) < estimateTokens(content) {
		t.Skip("content large enough to shrink; this edge no longer applies")
	}
	sections := []Section{{Name: "What You Are Discovering", Content: content, Source: "ring3", Elastic: true}}
	out, _ := newBudgetEnforcer(1, nil).FoldAndTrim(sections)
	if out[0].Folded {
		t.Error("a fold that does not shrink was applied anyway")
	}
	if out[0].Content != "" && estimateTokens(out[0].Content) > estimateTokens(content) {
		t.Error("the section grew under budget pressure")
	}
}

// TestRing3WithoutPartsFallsBack: content carrying no part headers has
// no whole part to drop, so the existing unit discipline applies
// unchanged. This is the path every pre-existing ring3 test takes.
func TestRing3WithoutPartsFallsBack(t *testing.T) {
	blob := strings.Repeat("a paragraph of working truth.\n\n", 6)
	if got, want := summarize("ring3", blob, budgetRoute), SummarizeUnits(blob, routeFor("ring3", budgetRoute)); got != want {
		t.Error("headerless ring3 content no longer follows SummarizeUnits")
	}
}

// TestRing3SinglePartFallsBack: one part is not a ladder. Dropping the
// only part present would empty the section rather than reduce it.
func TestRing3SinglePartFallsBack(t *testing.T) {
	secs := []ring.Section{{Name: "working_truth", Content: strings.Repeat("Readback before claim is law.\n\n", 6)}}
	content := frameRing3 + "\n\n" + RenderRing3Body(secs)
	got := summarize("ring3", content, budgetRoute)
	if !strings.Contains(got, "Readback before claim is law") {
		t.Error("the only part present was dropped instead of reduced")
	}
}

func TestRing3YieldOrderMatchesRenderMembership(t *testing.T) {
	if len(ring3YieldOrder) != len(ring3Parts) {
		t.Fatalf("yield order has %d rungs, render has %d parts", len(ring3YieldOrder), len(ring3Parts))
	}
	for _, name := range ring3YieldOrder {
		found := false
		for _, p := range ring3Parts {
			if p.name == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("yield order names %q, which is not a rendered part", name)
		}
	}
	if last := ring3YieldOrder[len(ring3YieldOrder)-1]; last != "working_truth" {
		t.Errorf("working_truth must yield LAST; ladder ends with %q", last)
	}
}

func TestRing3FoldSurvivesAQuotedHeader(t *testing.T) {
	secs := []ring.Section{
		{Name: "surfacing", Content: strings.Repeat("DREAM rewrites this on every pass.\n\n", 4)},
		{Name: "working_truth", Content: "The section headed ## Who You Work With is empty until consolidation runs. " +
			strings.Repeat("Readback before claim is law. Narration is not bytes. ", 6)},
	}
	content := frameRing3 + "\n\n" + RenderRing3Body(secs)
	got := summarize("ring3", content, budgetRoute) // must not panic
	if !strings.Contains(got, "Readback before claim is law") {
		t.Fatalf("the load-bearing part did not survive: %q", got)
	}
	if strings.Contains(got, "DREAM rewrites") {
		t.Fatalf("the part that gives way first survived instead: %q", got)
	}
}

// PARTS IN AN UNEXPECTED ORDER STILL FOLD IN BOUNDS: the render order is
// a convention, not a law the fold may slice by.
func TestRing3FoldSurvivesPartsOutOfOrder(t *testing.T) {
	secs := []ring.Section{
		{Name: "working_truth", Content: strings.Repeat("Readback before claim is law. Narration is not bytes.\n\n", 4)},
		{Name: "operator", Content: strings.Repeat("The person I work with, accumulated across sessions.\n\n", 4)},
		{Name: "surfacing", Content: strings.Repeat("DREAM rewrites this on every pass.\n\n", 4)},
	}
	content := frameRing3 + "\n\n" + RenderRing3Body(secs)
	got := summarize("ring3", content, budgetRoute) // must not panic
	if !strings.Contains(got, "2 of 3 parts kept") || !strings.Contains(got, "Readback before claim") {
		t.Fatalf("the fold lost its shape when the parts were reordered: %q", got)
	}
}

// A HEADER STANDING TWICE IS NOT A BOUNDARY: the fold refuses to guess and
// hands the whole to the unit summarizer instead.
func TestRing3FoldRefusesADoubledHeader(t *testing.T) {
	secs := []ring.Section{
		{Name: "surfacing", Content: "noticing\n\n## What You're Working With\n\nnot the real one\n\n"},
		{Name: "working_truth", Content: strings.Repeat("Readback before claim is law.\n\n", 6)},
	}
	content := frameRing3 + "\n\n" + RenderRing3Body(secs)
	got := summarize("ring3", content, budgetRoute) // must not panic
	if strings.Contains(got, "parts kept") {
		t.Fatalf("a doubled header was treated as a boundary: %q", got)
	}
}

// THE RECEIPT NAMES A ROUTE THAT ARRIVES. The operator model is rebuilt by
// consolidation and reachable by no recall source; when it is the part not
// in view, the receipt says so instead of pointing at recall.
func TestRing3ReceiptIsHonestAboutTheOperatorModel(t *testing.T) {
	secs := []ring.Section{
		{Name: "operator", Content: strings.Repeat("The person I work with, accumulated across sessions.\n\n", 4)},
		{Name: "working_truth", Content: strings.Repeat("Readback before claim is law. Narration is not bytes.\n\n", 4)},
	}
	got := summarize("ring3", frameRing3+"\n\n"+RenderRing3Body(secs), budgetRoute)
	if !strings.Contains(got, "Who You Work With not in view") || !strings.Contains(got, "not searchable") {
		t.Fatalf("the receipt promised a route that does not arrive: %q", got)
	}
}
