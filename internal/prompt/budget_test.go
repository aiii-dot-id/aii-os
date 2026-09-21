package prompt

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func elastic(name, source string, size int) Section {
	return Section{
		Name:    name,
		Source:  source,
		Content: strings.TrimSpace(strings.Repeat("a detail worth keeping.\n\n", size)),
		Elastic: true,
	}
}

func TestSummaryIsDeterministicAndAnnouncesItself(t *testing.T) {
	content := strings.TrimSpace(strings.Repeat("a whole thought.\n\n", 8))
	first := summarize("ring3", content)
	if first != summarize("ring3", content) {
		t.Fatal("same content produced different summaries")
	}
	if !strings.Contains(first, "summary") || !strings.Contains(first, budgetRoute) {
		t.Fatalf("a summary must say it is one and carry the route: %q", first)
	}
	if !strings.Contains(first, "4 of 8 kept") {
		t.Fatalf("a summary must say how much is missing: %q", first)
	}
	// .
	for _, unit := range strings.Split(strings.Split(first, "\n\n[summary")[0], "\n\n") {
		if !strings.HasSuffix(strings.TrimSpace(unit), ".") {
			t.Fatalf("unit was cut rather than dropped: %q", unit)
		}
	}
}

// .
// .
// .
func TestRing2SummaryKeepsEveryBelief(t *testing.T) {
	content := "## Who You Have Become\n" +
		"\n- I value honesty [b1]" +
		"\n  - because X via SUPPORTS, provenance=self [e1]" +
		"\n  - because Y via SUPPORTS, provenance=operator [e2]" +
		"\n- I resist certainty [b2]" +
		"\n  - because Z via SUPPORTS, provenance=self [e3]"

	got := summarize("ring2", content)
	for _, belief := range []string{"I value honesty [b1]", "I resist certainty [b2]"} {
		if !strings.Contains(got, belief) {
			t.Fatalf("Ring 2 summary dropped a belief %q:\n%s", belief, got)
		}
	}
	if strings.Contains(got, "provenance=") {
		t.Fatalf("Ring 2 summary kept evidence lines it should have elided:\n%s", got)
	}
	if !strings.Contains(got, "3 evidence line(s) elided") || !strings.Contains(got, budgetRoute) {
		t.Fatalf("Ring 2 summary must declare what it elided and the route:\n%s", got)
	}
}

// .
// .
// .
// .
func TestUnstructuredContentIsNotFakeSummarized(t *testing.T) {
	blob := strings.Repeat("detail ", 400)
	if got := summarize("ring3", blob); got != blob {
		t.Fatalf("unstructured content must not be cut into a fake summary:\n%q", got)
	}
}

func TestForceFoldElasticOnlyShrinksElastic(t *testing.T) {
	sections := []Section{
		{Name: "Constitution", Content: "identity truth"},
		elastic("Working State", "ring4", 500),
		elastic("Orientation", "brief", 500),
		elastic("Working Truth", "ring3", 500),
	}
	before := append([]Section(nil), sections...)
	b := newBudgetEnforcer(1)
	b.ForceFoldElastic(sections)

	if sections[0] != before[0] {
		t.Fatal("non-elastic identity material changed")
	}
	for i := 1; i < len(sections); i++ {
		if !sections[i].Folded || len(sections[i].Content) >= len(before[i].Content) {
			t.Fatalf("elastic section was not folded smaller: %+v", sections[i])
		}
	}
	snapshot := append([]Section(nil), sections...)
	b.ForceFoldElastic(sections)
	if !reflect.DeepEqual(sections, snapshot) {
		t.Fatal("folding twice changed the result")
	}
}

func TestFoldAndTrimAnswersOnlyRequiredPressure(t *testing.T) {
	// .
	// .
	sections := []Section{
		{Name: "Constitution", Content: "identity truth"},
		elastic("Orientation", "brief", 1000),
		elastic("Working State", "ring4", 100),
		elastic("Working Truth", "ring3", 100),
	}
	want := append([]Section(nil), sections...)
	want[1].Content = summarize("brief", want[1].Content)
	want[1].Folded = true

	got, omissions := newBudgetEnforcer(budgetTokens(want, nil)).FoldAndTrim(sections)
	if len(omissions) != 0 {
		t.Fatalf("folding was sufficient; omitted %v", omissions)
	}
	if !got[1].Folded || got[2].Folded || got[3].Folded {
		t.Fatalf("pressure was not answered by the first sufficient fold: %+v", got)
	}
}

