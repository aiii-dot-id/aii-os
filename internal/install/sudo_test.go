package install

import (
	"os"
	"os/user"
	"strings"
	"testing"
)

// .
// .
// .
func someOtherAccount(t *testing.T) *user.User {
	t.Helper()
	me, _ := user.Current()
	for _, name := range []string{"daemon", "nobody", "bin", "sys"} {
		u, err := user.Lookup(name)
		if err != nil || u.HomeDir == "" {
			continue
		}
		if me != nil && u.HomeDir == me.HomeDir {
			continue
		}
		return u
	}
	if me != nil && me.Username != "root" {
		return me
	}
	t.Skip("no distinguishable second account on this host")
	return nil
}

// .
// .
// .
// .
// .
// .
// .
func TestRootFollowsTheOperatorNotTheEffectiveUser(t *testing.T) {
	other := someOtherAccount(t)
	t.Setenv("SUDO_USER", other.Username)

	got, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, other.HomeDir) {
		t.Fatalf("Root() with SUDO_USER=%s = %q, want it under that operator's home %q",
			other.Username, got, other.HomeDir)
	}
	if cur, err := os.UserHomeDir(); err == nil && other.HomeDir != cur && strings.HasPrefix(got, cur) {
		t.Fatalf("Root() followed the EFFECTIVE user's home %q instead of the operator's %q", cur, other.HomeDir)
	}

	name, err := OperatorName()
	if err != nil {
		t.Fatal(err)
	}
	if name != other.Username {
		t.Fatalf("OperatorName() = %q, want %q", name, other.Username)
	}
}

// .
// .
func TestSudoUserRootIsIgnored(t *testing.T) {
	t.Setenv("SUDO_USER", "root")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home")
	}
	got, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, home) {
		t.Fatalf("Root() with SUDO_USER=root = %q, want it under %q", got, home)
	}
}

// .
func TestRootWithoutSudoUsesTheCurrentHome(t *testing.T) {
	t.Setenv("SUDO_USER", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home")
	}
	got, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, home) {
		t.Fatalf("Root() = %q, want it under %q", got, home)
	}
}
