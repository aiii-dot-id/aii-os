package store

import (
	"strings"
	"testing"
)

func TestResumeCardRendersOrderedFieldsVerbatim(t *testing.T) {
	got := RenderResumeCard(ResumeFields{
		Focus:            "ship the beta",
		Established:      "deb installs; rollback failed on read-only /usr",
		NextAction:       "fix the rollback path",
		ExpectedEvidence: "rollback.sh PASS on the deb",
		Falsifier:        "the boot logs ROLLBACK FAILED again",
		DecisionNeeded:   "confirm apt is Ubuntu's update path",
		ResumePoint:      "packaging/deb + internal/updates",
	})
	for _, want := range []string{
		"Focus: ship the beta",
		"Established: deb installs; rollback failed on read-only /usr",
		"Next action: fix the rollback path",
		"Expected evidence: rollback.sh PASS on the deb",
		"Falsifier: the boot logs ROLLBACK FAILED again",
		"Decision needed: confirm apt is Ubuntu's update path",
		"Resume point: packaging/deb + internal/updates",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("card missing %q\n---\n%s", want, got)
		}
	}
	if strings.Index(got, "Focus:") > strings.Index(got, "Next action:") ||
		strings.Index(got, "Next action:") > strings.Index(got, "Falsifier:") {
		t.Fatalf("fields out of the recommended order:\n%s", got)
	}
}

func TestResumeCardMissingEvidenceRendersUnknownNeverAPositiveClaim(t *testing.T) {
	got := RenderResumeCard(ResumeFields{NextAction: "run the drill"})
	if !strings.Contains(got, "Expected evidence: not specified") {
		t.Fatalf("missing evidence must render 'not specified':\n%s", got)
	}
	if !strings.Contains(got, "Falsifier: not specified") {
		t.Fatalf("missing falsifier must render 'not specified':\n%s", got)
	}
	low := strings.ToLower(got)
	if strings.Contains(low, "verified") || strings.Contains(low, "complete") {
		t.Fatalf("card must not imply verification/completion:\n%s", got)
	}
}

func TestResumeCardAbsentFieldsStayQuiet(t *testing.T) {
	got := RenderResumeCard(ResumeFields{Focus: "x", NextAction: "y", ExpectedEvidence: "z", Falsifier: "w"})
	if strings.Contains(got, "Established:") || strings.Contains(got, "Decision needed:") || strings.Contains(got, "Resume point:") {
		t.Fatalf("absent fields must not render:\n%s", got)
	}
}

func TestResumeCardNoSubstantiveWorkRendersNothing(t *testing.T) {
	if got := RenderResumeCard(ResumeFields{ExpectedEvidence: "x", Falsifier: "y"}); got != "" {
		t.Fatalf("expected empty card for evidence-only, got:\n%s", got)
	}
	if got := RenderResumeCard(ResumeFields{}); got != "" {
		t.Fatalf("expected empty card for empty fields, got:\n%s", got)
	}
}

func TestWorkEvidenceFieldsRoundTripAndFeedTheCard(t *testing.T) {
	s, err := NewMemory()
	if err != nil {
		t.Fatalf("NewMemory: %v", err)
	}
	defer s.Close()
	if err := s.StartWorkSession("ev-1", "evidence round trip"); err != nil {
		t.Fatalf("start: %v", err)
	}
	sp := func(v string) *string { return &v }
	if err := s.UpdateWorkPlan("ev-1", sp("land P0"), sp("wire the card"), sp("## plan"),
		sp("the card renders once"), sp("two return surfaces render"), sp("confirm apt is the ubuntu path")); err != nil {
		t.Fatalf("UpdateWorkPlan: %v", err)
	}
	ws, err := s.ActiveWorkSession()
	if err != nil || ws == nil {
		t.Fatalf("ActiveWorkSession: %v ws=%v", err, ws)
	}
	if ws.ExpectedEvidence != "the card renders once" || ws.Falsifier != "two return surfaces render" || ws.DecisionNeeded != "confirm apt is the ubuntu path" {
		t.Fatalf("evidence fields not read back: %+v", ws)
	}
	// .
	if err := s.UpdateWorkPlan("ev-1", nil, nil, nil, nil, sp("changed falsifier"), nil); err != nil {
		t.Fatalf("update falsifier: %v", err)
	}
	ws, _ = s.ActiveWorkSession()
	if ws.Falsifier != "changed falsifier" || ws.ExpectedEvidence != "the card renders once" {
		t.Fatalf("set-only semantics broke: %+v", ws)
	}
	card := RenderResumeCard(ResumeFields{
		Focus: ws.Focus, Established: ws.State, NextAction: ws.NextMove,
		ExpectedEvidence: ws.ExpectedEvidence, Falsifier: ws.Falsifier,
		DecisionNeeded: ws.DecisionNeeded, ResumePoint: ws.Plan,
	})
	for _, want := range []string{"Expected evidence: the card renders once", "Falsifier: changed falsifier", "Decision needed: confirm apt is the ubuntu path"} {
		if !strings.Contains(card, want) {
			t.Fatalf("card missing %q:\n%s", want, card)
		}
	}
}
