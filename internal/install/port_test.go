package install

import (
	"os"
	"strings"
	"testing"
	"time"
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

// A config that reads and names no port is served where the identity
// binds it, DefaultPort, whatever the slot: slot 3 was offered Port(3) at
// creation, but an operator who removed the key moved it.
func TestConfiguredPortOfAConfigNamingNoPortIsTheIdentitysDefault(t *testing.T) {
	root := t.TempDir()
	dir, err := Create(root, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ConfigPathIn(dir), []byte(`{"dashboard":{"host":"127.0.0.1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ConfiguredPort(dir, 3); got != DefaultPort {
		t.Fatalf("ConfiguredPort of a config naming no port = %d, want DefaultPort %d", got, DefaultPort)
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
	got, err := NextFreePort(root, Port(0), DefaultPortSearchSpan, func(p int) bool { return !held[p] })
	if err != nil || got != Port(0)+3 {
		t.Fatalf("next free after slot 0: %d %v", got, err)
	}
	if _, err := NextFreePort(root, Port(0), DefaultPortSearchSpan, func(int) bool { return false }); err == nil {
		t.Fatal("a machine with nothing free was given a port")
	}
	if got, err := NextFreePort(root, 65534, DefaultPortSearchSpan, func(int) bool { return true }); err != nil || got != 65535 {
		t.Fatalf("the top of the range: %d %v", got, err)
	}
	if _, err := NextFreePort(root, 65535, DefaultPortSearchSpan, func(int) bool { return true }); err == nil {
		t.Fatal("a port past 65535 was offered")
	}
	// The span is the operator's (dashboard.port_search_span): a search
	// looks that far and no further.
	if _, err := NextFreePort(root, Port(0), 2, func(p int) bool { return !held[p] }); err == nil || !strings.Contains(err.Error(), "dashboard.port_search_span is 2") {
		t.Fatalf("a span of two looked past the two it was given: %v", err)
	}
}

// THE TWO NUMBERS A START READS FROM ITS SLOT are held to their bounds:
// absent or 0 is the default, and a value outside the bounds is held to the
// nearer one; a key of the wrong type is read as absent and leaves the
// port readable.
func TestTheSlotsStartWaitAndPortSpanAreReadAndHeld(t *testing.T) {
	dir, err := Create(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := ConfiguredStartWait(dir); got != DefaultStartWaitSeconds*time.Second {
		t.Fatalf("an unstated wait is %s", got)
	}
	if got := ConfiguredPortSearchSpan(dir); got != DefaultPortSearchSpan {
		t.Fatalf("an unstated span is %d", got)
	}
	path := ConfigPathIn(dir)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		with       string
		wait, span int
	}{
		{`"start_wait_seconds": 90, "port_search_span": 7,`, 90, 7},
		{`"start_wait_seconds": 1, "port_search_span": 5000,`, MinStartWaitSeconds, MaxPortSearchSpan},
		{`"start_wait_seconds": "soon", "port_search_span": 7,`, DefaultStartWaitSeconds, 7},
	} {
		if err := os.WriteFile(path, []byte(strings.Replace(string(raw), `"dashboard": {`, `"dashboard": {`+c.with, 1)), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := ConfiguredStartWait(dir); got != time.Duration(c.wait)*time.Second {
			t.Errorf("%s: the wait in force is %s, want %ds", c.with, got, c.wait)
		}
		if got := ConfiguredPortSearchSpan(dir); got != c.span {
			t.Errorf("%s: the span in force is %d, want %d", c.with, got, c.span)
		}
		if got := ConfiguredPort(dir, 0); got != Port(0) {
			t.Errorf("%s: the port is read as %d", c.with, got)
		}
	}
}
