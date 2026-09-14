package app

import (
	"net"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/broker"
)

// .
// .
func TestRefuseOwnIsPreciseNotBlanket(t *testing.T) {
	own := map[int]bool{8181: true}
	mine := []net.IP{net.ParseIP("192.0.2.6"), net.ParseIP("fe80::1")}
	for _, c := range []struct {
		ip   string
		port int
		want bool
	}{
		{"127.0.0.1", 8181, true},
		{"::1", 8181, true},
		{"192.0.2.6", 8181, true},
		{"192.0.2.6", 8123, false},
		{"192.168.1.50", 8181, false},
		{"127.0.0.1", 11434, false},
		{"169.254.169.254", 8181, false},
	} {
		if got := refuseOwn(net.ParseIP(c.ip), c.port, own, mine); got != c.want {
			t.Fatalf("%s:%d refused=%v, want %v", c.ip, c.port, got, c.want)
		}
	}
	if refuseOwn(net.ParseIP("127.0.0.1"), 8181, nil, mine) {
		t.Fatal("with no listener of its own the identity refuses nothing")
	}
}

// .
func TestPluginGrantViewCarriesTheLocalList(t *testing.T) {
	v := pluginGrantView(broker.Grant{Local: []string{"192.168.1.10:8123", "homeassistant.local:*"}, PlaintextCredentials: true, Hosts: []string{"api.example.test:443"}})
	if len(v.Local) != 2 || v.Local[1] != "homeassistant.local:*" || !v.PlaintextCredentials || len(v.Hosts) != 1 {
		t.Fatalf("view: %+v", v)
	}
}
