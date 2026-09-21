package prompt

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// .
// .
func captureLog(t *testing.T) *logsink.Capture {
	t.Helper()
	buf := logsink.CaptureForTest(t)
	return buf
}

// .
// .
// .
// .
func TestFoldTelemetryIsSilentWithoutPressure(t *testing.T) {
	buf := captureLog(t)
	sections := []Section{
		{Name: "Constitution", Content: "identity truth"},
		elastic("Working Truth", "ring3", 100),
	}
	newBudgetEnforcer(budgetTokens(sections, nil) + 1000).FoldAndTrim(sections)

	if len(buf.String()) != 0 {
		t.Fatalf("a prompt that fits emitted telemetry: %q", buf.String())
	}
}

// .
// .
// .
func TestFoldTelemetryNamesWhatYielded(t *testing.T) {
	buf := captureLog(t)
	sections := []Section{
		{Name: "Constitution", Content: "identity truth"},
		elastic("Orientation", "brief", 1000),
		elastic("Working State", "ring4", 100),
		elastic("Working Truth", "ring3", 100),
	}
	want := append([]Section(nil), sections...)
	want[1].Content = summarize("brief", want[1].Content)
	want[1].Folded = true

	newBudgetEnforcer(budgetTokens(want, nil)).FoldAndTrim(sections)

	line := buf.String()
	if !strings.Contains(line, "prompt.budget") {
		t.Fatalf("telemetry is not greppable by subsystem: %q", line)
	}
	for _, field := range []string{"budget=", "in=", "out=", "folded=[", "omitted=["} {
		if !strings.Contains(line, field) {
			t.Fatalf("telemetry missing %q: %q", field, line)
		}
	}
	if !strings.Contains(line, "folded=[brief]") {
		t.Fatalf("telemetry did not name the section that folded: %q", line)
	}
	if strings.Count(line, "\n") != 1 {
		t.Fatalf("one compose under pressure must emit exactly one line: %q", line)
	}
}

// .
// .
// .
// .
func TestFoldTelemetryDistinguishesOmissionFromFold(t *testing.T) {
	buf := captureLog(t)
	identity := Section{Name: "Constitution", Content: "identity truth"}
	sections := []Section{
		identity,
		elastic("Working State", "ring4", 1000),
		elastic("Orientation", "brief", 1000),
		elastic("Working Truth", "ring3", 1000),
	}
	want := []Omission{{"Orientation", "brief"}, {"Working State", "ring4"}, {"Working Truth", "ring3"}}
	newBudgetEnforcer(budgetTokens([]Section{identity}, want)).FoldAndTrim(sections)

	line := buf.String()
	for _, source := range []string{"brief", "ring4", "ring3"} {
		if !strings.Contains(line, source) {
			t.Fatalf("telemetry omitted %q from the record: %q", source, line)
		}
	}
	omitted := line[strings.Index(line, "omitted=["):]
	for _, source := range []string{"brief", "ring4", "ring3"} {
		if !strings.Contains(omitted, source) {
			t.Fatalf("dropped section %q was not recorded as omitted: %q", source, line)
		}
	}
}

// .
// .
// .
// .
// .
// .
func TestFoldTelemetryDoesNotAlterTheFold(t *testing.T) {
	build := func() []Section {
		return []Section{
			{Name: "Constitution", Content: "identity truth"},
			elastic("Orientation", "brief", 1000),
			elastic("Working State", "ring4", 400),
			elastic("Working Truth", "ring3", 400),
		}
	}
	budget := budgetTokens(build(), nil) / 3

	buf := captureLog(t)
	observed, observedOmissions := newBudgetEnforcer(budget).FoldAndTrim(build())
	if len(buf.String()) == 0 {
		t.Fatal("test is vacuous: this budget did not produce pressure")
	}

	// .
	// .
	silent, silentOmissions := newBudgetEnforcer(budget).FoldAndTrim(build())

	if len(observed) != len(silent) {
		t.Fatalf("section count diverged: %d observed, %d silent", len(observed), len(silent))
	}
	for i := range observed {
		if observed[i].Content != silent[i].Content || observed[i].Folded != silent[i].Folded {
			t.Fatalf("section %q folded differently when observed", observed[i].Name)
		}
	}
	if len(observedOmissions) != len(silentOmissions) {
		t.Fatalf("omissions diverged: %v observed, %v silent", observedOmissions, silentOmissions)
	}
}
