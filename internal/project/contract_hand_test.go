package project

import (
	"testing"
)

// "Who agreed it, as the contract records" (the operator, 2026-10-31:
// record the hand). The manager writes the hand and the time with every
// change of a contract's words: the operator's edit says operator, the
// identity's says identity, a creation with a contract says its creator;
// the same words written again keep the hand that agreed them; a caller
// that is neither hand is refused; and the recorder is told the hand. A
// project written before the field has an empty hand and says nothing.
func TestAContractRecordsTheHandThatAgreedIt(t *testing.T) {
	m := NewManager(t.TempDir())
	var told []string
	m.SetContractRecorder(func(id, by string, before *Contract, after Contract) error {
		told = append(told, by+":"+after.Outcome)
		return nil
	})
	p, err := m.Create("Alpha", "", "operator", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Contract.AgreedBy != "" || p.Contract.AgreedAt != "" {
		t.Fatalf("a project with no contract must have no hand, got %q at %q", p.Contract.AgreedBy, p.Contract.AgreedAt)
	}

	first := &Contract{Outcome: "the installer works", Acceptance: []string{"a clean box installs"}}
	if p, err = m.ApplyPatch(p.ID, "operator", nil, nil, nil, nil, first, nil); err != nil {
		t.Fatal(err)
	}
	if p.Contract.AgreedBy != "operator" || p.Contract.AgreedAt == "" {
		t.Fatalf("the operator's contract must say operator and when, got %q at %q", p.Contract.AgreedBy, p.Contract.AgreedAt)
	}
	at := p.Contract.AgreedAt

	// The same words, written again by the other hand: nothing was agreed anew.
	same := &Contract{Outcome: first.Outcome, Acceptance: append([]string(nil), first.Acceptance...)}
	if p, err = m.Update(p.ID, "identity", "", "", "", nil, same, nil); err != nil {
		t.Fatal(err)
	}
	if p.Contract.AgreedBy != "operator" || p.Contract.AgreedAt != at {
		t.Fatalf("unchanged words must keep the hand that agreed them, got %q at %q", p.Contract.AgreedBy, p.Contract.AgreedAt)
	}

	// Changed words by the identity: the identity agreed them.
	changed := &Contract{Outcome: "the installer works on three platforms"}
	if p, err = m.Update(p.ID, "identity", "", "", "", nil, changed, nil); err != nil {
		t.Fatal(err)
	}
	if p.Contract.AgreedBy != "identity" {
		t.Fatalf("the identity's contract must say identity, got %q", p.Contract.AgreedBy)
	}

	// Two lists a joined string would fold together are different words:
	// the identity's change from ["one\x00two"] to ["one", "two"] is a change.
	if p, err = m.Update(p.ID, "operator", "", "", "", nil, &Contract{Outcome: changed.Outcome, Acceptance: []string{"one\x00two"}}, nil); err != nil {
		t.Fatal(err)
	}
	if p, err = m.Update(p.ID, "identity", "", "", "", nil, &Contract{Outcome: changed.Outcome, Acceptance: []string{"one", "two"}}, nil); err != nil {
		t.Fatal(err)
	}
	if p.Contract.AgreedBy != "identity" {
		t.Fatalf("a list split differently is a change; the hand must be the identity's, got %q", p.Contract.AgreedBy)
	}
	if p, err = m.Update(p.ID, "identity", "", "", "", nil, changed, nil); err != nil {
		t.Fatal(err)
	}

	// The hand survives a reload from disk.
	got, err := NewManager(m.root).Load(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Contract.AgreedBy != "identity" || got.Contract.AgreedAt == "" {
		t.Fatalf("the hand did not survive a reload: %q at %q", got.Contract.AgreedBy, got.Contract.AgreedAt)
	}

	// A caller that is neither hand is refused, and the contract stands.
	if _, err := m.ApplyPatch(p.ID, "someone", nil, nil, nil, nil, &Contract{Outcome: "x"}, nil); err == nil {
		t.Fatal("a contract written by neither hand must be refused")
	}
	// A patch without a contract needs no hand.
	if _, err := m.ApplyPatch(p.ID, "", nil, nil, strPtrIfSet("a focus"), nil, nil, nil); err != nil {
		t.Fatalf("a patch with no contract must not ask for a hand: %v", err)
	}

	// The recorder is told, at every contract write as before, the hand the
	// contract records: none at a creation without one, the operator's for
	// its words and for their repeat, the identity's for the changed words.
	want := ":|operator:the installer works|operator:the installer works|identity:the installer works on three platforms|operator:the installer works on three platforms|identity:the installer works on three platforms|identity:the installer works on three platforms"
	if joined := join(told); joined != want {
		t.Fatalf("the recorder heard %q, want %q", joined, want)
	}

	// A creation with a contract is agreed by its creator.
	q, err := m.Create("Beta", "", "identity", nil, &Contract{Outcome: "a beta"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if q.Contract.AgreedBy != "identity" || q.Contract.AgreedAt == "" {
		t.Fatalf("a contract given at creation must be agreed by the creator, got %q at %q", q.Contract.AgreedBy, q.Contract.AgreedAt)
	}
}

func join(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += "|"
		}
		out += s
	}
	return out
}
