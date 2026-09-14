package store

import (
	"path/filepath"
	"strings"
	"testing"
)

// .
// .
// .
func TestSkillProposalGroundsItsCitations(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.StartWorkSession("ws_real1234", "the trajectory that taught it"); err != nil {
		t.Fatal(err)
	}

	// .
	if _, err := s.ProposeSkill("yield early", "delta text", "learned in ws_real1234 and ws_deadbeef99"); err == nil {
		t.Fatal("a proposal citing a phantom session was accepted")
	} else if !strings.Contains(err.Error(), "ws_deadbeef99") {
		t.Fatalf("the refusal does not name the phantom: %v", err)
	}

	// .
	id, err := s.ProposeSkill("yield early", "delta text", "learned in ws_real1234")
	if err != nil {
		t.Fatal(err)
	}
	props, err := s.ListSkillProposals(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(props) != 1 || props[0].ID != id || props[0].Status != "proposed" || props[0].Verified != "none" {
		t.Fatalf("proposal row = %+v — want proposed/none", props)
	}

	// .
	if err := s.DecideSkillProposal(id, "shipped"); err == nil {
		t.Fatal("an untyped decision status was accepted")
	}
	// .
	// .
	// .
	// .
	if err := s.DecideSkillProposal(id, "promoted"); err == nil {
		t.Fatal("an unverified proposal was promoted — the row would claim doctrine with verified=none")
	} else if !strings.Contains(err.Error(), "verified=none") {
		t.Errorf("the refusal does not name what is missing: %v", err)
	}
	// .
	if err := s.DecideSkillProposal(id, "rejected"); err != nil {
		t.Fatalf("rejecting a proposal requires no verifier and must still work: %v", err)
	}
	if err := s.DecideSkillProposal(id, "rejected"); err == nil {
		t.Fatal("a decided proposal was re-decided")
	}

	// .
	if _, err := s.ProposeSkill("t", "d", ""); err == nil {
		t.Fatal("a proposal without evidence was accepted")
	}
}

// .
// .
// .
func TestARefusedPromotionChangesNothing(t *testing.T) {
	s := testStore(t)
	id, err := s.ProposeSkill("title", "delta", "ws_evidence")
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.ListSkillProposals(10)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DecideSkillProposal(id, "promoted"); err == nil {
		t.Fatal("promotion succeeded while verified=none")
	}
	after, err := s.ListSkillProposals(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("the refusal changed the row count: %d -> %d", len(before), len(after))
	}
	if after[0].Status != "proposed" {
		t.Errorf("status = %q after a refused promotion, want proposed", after[0].Status)
	}
	if after[0].Verified != "none" {
		t.Errorf("verified = %q — a refused promotion must not imply verification", after[0].Verified)
	}
}
