//go:build (darwin && !ios) || (linux && !android)

package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// THE SLOT IS NAMED EXACTLY. The pattern this replaced was `aii -dir <slot>`
// handed to pgrep -f: identity-1 matched identity-10, and a path's dots,
// brackets and pluses were regex. (Moved here from cmd/aii-app with the
// decision itself, which uninstall asks too.)
func TestTheSlotIsNamedExactlyNotByPattern(t *testing.T) {
	slot := "/home/p/.aii/identity-1"
	odd := "/home/p q/a.b[1]+(x)/identity-1"
	for _, c := range []struct {
		name string
		argv []string
		slot string
		want bool
	}{
		{"exact", []string{"/usr/bin/aii", "-dir", slot}, slot, true},
		{"inline", []string{"aii", "-dir=" + slot}, slot, true},
		{"double dash", []string{"aii", "--dir", slot}, slot, true},
		{"trailing slash", []string{"aii", "-dir", slot + "/"}, slot, true},
		{"spaces and punctuation", []string{"aii", "-dir", odd}, odd, true},
		{"a prefix slot", []string{"aii", "-dir", slot + "0"}, slot, false},
		{"punctuation is not a pattern", []string{"aii", "-dir", "/home/p q/aXb11(x)/identity-1"}, odd, false},
		{"another program", []string{"/usr/bin/vim", "-dir", slot}, slot, false},
		{"no value", []string{"aii", "-dir"}, slot, false},
		{"a name, not the flag", []string{"aii", "register", slot}, slot, false},
	} {
		if got := runsSlot([][]string{c.argv}, c.slot); got != c.want {
			t.Errorf("%s: %q for %q = %v, want %v", c.name, c.argv, c.slot, got, c.want)
		}
	}
}

// THE PROCESS TABLE IS READ WHOLE ON THIS PLATFORM: a real process running a
// slot whose path has spaces and punctuation is found, and its prefix
// neighbour is not.
func TestARunningSlotIsFoundInTheProcessTable(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(t.TempDir(), "a b.c[1]+")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	stand := filepath.Join(base, "aii")
	raw, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stand, raw, 0o755); err != nil {
		t.Fatal(err)
	}
	slot := filepath.Join(base, "identity-10")
	cmd := exec.Command(stand, "-dir", slot)
	cmd.Env = append(os.Environ(), "AII_INSTALL_STAND_IN=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })

	deadline := time.Now().Add(10 * time.Second)
	for {
		present, err := SlotRunning(slot)
		if err != nil {
			t.Fatal(err)
		}
		if present {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the process running %q was not found in the process table", slot)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if present, err := SlotRunning(filepath.Join(base, "identity-1")); err != nil || present {
		t.Fatalf("identity-1 was read as running because identity-10 is: %v %v", present, err)
	}
}
