//go:build windows

package fileperm

import (
	"strings"
	"testing"
)

// A CAPABILITY'S SID IS ITS NAME'S: the same in every process that derives
// it, a capability SID (S-1-15-3-1024-…), and another name's is another.
func TestACapabilitySIDIsItsNamesAlone(t *testing.T) {
	a, err := CapabilitySID("aiios.store.v.0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	again, err := CapabilitySID("aiios.store.v.0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	other, err := CapabilitySID("aiios.store.v.fedcba9876543210")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(a.String(), "S-1-15-3-1024-") {
		t.Fatalf("not a capability SID: %s", a)
	}
	if a.String() != again.String() || a.String() == other.String() {
		t.Fatalf("one name, one SID: %s %s; another name: %s", a, again, other)
	}
}
