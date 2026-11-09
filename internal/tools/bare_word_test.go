package tools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

// A WORD THAT MERELY NAMES A KEPT PLACE IS NOT A PATH INTO IT. The wall
// refused every command carrying the bare word "log" -- 58 refusals in 48
// hours on the identity's own shell -- because the place rule resolved each
// token against the base as though it were a path, so "git log" became
// <home>/log. A token is a path only where the command would read it as one:
// a program's first argument is a word of that program's own grammar.
//
// The control is two-sided. These false refusals must pass; and every real
// refusal -- a word used as a path, with a separator, a walk, a glob, or
// across a separator -- must still refuse. That second half is what keeps a
// narrow exemption from becoming a hole.
func TestABareWordThatNamesAPlaceIsNotAPathIntoIt(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(home, "log")
	if err := os.MkdirAll(kept, 0o700); err != nil {
		t.Fatal(err)
	}
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{{ID: "sub.logs", Kind: firewall.KindSubstrate, Path: kept,
		Except: []string{filepath.Join(kept, "control.json")}, Reason: "kept", Enforced: true}})
	r := NewRegistry(home, policy, Timeouts{ShellSeconds: 30})

	// The false refusals: a word of the program's grammar, never a path.
	for _, cmd := range []string{
		"git log",
		"git log --oneline -1",
		"git log --format=%s",
		// git's own options before the subcommand.
		"git -C notes log -1 --format=%h",
		"git --no-pager log",
		"git -c core.pager=cat log --oneline",
		"git --git-dir=notes/.git log",
		"go build ./...",
		"docker ps",
	} {
		if why, _ := r.shellWallFrom(policy, home, cmd); why != "" {
			t.Errorf("%q reads its word as a word, and is refused: %s", cmd, why)
		}
	}

	// The real refusals, from the home. Every one of these uses the word as a
	// path, or hands it to a command that will.
	for _, cmd := range []string{
		"rm -r log",
		"ls log",
		"cat log/today",
		"mv log x",
		"cd log && rm *",
		"rm -rf ./log",
		"rm -rf log/",
		"git log; rm -r log",
		"git log && rm -r log",
		"git log $(rm -r log)",
		"echo log | sh",
		"cp log x",
		// A grammar word reused as an explicit path in the same simple
		// command. The token list deduplicates, so an exemption bound to a
		// token would swallow the later occurrence: the argument must be
		// proven from the command's own words.
		"git log -- log",
		"git log log",
		// An option's value is a path, whatever follows it; and the
		// explicit path after the subcommand is still asked.
		"git -C log status",
		"git --attr-source log status",
		"git -C notes log -- log",
		// A redirection's operand is opened by the shell before the program
		// starts, so it is a path whatever it spells.
		"git log > log",
		"git log >log",
		"git < log",
	} {
		if why, _ := r.shellWallFrom(policy, home, cmd); why == "" {
			t.Errorf("%q reads the word as a path, and is let through", cmd)
		}
	}
}

// THE MESSAGE NAMES THE PLACE, AND NEVER COMPARES A WORD TO ITSELF. A word
// that IS the place's own name is said to name it; only a path that resolves
// inside is said to be inside.
func TestTheRefusalNamesThePlaceItRefuses(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(home, "log")
	if err := os.MkdirAll(kept, 0o700); err != nil {
		t.Fatal(err)
	}
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{{ID: "sub.logs", Kind: firewall.KindSubstrate, Path: kept,
		Reason: "the host keeps it", Enforced: true}})
	r := NewRegistry(home, policy, Timeouts{ShellSeconds: 30})

	why, _ := r.shellWallFrom(policy, home, "ls log")
	if why == "" {
		t.Fatal("ls log was let through")
	}
	if !contains(why, "log") || !contains(why, "keeps") {
		t.Errorf("the refusal does not name the place: %s", why)
	}
	if contains(why, "log is inside log") {
		t.Errorf("the refusal compares a word to itself: %s", why)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
