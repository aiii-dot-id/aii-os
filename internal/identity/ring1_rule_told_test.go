package identity

import (
	"strings"
	"testing"
)

// AN IDENTITY IS TOLD WHOSE WORD RING 1 CITES BEFORE IT IS REFUSED FOR IT.
// The rule for a spoken affirmation was met only in a refusal: a resident
// that had never tried could not know that a voice in the room is not its
// operator's word. The commit verb's description carries the rule, and
// the rule says each thing the store holds a spoken turn to: the primary
// human operator, a typed word, an identified and enrolled voice, the
// UUID they named, and that no earlier word is cited in its place. It
// describes; it does not direct.
func TestTheCommitVerbSaysWhoseWordRing1Cites(t *testing.T) {
	var commit string
	for _, v := range verbRegistry {
		if v.Name == "commit" {
			commit = v.Description
		}
	}
	if n := strings.Count(commit, OperatorWordRule); n != 1 {
		t.Fatalf("the commit verb's description carries the rule for the operator's word %d times, want once: %q", n, commit)
	}
	for _, said := range []string{
		"relationship.upsert", "primary human operator", "you do not choose it",
		"A word they typed stands", "identified its speaker as them", "enrolled voice",
		"speaker UUID they named as their own", "another identity's", "no earlier word is cited",
	} {
		if !strings.Contains(OperatorWordRule, said) {
			t.Errorf("the rule does not say %q: %q", said, OperatorWordRule)
		}
	}
	for _, directive := range []string{"you should", "you must", "remember to", "ask them"} {
		if strings.Contains(strings.ToLower(OperatorWordRule), directive) {
			t.Errorf("the rule directs the resident (%q): %q", directive, OperatorWordRule)
		}
	}
}
