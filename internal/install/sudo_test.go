package install

import (
	"os"
	"os/user"
	"strings"
	"testing"
)

// someOtherAccount finds a real, non-root account that is not the one
// running the test, so SUDO_USER can name somebody whose home is
// demonstrably different from the current process's.
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

// Registering a macOS LaunchDaemon needs root, so this path runs under
// sudo routinely. os.UserHomeDir() answers /var/root there, which would
// point a daemon at an identity its owner could never reach — the exact
// confusion the ENTIRELY LOCAL law exists to prevent.
//
// This runs as root or not: it does not ask who the test user is, it
// asks whether SUDO_USER is honoured.
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

// SUDO_USER=root is not an operator: it means someone is root already,
// and an identity must not be owned by root.
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

// Unelevated is the common case and must be unchanged.
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
