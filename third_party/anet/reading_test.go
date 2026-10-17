//go:build linux

package anet

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"testing"
)

// described is what a reading says of the machine: each interface that has
// an address, by index, name, MTU and flags, with its addresses in order.
func described(t *testing.T, ifaces []net.Interface, addrs func(*net.Interface) ([]net.Addr, error)) []string {
	t.Helper()
	var out []string
	for i := range ifaces {
		got, err := addrs(&ifaces[i])
		if err != nil {
			t.Fatalf("the addresses of %s: %v", ifaces[i].Name, err)
		}
		if len(got) == 0 {
			continue // the netlink reading is of addresses: it never sees an interface with none
		}
		var said []string
		for _, a := range got {
			said = append(said, a.String())
		}
		sort.Strings(said)
		out = append(out, fmt.Sprintf("%d %s mtu=%d %s: %s", ifaces[i].Index, ifaces[i].Name, ifaces[i].MTU, ifaces[i].Flags, strings.Join(said, " ")))
	}
	sort.Strings(out)
	return out
}

func systems(t *testing.T) []string {
	t.Helper()
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	return described(t, ifaces, func(i *net.Interface) ([]net.Addr, error) { return i.Addrs() })
}
