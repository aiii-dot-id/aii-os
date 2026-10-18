package install

import (
	"os"
	"testing"
)

// Port(n) is what a slot is created with; config.json is what it runs
// with. An operator who moves the port must not leave every reader
// pointed at the old one.
func TestConfiguredPortFollowsTheConfigNotTheSlotNumber(t *testing.T) {
	root := t.TempDir()
	dir, err := Create(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := ConfiguredPort(dir, 0); got != Port(0) {
		t.Fatalf("fresh slot reports %d, want %d", got, Port(0))
	}

	data, err := os.ReadFile(ConfigPathIn(dir))
	if err != nil {
		t.Fatal(err)
	}
	moved := []byte(string(data))
	// Rewrite the port the way an operator editing config.json would.
	out := replacePort(string(moved), Port(0), 9999)
	if err := os.WriteFile(ConfigPathIn(dir), []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ConfiguredPort(dir, 0); got != 9999 {
		t.Fatalf("after the operator moved the port, ConfiguredPort = %d, want 9999", got)
	}
}

// A slot with no readable config will not start; reporting the
// creation port is a better answer than reporting zero.
func TestConfiguredPortFallsBackWhenConfigIsUnreadable(t *testing.T) {
	root := t.TempDir()
	dir, err := Create(root, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(ConfigPathIn(dir)); err != nil {
		t.Fatal(err)
	}
	if got := ConfiguredPort(dir, 3); got != Port(3) {
		t.Fatalf("ConfiguredPort with no config = %d, want the creation port %d", got, Port(3))
	}
	if err := os.WriteFile(ConfigPathIn(dir), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ConfiguredPort(dir, 3); got != Port(3) {
		t.Fatalf("ConfiguredPort with broken config = %d, want the creation port %d", got, Port(3))
	}
}

func replacePort(s string, from, to int) string {
	old := `"port": ` + itoa(from)
	new := `"port": ` + itoa(to)
	i := indexOf(s, old)
	if i < 0 {
		return s
	}
	return s[:i] + new + s[i+len(old):]
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// THE NEXT FREE PORT steps over every port a slot under the root is
// configured for — a stopped slot still owns its port — and over what free
// says is held, and gives up past its span instead of searching forever.
func TestNextFreePortStepsOverSlotsAndHeldPorts(t *testing.T) {
	root := t.TempDir()
	for _, n := range []int{0, 1} {
		if _, err := Create(root, n); err != nil {
			t.Fatal(err)
		}
	}
	held := map[int]bool{Port(0) + 2: true}
	got, err := NextFreePort(root, Port(0), func(p int) bool { return !held[p] })
	if err != nil || got != Port(0)+3 {
		t.Fatalf("next free after slot 0: %d %v", got, err)
	}
	if _, err := NextFreePort(root, Port(0), func(int) bool { return false }); err == nil {
		t.Fatal("a machine with nothing free was given a port")
	}
	if got, err := NextFreePort(root, 65534, func(int) bool { return true }); err != nil || got != 65535 {
		t.Fatalf("the top of the range: %d %v", got, err)
	}
	if _, err := NextFreePort(root, 65535, func(int) bool { return true }); err == nil {
		t.Fatal("a port past 65535 was offered")
	}
}
