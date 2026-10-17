//go:build linux && !android

package anet

import (
	"net"
	"sort"
	"strings"
	"testing"
)

// THE READING ANDROID TAKES AGREES WITH THE SYSTEM'S OWN. On Android 11 and
// later the exported functions read the interfaces through interfaceTable
// and interfaceAddrTable: an address dump asked of netlink on a socket that
// is never bound. Linux answers the same request, so here that reading is
// held to the standard library's on the same machine: the same interfaces
// that have an address, each with the same index, name, MTU, flags and
// addresses.
//
// A machine's interfaces change under a test (a container starts), so the
// system is read before and after and the comparison is made only when the
// two agree.
func TestTheNetlinkReadingAgreesWithTheSystemsOwn(t *testing.T) {
	for attempt := 0; attempt < 20; attempt++ {
		before := systems(t)
		table, err := interfaceTable(0)
		if err != nil {
			t.Fatal(err)
		}
		got := described(t, table, func(i *net.Interface) ([]net.Addr, error) { return interfaceAddrTable(i) })
		if after := systems(t); strings.Join(before, "\n") != strings.Join(after, "\n") {
			continue
		}
		if len(before) == 0 {
			t.Fatal("the system names no interface with an address: there is nothing to compare")
		}
		if strings.Join(got, "\n") != strings.Join(before, "\n") {
			t.Fatalf("the netlink reading differs from the system's own\nnetlink:\n  %s\nsystem:\n  %s", strings.Join(got, "\n  "), strings.Join(before, "\n  "))
		}

		// Every address at once is the same set as every interface's in turn.
		all, err := interfaceAddrTable(nil)
		if err != nil {
			t.Fatal(err)
		}
		own, err := net.InterfaceAddrs()
		if err != nil {
			t.Fatal(err)
		}
		list := func(addrs []net.Addr) string {
			var said []string
			for _, a := range addrs {
				said = append(said, a.String())
			}
			sort.Strings(said)
			return strings.Join(said, " ")
		}
		if again := systems(t); strings.Join(again, "\n") != strings.Join(before, "\n") {
			continue
		}
		if list(all) != list(own) {
			t.Fatalf("every address at once differs from the system's own\nnetlink: %s\nsystem:  %s", list(all), list(own))
		}

		// One interface asked for by its index is that interface.
		one, err := interfaceTable(table[0].Index)
		if err != nil || len(one) != 1 || one[0].Index != table[0].Index || one[0].Name != table[0].Name {
			t.Fatalf("interface %d asked for alone gave %+v, %v", table[0].Index, one, err)
		}
		return
	}
	t.Fatal("the machine's interfaces changed under every one of twenty readings")
}
