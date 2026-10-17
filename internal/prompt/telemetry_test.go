package prompt

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// captureLog redirects the standard logger — the one logsink tees to
// disk — for the duration of one test.
func captureLog(t *testing.T) *logsink.Capture {
	t.Helper()
	buf := logsink.CaptureForTest(t)
	return buf
}

// A prompt that FITS must emit nothing. Silence is the measurement:
// every line in the log is a compose that was actually under pressure,
// so "does folding ever fire?" is answered by the presence of lines
// rather than by reading a ratio out of noise.
func TestFoldTelemetryIsSilentWithoutPressure(t *testing.T) {
	buf := captureLog(t)
	sections := []Section{
		{Name: "Constitution", Content: "identity truth"},
		elastic("Working Truth", "ring3", 100),
	}
	newBudgetEnforcer((&budgetEnforcer{}).budgetTokens(sections, nil)+1000, nil).FoldAndTrim(sections)

	if len(buf.String()) != 0 {
		t.Fatalf("a prompt that fits emitted telemetry: %q", buf.String())
	}
}

// Under real pressure the line must name WHAT yielded, not merely that
// something did. A bare count would reproduce the defect this arc
// started from: a receipt that reports loss without naming it.
func TestFoldTelemetryNamesWhatYielded(t *testing.T) {
	buf := captureLog(t)
	sections := []Section{
		{Name: "Constitution", Content: "identity truth"},
		elastic("Orientation", "brief", 1000),
		elastic("Working State", "ring4", 100),
		elastic("Working Truth", "ring3", 100),
	}
	want := append([]Section(nil), sections...)
	want[1].Content = summarize("brief", want[1].Content, budgetRoute)
	want[1].Folded = true

	newBudgetEnforcer((&budgetEnforcer{}).budgetTokens(want, nil), nil).FoldAndTrim(sections)

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

// An omitted section must be named too, and separately from a folded
// one: folding keeps a digest in view, omission removes the content
// entirely. Collapsing them would make the two outcomes indistinguishable
// in the record, which is the thing being measured.
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
	newBudgetEnforcer((&budgetEnforcer{}).budgetTokens([]Section{identity}, want), nil).FoldAndTrim(sections)

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

// Measurement must not be control. Observing pressure may not change what
// pressure does — so the same input must fold identically whether or not
// anyone is reading the log. This is the do-not-copy lesson pinned as a
// test rather than trusted as an intention: the C usefulness scorer
// failed precisely because its record had an arithmetic path back into
// the decision.
func TestFoldTelemetryDoesNotAlterTheFold(t *testing.T) {
	build := func() []Section {
		return []Section{
			{Name: "Constitution", Content: "identity truth"},
			elastic("Orientation", "brief", 1000),
			elastic("Working State", "ring4", 400),
			elastic("Working Truth", "ring3", 400),
		}
	}
	budget := (&budgetEnforcer{}).budgetTokens(build(), nil) / 3

	buf := captureLog(t)
	observed, observedOmissions := newBudgetEnforcer(budget, nil).FoldAndTrim(build())
	if len(buf.String()) == 0 {
		t.Fatal("test is vacuous: this budget did not produce pressure")
	}

	// Same input, same budget. CaptureForTest already has the logger,
	// so this half runs with nothing reaching a console either way.
	silent, silentOmissions := newBudgetEnforcer(budget, nil).FoldAndTrim(build())

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