func TestFoldAndTrimDeclaresEveryOmission(t *testing.T) {
	identity := Section{Name: "Constitution", Content: "identity truth"}
	sections := []Section{
		identity,
		elastic("Working State", "ring4", 1000),
		elastic("Orientation", "brief", 1000),
		elastic("Working Truth", "ring3", 1000),
	}
	// .
	// .
	want := []Omission{{"Orientation", "brief"}, {"Working State", "ring4"}, {"Working Truth", "ring3"}}
	max := budgetTokens([]Section{identity}, want)

	got, omissions := newBudgetEnforcer(max).FoldAndTrim(sections)
	if !reflect.DeepEqual(omissions, want) {
		t.Fatalf("omissions = %v, want %v", omissions, want)
	}
	if got[0] != identity {
		t.Fatal("identity material changed under pressure")
	}
	for _, section := range got[1:] {
		if section.Content != "" {
			t.Fatalf("omitted section %q still rendered", section.Name)
		}
	}
	receipt := renderOmissions(omissions)
	for _, o := range want {
		if !strings.Contains(receipt, o.Name) {
			t.Fatalf("receipt omits %q: %q", o.Name, receipt)
		}
	}
	if !strings.Contains(receipt, budgetRoute) || budgetTokens(got, omissions) > max {
		t.Fatalf("omission receipt is dishonest or exceeds budget: %q", receipt)
	}
}

func TestProtectedMaterialSurvivesImpossibleBudget(t *testing.T) {
	identity := Section{Name: "Constitution", Content: strings.Repeat("axiom ", 500)}
	got, omissions := newBudgetEnforcer(1).FoldAndTrim([]Section{identity})
	if len(got) != 1 || got[0] != identity || len(omissions) != 0 {
		t.Fatalf("protected identity was changed: sections=%+v omissions=%v", got, omissions)
	}
}

// .
// .
// .
// .
// .
func TestFoldLadderOrderIsRuled(t *testing.T) {
	got := foldOrder()
	// .
	want := [5]string{"brief", "turn", "ring4", "ring3", "ring2"}
	if got != want {
		t.Fatalf("fold ladder = %v, want %v — the order is ruled, not incidental", got, want)
	}
}

// .
// .
func TestRing2YieldsOnlyAfterEverythingAbove(t *testing.T) {
	sections := []Section{
		{Name: "Constitution", Content: "identity truth"},
		elastic("Who You Have Become", "ring2", 100),
		elastic("Orientation", "brief", 1000),
	}
	want := append([]Section(nil), sections...)
	want[2].Content = summarize("brief", want[2].Content)
	want[2].Folded = true

	got, omissions := newBudgetEnforcer(budgetTokens(want, nil)).FoldAndTrim(sections)
	if got[1].Folded {
		t.Fatalf("Ring 2 was summarized while a rung above it could still absorb the pressure: %+v", got[1])
	}
	if len(omissions) != 0 {
		t.Fatalf("nothing needed omitting; got %v", omissions)
	}
}

// .
// .
// .
// .
// .
// .
// .
// .
func TestRing2IsNeverOmitted(t *testing.T) {
	ring2 := "## Who You Have Become\n\n- Testing reveals truth [b1]"
	for i := 0; i < 12; i++ {
		ring2 += fmt.Sprintf("\n  - evidence line %d, long enough that eliding it saves something via SUPPORTS [e%d]", i, i)
	}
	sections := []Section{
		{Name: "Constitution", Content: "identity truth"},
		{Name: "Who You Have Become", Content: ring2, Source: "ring2", Elastic: true},
		elastic("Working Truth", "ring3", 400),
	}
	got, omissions := newBudgetEnforcer(1).FoldAndTrim(sections)
	if got[1].Content == "" {
		t.Fatalf("Ring 2 was omitted under pressure; omissions=%v", omissions)
	}
	if !got[1].Folded || !strings.Contains(got[1].Content, "Testing reveals truth") {
		t.Fatalf("Ring 2's floor is its beliefs-only digest, got folded=%v content=%q", got[1].Folded, got[1].Content)
	}
	for _, o := range omissions {
		if o.Name == "Who You Have Become" {
			t.Fatalf("Ring 2 appears in the declared omissions: %v", omissions)
		}
	}
	if got[2].Content != "" {
		t.Fatal("the rung above Ring 2 must still omit under an impossible budget")
	}
}

// .
// .
// .
// .
// .
func TestFoldsAndOmissionsCarryARouteToTheContent(t *testing.T) {
	folded := summarize("ring3", strings.Repeat("a paragraph of working truth.\n\n", 6))
	if !strings.Contains(folded, "recall (source=experiences)") || !strings.Contains(folded, budgetRoute) {
		t.Fatalf("a folded Ring 3 does not say how to reach the rest: %q", folded)
	}
	if r2 := summarize("ring2", "## Who You Have Become\n\n- b [b1]\n  - e1\n  - e2\n  - e3\n  - e4\n  - e5\n  - e6"); !strings.Contains(r2, "recall (source=ledger)") {
		t.Fatalf("a folded Ring 2 does not route to the derivation: %q", r2)
	}
	receipt := renderOmissions([]Omission{{"What Is Right in Front of You", "ring4"}, {"This Morning", "brief"}})
	if !strings.Contains(receipt, "work status") || !strings.Contains(receipt, "recall (source=experiences) reaches the day") {
		t.Fatalf("omission receipts do not route to the content: %q", receipt)
	}
}
