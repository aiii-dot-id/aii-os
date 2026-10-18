package prompt

import (
	"strings"
	"testing"
)

func TestTheCharterSentencesSayToWaitForTheReplyBeforeRecording(t *testing.T) {
	for name, s := range map[string]string{"with a charter": ring1Growth, "approved without a charter": ring1Incomplete} {
		for _, want := range []string{"wait for the reply", "once they have affirmed it", "affirmation or negation", "commit relationship.upsert (charter_text)", "stamps their most recent words", "only after they have answered"} {
			if !strings.Contains(s, want) {
				t.Errorf("the sentence %s does not say %q:\n%s", name, want, s)
			}
		}
		for _, wrong := range []string{"propose the change with commit", "propose it with commit"} {
			if strings.Contains(s, wrong) {
				t.Errorf("the sentence %s still names the minting verb as the way to propose (%q):\n%s", name, wrong, s)
			}
		}
	}
	// The operator's own sentence for an identity with no charter stands.
	if !strings.Contains(ring1Reminder, "present a Ring 1 proposal for their affirmation or negation") {
		t.Fatalf("the absent-charter sentence changed: %q", ring1Reminder)
	}
}
