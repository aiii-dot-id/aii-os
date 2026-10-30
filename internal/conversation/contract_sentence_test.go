package conversation

import (
	"strings"
	"testing"
)

// THE CONTRACT'S LAST SENTENCE IS THE ONLY ONE SAFE REPLACES. Out of SAFE
// the closing is what it was: the contract ends with the sentence written
// out here, once, and the platform note follows it. SystemAdditionsSaying
// puts the caller's sentence in that place and moves no other byte.
func TestTheContractsLastSentenceIsTheOnlyOneSafeReplaces(t *testing.T) {
	const upkeep = "It is written fresh each turn from what you have recorded and is not\nkept in your history: change what it shows with your tools, and note what should\noutlast the turn."
	ordinary := SystemAdditions()
	if strings.Count(ordinary, upkeep) != 1 || !strings.Contains(ordinary, "of the conversation is out\nof view. "+upkeep+"\n\nYou are running on ") {
		t.Fatalf("the ordinary closing no longer ends its contract with the sentence as it was:\n%s", ordinary)
	}
	if got := SystemAdditionsSaying(upkeep); got != ordinary {
		t.Fatalf("given the ordinary sentence the closing is not the ordinary closing:\n got %q\nwant %q", got, ordinary)
	}
	const safe = "SAFE's own sentence."
	if got, want := SystemAdditionsSaying(safe), strings.Replace(ordinary, upkeep, safe, 1); got != want {
		t.Fatalf("SAFE's closing differs from the ordinary one beyond its last sentence:\n got %q\nwant %q", got, want)
	}
}
