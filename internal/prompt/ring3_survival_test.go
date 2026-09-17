package prompt

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ring"
)

// .
// .
func ring3Content() string {
	secs := []ring.Section{
		{Name: "surfacing", Content: strings.Repeat("DREAM rewrites this on every pass.\n\n", 4)},
		{Name: "operator", Content: strings.Repeat("The person I work with, accumulated across sessions.\n\n", 4)},
		{Name: "working_truth", Content: strings.Repeat("Readback before claim is law. Narration is not bytes.\n\n", 4)},
	}
	return frameRing3 + "\n\n" + RenderRing3Body(secs)
}

// .
// .
// .
// .
// .
func TestRing3KeepsCoreShedsEphemeral(t *testing.T) {
	got := summarize("ring3", ring3Content())

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

// .
// .
// .
func TestRing3FrameSurvives(t *testing.T) {
	got := summarize("ring3", ring3Content())
	if !strings.Contains(got, "# What You Are Discovering") {
		t.Error("the Ring 3 frame was dropped with the parts")
	}
}

// .
// .
// .
func TestRing3ReceiptNamesWhatWent(t *testing.T) {
	got := summarize("ring3", ring3Content())

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

// .
// .
// .
func TestRing3FoldIsDeterministic(t *testing.T) {
	content := ring3Content()
	once, again := summarize("ring3", content), summarize("ring3", content)
	if once != again {
		t.Error("ring3 fold is not deterministic")
	}
}

// .
// .
// .
func TestRing3FoldShrinksRealisticSection(t *testing.T) {
	secs := []ring.Section{
		{Name: "surfacing", Content: strings.Repeat("DREAM rewrites this on every pass, and the next pass rewrites it again.\n\n", 30)},
		{Name: "operator", Content: strings.Repeat("The person I work with, accumulated slowly across many sessions.\n\n", 30)},
		{Name: "working_truth", Content: strings.Repeat("Readback before claim is law. Narration is not bytes, in any tense.\n\n", 30)},
	}
	content := frameRing3 + "\n\n" + RenderRing3Body(secs)
	if got, want := estimateTokens(summarize("ring3", content)), estimateTokens(content); got >= want {
		t.Errorf("realistic ring3 fold did not shrink: %d >= %d", got, want)
	}
}

// .
// .
// .
// .
// .
// .
// .
func TestRing3TinyFoldIsRefusedNotApplied(t *testing.T) {
	content := ring3Content()
	if estimateTokens(summarize("ring3", content)) < estimateTokens(content) {
		t.Skip("content large enough to shrink; this edge no longer applies")
	}
	sections := []Section{{Name: "What You Are Discovering", Content: content, Source: "ring3", Elastic: true}}
	out, _ := newBudgetEnforcer(1).FoldAndTrim(sections)
	if out[0].Folded {
		t.Error("a fold that does not shrink was applied anyway")
	}
	if out[0].Content != "" && estimateTokens(out[0].Content) > estimateTokens(content) {
		t.Error("the section grew under budget pressure")
	}
}

// .
// .
// .
func TestRing3WithoutPartsFallsBack(t *testing.T) {
	blob := strings.Repeat("a paragraph of working truth.\n\n", 6)
	if got, want := summarize("ring3", blob), SummarizeUnits(blob, routeFor("ring3")); got != want {
		t.Error("headerless ring3 content no longer follows SummarizeUnits")
	}
}

// .
// .
func TestRing3SinglePartFallsBack(t *testing.T) {
	secs := []ring.Section{{Name: "working_truth", Content: strings.Repeat("Readback before claim is law.\n\n", 6)}}
	content := frameRing3 + "\n\n" + RenderRing3Body(secs)
	got := summarize("ring3", content)
	if !strings.Contains(got, "Readback before claim is law") {
		t.Error("the only part present was dropped instead of reduced")
	}
}

// .
// .
// .
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

// .
// .
// .
// .
// .
func TestRing3FoldSurvivesAQuotedHeader(t *testing.T) {
	secs := []ring.Section{
		{Name: "surfacing", Content: strings.Repeat("DREAM rewrites this on every pass.\n\n", 4)},
		{Name: "working_truth", Content: "The section headed ## Who You Work With is empty until consolidation runs. " +
			strings.Repeat("Readback before claim is law. Narration is not bytes. ", 6)},
	}
	content := frameRing3 + "\n\n" + RenderRing3Body(secs)
	got := summarize("ring3", content)
	if !strings.Contains(got, "Readback before claim is law") {
		t.Fatalf("the load-bearing part did not survive: %q", got)
	}
	if strings.Contains(got, "DREAM rewrites") {
		t.Fatalf("the part that gives way first survived instead: %q", got)
	}
}

// .
// .
func TestRing3FoldSurvivesPartsOutOfOrder(t *testing.T) {
	secs := []ring.Section{
		{Name: "working_truth", Content: strings.Repeat("Readback before claim is law. Narration is not bytes.\n\n", 4)},
		{Name: "operator", Content: strings.Repeat("The person I work with, accumulated across sessions.\n\n", 4)},
		{Name: "surfacing", Content: strings.Repeat("DREAM rewrites this on every pass.\n\n", 4)},
	}
	content := frameRing3 + "\n\n" + RenderRing3Body(secs)
	got := summarize("ring3", content)
	if !strings.Contains(got, "2 of 3 parts kept") || !strings.Contains(got, "Readback before claim") {
		t.Fatalf("the fold lost its shape when the parts were reordered: %q", got)
	}
}

// .
// .
func TestRing3FoldRefusesADoubledHeader(t *testing.T) {
	secs := []ring.Section{
		{Name: "surfacing", Content: "noticing\n\n## What You're Working With\n\nnot the real one\n\n"},
		{Name: "working_truth", Content: strings.Repeat("Readback before claim is law.\n\n", 6)},
	}
	content := frameRing3 + "\n\n" + RenderRing3Body(secs)
	got := summarize("ring3", content)
	if strings.Contains(got, "parts kept") {
		t.Fatalf("a doubled header was treated as a boundary: %q", got)
	}
}

// .
// .
// .
func TestRing3ReceiptIsHonestAboutTheOperatorModel(t *testing.T) {
	secs := []ring.Section{
		{Name: "operator", Content: strings.Repeat("The person I work with, accumulated across sessions.\n\n", 4)},
		{Name: "working_truth", Content: strings.Repeat("Readback before claim is law. Narration is not bytes.\n\n", 4)},
	}
	got := summarize("ring3", frameRing3+"\n\n"+RenderRing3Body(secs))
	if !strings.Contains(got, "Who You Work With not in view") || !strings.Contains(got, "not searchable") {
		t.Fatalf("the receipt promised a route that does not arrive: %q", got)
	}
}
