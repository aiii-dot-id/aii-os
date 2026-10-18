package publicname

import (
	"fmt"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/certs"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func relaySet(names ...string) []certs.RelayEndpoint {
	out := make([]certs.RelayEndpoint, 0, len(names))
	for _, n := range names {
		out = append(out, certs.RelayEndpoint{Name: n, Port: 443})
	}
	return out
}

// THE RELAY AN IDENTITY IS OFFERED IS ITS OWN, NOT THE LIST'S FIRST. The
// one-press panel took the first relay the service advertised: every
// identity chose the same one, and a service that listed its relays in
// another order moved all of them. The suggestion is a rank of the
// identity's name against each relay's, so it is the same in any order,
// differs between identities, and a relay added later takes only the
// identities that rank it first.
func TestTheSuggestedRelayIsTheIdentitysOwnInAnyOrder(t *testing.T) {
	a, b, c := "a.relay.example.test", "b.relay.example.test", "c.relay.example.test"
	chosen := map[string]int{}
	for i := 0; i < 200; i++ {
		name := fmt.Sprintf("identity%03d.id.example.test", i)
		first, ok := suggestedRelay(name, "", relaySet(a, b, c))
		if !ok {
			t.Fatalf("%s: three relays offered and none suggested", name)
		}
		for _, order := range [][]string{{a, c, b}, {b, a, c}, {b, c, a}, {c, a, b}, {c, b, a}} {
			if again, _ := suggestedRelay(name, "", relaySet(order...)); again.Name != first.Name {
				t.Fatalf("%s: suggested %s, and %s when the same relays were listed as %v", name, first.Name, again.Name, order)
			}
		}
		chosen[first.Name]++
		// A fourth relay moves this identity to itself or not at all.
		if with, _ := suggestedRelay(name, "", relaySet(a, b, c, "d.relay.example.test")); with.Name != first.Name && with.Name != "d.relay.example.test" {
			t.Fatalf("%s: a new relay moved it from %s to %s, a relay that was there before", name, first.Name, with.Name)
		}
	}
	// Two hundred identities over three relays: none is left empty, and none
	// takes them all. (A fair rank gives each about sixty-seven; forty is
	// far outside what chance does, and well inside what "the first" did.)
	for _, r := range []string{a, b, c} {
		if chosen[r] < 40 {
			t.Fatalf("the identities are not spread across the relays: %v", chosen)
		}
	}
	if _, ok := suggestedRelay("x.id.example.test", "", nil); ok {
		t.Fatal("a relay was suggested from an empty list")
	}
}

// A SAVED CHOICE IS NEVER REPLACED WHILE THE SERVICE STILL OFFERS IT. The
// operator's choice, or an earlier suggestion that was saved, stands; one
// the service has stopped advertising can carry nothing, and the rank
// answers again.
func TestASavedRelayChoiceStandsWhileItIsOffered(t *testing.T) {
	a, b := "a.relay.example.test", "b.relay.example.test"
	name := fixtureName
	ranked, _ := suggestedRelay(name, "", relaySet(a, b))
	other := a
	if ranked.Name == a {
		other = b
	}
	if got, _ := suggestedRelay(name, other, relaySet(a, b)); got.Name != other {
		t.Fatalf("the saved choice %s was replaced by %s", other, got.Name)
	}
	if got, _ := suggestedRelay(name, "gone.relay.example.test", relaySet(a, b)); got.Name != ranked.Name {
		t.Fatalf("a saved choice the service no longer offers gave %s, want the ranked %s", got.Name, ranked.Name)
	}
}

// THE CARD IS TOLD WHICH ONE, AND ONLY ONE. The page chooses nothing: it
// saves the relay the host marked.
func TestTheStateMarksOneSuggestedRelay(t *testing.T) {
	r := New(Wires{Settings: func() Settings { return relayCfg("b.relay.example.test:443") }})
	service := &certs.ServiceStatus{
		Capabilities:   []string{certs.CapabilityRelayV2},
		RecordSetTypes: []string{certs.RecordCNAME},
		RelayEndpoints: relaySet("a.relay.example.test", "b.relay.example.test", "c.relay.example.test"),
	}
	r.ServeForTest(store.PublicName{Name: fixtureName, Zone: "id.example.test"}, "https://"+fixtureName+":8180", service)
	st := r.State()
	if st.Relays == nil || len(*st.Relays) != 3 {
		t.Fatalf("the card was not told the three relays: %+v", st.Relays)
	}
	marked := []string{}
	for _, c := range *st.Relays {
		if c.Suggested {
			marked = append(marked, c.Name)
		}
	}
	if len(marked) != 1 || marked[0] != "b.relay.example.test" {
		t.Fatalf("the relay the configuration names is not the one marked: %v", marked)
	}
}
