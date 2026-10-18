package publicname

import (
	"net"
	"reflect"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

func fixtureAddrs(t *testing.T, cidrs ...string) {
	t.Helper()
	restore := InterfaceAddrsForTest(func() ([]net.Addr, error) {
		out := make([]net.Addr, 0, len(cidrs))
		for _, c := range cidrs {
			ip, n, err := net.ParseCIDR(c)
			if err != nil {
				t.Fatal(err)
			}
			n.IP = ip
			out = append(out, n)
		}
		return out, nil
	})
	t.Cleanup(restore)
}

// AN ADDRESS LIST IS CHECKED FOR WHAT IT IS, WHATEVER THE ROUTE. The route
// owner never looks at the addresses while the route is a relay or
// disabled, so a bad entry saved then would wait until the day the route
// went back to direct.
func TestDirectAddressesAreHeldToTheirShape(t *testing.T) {
	if err := CheckDirectAddresses([]string{"10.8.0.2", "fd00::2", " 192.0.2.6 "}); err != nil {
		t.Fatalf("addresses of both families, one with spaces around it: %v", err)
	}
	if err := CheckDirectAddresses([]string{}); err != nil {
		t.Fatalf("an empty list is a real answer, an intentionally empty route: %v", err)
	}
	for _, bad := range []string{"vpn.example.test", "10.8.0.2:8180", "10.8.0.0/24", ""} {
		err := CheckDirectAddresses([]string{"10.8.0.2", bad})
		if err == nil || !strings.Contains(err.Error(), "certificate.direct_addresses") {
			t.Fatalf("%q was taken as an address: %v", bad, err)
		}
	}
}

// A CHANGE THAT WOULD LEAVE A DIRECT ROUTE NOTHING TO PUBLISH IS REFUSED,
// WITH THE REASON. Removing the override on a machine with two addresses
// and a dashboard bound to every interface cannot be worked out; accepted,
// the working route would lapse at the end of its lease. While a relay
// carries the name, or the route is disabled, the same removal publishes
// nothing and is taken.
func TestADirectAddressChangeThatCannotBePublishedIsRefused(t *testing.T) {
	fixtureAddrs(t, "10.8.0.2/24", "192.168.1.5/24")
	r, _ := fixtureRuntime(Settings{})
	auto := Settings{Host: "0.0.0.0"}
	err := r.DirectChoiceRefusal(auto)
	if err == nil {
		t.Fatal("automatic was accepted on a machine where it cannot choose between two addresses")
	}
	for _, want := range []string{"10.8.0.2", "192.168.1.5", "direct_addresses"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not say %q: %v", want, err)
		}
	}
	listed := auto
	listed.Certificate.DirectAddresses = &[]string{"10.8.0.2"}
	if err := r.DirectChoiceRefusal(listed); err != nil {
		t.Fatalf("one of the two, named: %v", err)
	}
	for _, mode := range []string{RouteRelay, RouteDisabled} {
		carried := auto
		carried.Certificate.RouteMode = mode
		if err := r.DirectChoiceRefusal(carried); err != nil {
			t.Fatalf("route %s publishes nothing from the addresses, and removing the override was refused: %v", mode, err)
		}
	}
	// One address is its family's answer: automatic works.
	fixtureAddrs(t, "10.8.0.2/24")
	if err := r.DirectChoiceRefusal(auto); err != nil {
		t.Fatalf("automatic on a machine with one address: %v", err)
	}
}

// THE CARD IS TOLD THE OVERRIDE AS IT STANDS AND THE MACHINE'S ADDRESSES TO
// PICK FROM: absent means "work it out", present and empty an intentionally
// empty route, and they must not read the same.
func TestTheCardIsToldTheDirectAddresses(t *testing.T) {
	fixtureAddrs(t, "192.168.1.5/24", "10.8.0.2/24", "127.0.0.1/8", "fe80::1/64", "fd00::2/64")
	r, box := fixtureRuntime(Settings{Host: "0.0.0.0"})
	r.ServeForTest(store.PublicName{Name: fixtureName, Zone: "id.example.test"}, "https://"+fixtureName+":8180", nil)
	st := r.State()
	if st.DirectAddresses != nil {
		t.Fatalf("no override reads as one: %v", *st.DirectAddresses)
	}
	if want := []string{"10.8.0.2", "192.168.1.5", "fd00::2"}; !reflect.DeepEqual(st.MachineAddresses, want) {
		t.Fatalf("the machine's addresses to pick from: %v, want %v (loopback and link-local are not publishable)", st.MachineAddresses, want)
	}
	box.set(func(s *Settings) { s.Certificate.DirectAddresses = &[]string{} })
	if st := r.State(); st.DirectAddresses == nil || len(*st.DirectAddresses) != 0 {
		t.Fatal("an intentionally empty list reads as no override")
	}
	box.set(func(s *Settings) { s.Certificate.DirectAddresses = &[]string{"10.8.0.2"} })
	if st := r.State(); st.DirectAddresses == nil || !reflect.DeepEqual(*st.DirectAddresses, []string{"10.8.0.2"}) {
		t.Fatalf("the override: %v", st.DirectAddresses)
	}
}
