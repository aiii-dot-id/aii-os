package identity

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
)

// THE ROLE SENTENCE. Names sorted; each description on one line, every
// run of white space (any line break among it) one space; one final full
// stop dropped so the list reads as one sentence; a role with no description
// named alone; no role, the sentence as it always was. VerbsAt gives the
// sentence to the work verb in a copy and never to the registry.
func TestTheRoleSentenceFoldsAndSorts(t *testing.T) {
	got := roleDescription(map[string]string{
		"zeta":  "",
		"alpha": "first line\r\nsecond line\rthird\u2028fourth\u0085fifth.",
		"mid":   "  one\n\ntwo  ",
	})
	const want = `(spawn, optional) named inference route in agency.roles. Your operator has configured: "alpha", first line second line third fourth fifth; "mid", one two; "zeta". An unrouted name falls back to your active model.`
	if got != want {
		t.Fatalf("role sentence:\n got %s\nwant %s", got, want)
	}
	if got := roleDescription(nil); got != roleUnrouted {
		t.Fatalf("no roles: %s", got)
	}
	at := VerbsAt(conversation.Checkpoint{}, map[string]string{"critic": "reads drafts"})
	if s := roleOf(t, at); s == roleUnrouted {
		t.Fatal("VerbsAt did not write the roles into the role sentence")
	}
	if s := roleOf(t, Verbs()); s != roleUnrouted {
		t.Fatalf("VerbsAt wrote the roles into the registry: %s", s)
	}
}

func roleOf(t *testing.T, verbs []Verb) string {
	t.Helper()
	for _, v := range verbs {
		if v.Name == "work" {
			return v.Params["properties"].(map[string]any)["role"].(map[string]any)["description"].(string)
		}
	}
	t.Fatal("no work verb")
	return ""
}
