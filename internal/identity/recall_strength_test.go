package identity

import (
	"regexp"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// THE LISTING SHOWS A BELIEF'S STRENGTH WITHOUT REINFORCING IT (a belief
// could not be read without being strengthened): `recall
// source=beliefs query=""` shows each belief's strength as the attention
// pass judges it, marks the fading ones, and records no access.
func TestTheBeliefListingShowsStrengthWithoutReinforcing(t *testing.T) {
	engine, st, _, _, _ := setupEngine(t)
	seedBelief(t, st, 1, "the lighthouse on the cape is manned")
	before, _, err := st.MemoryAccessOf(store.MemoryRef{Store: "beliefs", ID: "b01"})
	if err != nil {
		t.Fatal(err)
	}
	out := recallOut(t, engine, map[string]any{"source": "beliefs", "query": ""})
	line := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "[b01, seq 1,") {
			line = l
		}
	}
	if !regexp.MustCompile(`\[b01, seq 1, [^\]]*ring 3, evidence 0, strength [01]\.[0-9]{2}(, fading)?\] the lighthouse`).MatchString(line) {
		t.Fatalf("the listing's line does not show the belief's strength: %q\n%s", line, out)
	}
	after, _, err := st.MemoryAccessOf(store.MemoryRef{Store: "beliefs", ID: "b01"})
	if err != nil {
		t.Fatal(err)
	}
	if after.Count != before.Count {
		t.Fatalf("the listing reinforced the belief: accesses %d → %d", before.Count, after.Count)
	}
}
