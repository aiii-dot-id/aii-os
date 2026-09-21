package install

import (
	"os"
	"testing"
)

// .
// .
// .
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
	// .
	out := replacePort(string(moved), Port(0), 9999)
	if err := os.WriteFile(ConfigPathIn(dir), []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ConfiguredPort(dir, 0); got != 9999 {
		t.Fatalf("after the operator moved the port, ConfiguredPort = %d, want 9999", got)
	}
}

// .
// .
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
