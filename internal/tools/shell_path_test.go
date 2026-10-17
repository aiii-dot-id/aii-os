//go:build !windows

package tools

import (
	"runtime"
	"strings"
	"testing"
)

// On macOS the identity's shell finds Homebrew's tools first, as the
// operator's own shell does; everywhere else the PATH is the system's.
func TestShellPathPutsHomebrewFirstOnMacOSOnly(t *testing.T) {
	system := shellPath("linux")
	if strings.Contains(system, "homebrew") {
		t.Fatalf("linux PATH = %q, want no Homebrew", system)
	}
	mac := shellPath("darwin")
	if mac != "/opt/homebrew/bin:/opt/homebrew/sbin:"+system {
		t.Fatalf("macOS PATH = %q, want Homebrew's bin and sbin, then %q", mac, system)
	}
	for _, goos := range []string{"freebsd", "ios", "android"} {
		if p := shellPath(goos); p != system {
			t.Fatalf("%s PATH = %q, want %q", goos, p, system)
		}
	}
}

// The shell is started with this host's PATH and nothing else of the
// host's environment beyond HOME and LANG.
func TestShellEnvCarriesThisHostsPath(t *testing.T) {
	env := shellEnv("/sandbox")
	want := []string{"PATH=" + shellPath(runtime.GOOS), "HOME=/sandbox", "LANG=C.UTF-8"}
	if strings.Join(env, "\n") != strings.Join(want, "\n") {
		t.Fatalf("shellEnv = %q, want %q", env, want)
	}
}
