package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

// THE WALL IS ASKED THE WAY A CALL ASKS IT. The tests above call
// shellWallFrom directly, which skips shellBase -- so the path a call with a
// cwd takes (the script path) was never exercised, and a hole there would
// have been invisible to every test in this file. These go through Execute,
// the entry the model's call and the host's call both take (R162: the
// directory the wall reads from and the directory the process starts in are
// one value).
//
// The home is a t.TempDir(), never a live one: a test that writes into the
// identity's own home to prove a boundary is a test that can damage what it
// is proving.
//
// A KEPT PLACE IS KEPT WHERE THE COMMAND READS IT. shellBase returns the
// call's cwd as the base, so "log" from a call running in work/ is
// <home>/work/log -- NOT <home>/log. A fixture that keeps the place beside
// the working directory and then runs from inside it is asking about a
// different path, and the first draft of this file made exactly that
// mistake: eleven real paths "passed" through, because none of them named
// the place that was actually kept. The kept places are therefore created
// BESIDE each base the test uses: at the home for the inline path, and
// inside work/ for the cwd path.
func newWordWall(t *testing.T) (*Registry, *firewall.Policy, string) {
	t.Helper()
	return wordWall(t, true)
}

// newWordWallDirs is newWordWall without the kept FILE: the tests that
// run a command through the wall use it. On Windows a kept file lying
// directly in a folder the shell may write is a layout the wall cannot
// hold, and every call is refused before it runs (the native run of
// 2026-11-07, wallKeptInLow); the kept directories are the names the
// word tests need. The file sentence and the real-path refusals are read
// before any wall is built, so those tests keep the file.
func newWordWallDirs(t *testing.T) (*Registry, *firewall.Policy, string) {
	t.Helper()
	return wordWall(t, false)
}

func wordWall(t *testing.T, withFile bool) (*Registry, *firewall.Policy, string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(home, "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}

	// One kept directory and one kept file under EACH base the test runs
	// from, so a real path refuses whether or not the call carries a cwd.
	//
	// A kept directory named after every word command's WORD is what makes
	// the exemption load-bearing: with <base>/build kept, "go build ./..."
	// can only pass because the wall read "build" as a word and not as the
	// path <base>/build. A test whose kept name never collides with the word
	// would pass even if the exemption did nothing.
	//
	// THE KEPT FILE'S NAME IS DELIBERATELY NOT A SUBSTRATE PATTERN. The
	// first draft used one, and the pattern scan answered first, so the
	// place sentence was never composed and the assertion below could not
	// fail however placeSaid was written -- control D proved it vacuous.
	// "vault.key" is a name no pattern holds, so only the place rule can
	// refuse it.
	names := map[string]bool{"log": true}
	if withFile {
		names["vault.key"] = true
	}
	for _, tc := range everyWordCommand {
		names[tc.word] = true
	}
	var places []*firewall.Rule
	for _, base := range []string{home, work} {
		for name := range names {
			path := filepath.Join(base, name)
			if name == "vault.key" {
				if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.MkdirAll(path, 0o700); err != nil {
				t.Fatal(err)
			}
			places = append(places, &firewall.Rule{
				ID:   "sub." + name + "." + filepath.Base(base),
				Kind: firewall.KindSubstrate, Path: path, Reason: "kept", Enforced: true,
			})
		}
	}
	policy := firewall.DefaultPolicy()
	policy.SetPlaces(places)
	r := NewRegistry(home, policy, Timeouts{ShellSeconds: 30})
	// The wall holds the kept directories open for the tool's life (on
	// Windows without share-delete); ended here, so the temporary
	// directory can be removed.
	t.Cleanup(r.shell.endWall)
	return r, policy, home
}

// everyWordCommand is the enumerated list, written here as data so a program
// added to the map without a collision case fails this test: the list is
// only as safe as its least-tested entry. The word is the program's first
// argument, and it is the name the fixture keeps as a directory.
var everyWordCommand = []struct{ prog, word, cmd string }{
	{"git", "log", "git log --oneline -1"},
	{"go", "build", "go build ./..."},
	{"docker", "ps", "docker ps"},
	{"kubectl", "get", "kubectl get pods"},
	{"npm", "test", "npm test"},
	{"cargo", "build", "cargo build"},
	{"systemctl", "status", "systemctl status"},
	{"brew", "list", "brew list"},
}

// A LISTED PROGRAM WITH NO COLLISION CASE LEAVES THE LIST. A word command
// whose word is also a kept name is the only case that can tell the
// exemption from a hole; a program whose test word never collides proves
// nothing about it.
func TestEveryListedProgramHasAKeptNameCollisionCase(t *testing.T) {
	for _, tc := range everyWordCommand {
		if !shellWordCommands[tc.prog] {
			t.Errorf("%q has a collision case but is not in shellWordCommands", tc.prog)
		}
		if !strings.Contains(tc.cmd, tc.word) {
			t.Errorf("%q case does not carry its word %q", tc.cmd, tc.word)
		}
	}
	if len(everyWordCommand) != len(shellWordCommands) {
		t.Errorf("%d listed programs, %d collision cases: every entry needs one",
			len(shellWordCommands), len(everyWordCommand))
	}
}

// THE INLINE PATH: a call with no cwd, judged from the home. Each command's
// word is also a kept directory at that home, so passing means the wall read
// a word.
func TestTheExemptionHoldsThroughTheInlineCall(t *testing.T) {
	r, _, _ := newWordWallDirs(t)
	ctx := context.Background()

	for _, tc := range everyWordCommand {
		res, err := r.Execute(ctx, "shell", map[string]any{"command": tc.cmd})
		if err != nil {
			t.Fatalf("%q: %v", tc.cmd, err)
		}
		if res.Refused {
			t.Errorf("inline %q reads its word as a word, and is refused: %s", tc.cmd, res.Error)
		}
	}
}

// THE CWD ROUTE: a call naming a cwd. This is the route shellWallFrom
// cannot reach, and the one R162 made possible. The kept place of the same
// name exists inside work/, so the base is the cwd here. It runs no
// script, and this test does not claim to: the script form is
// TestTheScriptFormAsksTheFilesPath below.
func TestTheExemptionHoldsThroughACallWithACwd(t *testing.T) {
	r, _, home := newWordWallDirs(t)
	ctx := context.Background()
	work := filepath.Join(home, "work")

	for _, tc := range everyWordCommand {
		res, err := r.Execute(ctx, "shell", map[string]any{"command": tc.cmd, "cwd": work})
		if err != nil {
			t.Fatalf("%q: %v", tc.cmd, err)
		}
		if res.Refused {
			t.Errorf("with cwd, %q reads its word as a word, and is refused: %s", tc.cmd, res.Error)
		}
	}
}

// AND THE SAME TWO PATHS STILL REFUSE A REAL PATH. The exemption must not
// have opened the door it was cut beside. Every case runs from BOTH bases,
// and each base keeps the place the command names -- see newWordWall.
func TestTheRealPathsStillRefuseThroughBothPaths(t *testing.T) {
	r, _, home := newWordWall(t)
	ctx := context.Background()
	work := filepath.Join(home, "work")

	real := []string{
		"ls log", "cat log/today", "mv log x", "rm -r log", "cp log x",
		"cat vault.key", "rm vault.key", "mv vault.key x",
		"git log -- log", "git log log", "git log > log", "git < log",
		"git log; rm -r log", "git log && rm -r log", "cd log && rm *",
		"echo log | sh",
		// The word appearing a second time is not an exemption: the
		// occurrence count is what makes "git log -- log" refuse.
		"git log --oneline -1 log",
	}
	for _, cmd := range real {
		for _, args := range []map[string]any{
			{"command": cmd},
			{"command": cmd, "cwd": work},
		} {
			res, err := r.Execute(ctx, "shell", args)
			if err != nil {
				t.Fatalf("%q: %v", cmd, err)
			}
			if !res.Refused {
				t.Errorf("%q was let through (cwd=%v)", cmd, args["cwd"] != nil)
			}
		}
	}
}

// A KEPT FILE IS NOT A DIRECTORY, AND THE SENTENCE MUST NOT SAY IT IS. The
// operator approved this sentence (~19:08Z): "<word> names the directory
// <name>/, which the host keeps". A file has no trailing slash and is not a
// directory; saying so would be the wall describing a place it does not
// keep.
//
// Refusal() puts the sentence in Result.Error, not Output -- a gate's answer
// is an error field and Refused, never a failure (Result.failed).
//
// THE NAME MUST NOT BE A SUBSTRATE PATTERN. The first draft asserted this
// over a name the pattern scan owns, so the pattern answered before the
// place rule could compose a sentence, and forcing placeSaid to always say
// "directory" left the test passing. A vacuous assertion is worse than none:
// it reports the sentence is right while never having read it.
func TestTheSentenceTellsAKeptFileFromAKeptDirectory(t *testing.T) {
	r, _, _ := newWordWall(t)
	ctx := context.Background()

	res, err := r.Execute(ctx, "shell", map[string]any{"command": "cat vault.key"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Refused {
		t.Fatal("a kept file was let through")
	}
	// The approved form for a file, word for word: it
	// says "the file" and carries no trailing slash. Asserting only that
	// it is NOT the directory sentence would pass on any other wording,
	// including none.
	wantFile := "names the file vault.key, which the host keeps:"
	if !strings.Contains(res.Error, wantFile) {
		t.Errorf("a kept file is not said in the approved form\n got: %s\nwant it to contain: %s", res.Error, wantFile)
	}
	if strings.Contains(res.Error, "vault.key/") {
		t.Errorf("a kept file is given a trailing slash: %s", res.Error)
	}

	// And the directory case still says directory, with its slash.
	res, err = r.Execute(ctx, "shell", map[string]any{"command": "ls log"})
	if err != nil {
		t.Fatal(err)
	}
	wantDir := "names the directory log/, which the host keeps:"
	if !strings.Contains(res.Error, wantDir) {
		t.Errorf("a kept directory is not said in the approved form\n got: %s\nwant it to contain: %s", res.Error, wantDir)
	}
}

// THE SCRIPT FORM: a program kept in a file and named in the command. The
// wall asks the file's path as it asks any path, and it does not read the
// file's body, so no word inside a script is exempted by the inline
// grammar. Both halves matter: a script inside a kept place is refused,
// and a script outside one runs -- which shows the refusal is the file's
// path and not the body's words.
func TestTheScriptFormAsksTheFilesPath(t *testing.T) {
	r, _, home := newWordWallDirs(t)
	ctx := context.Background()

	body := "git log\n"
	inside := filepath.Join(home, "log", "run.sh")
	if err := os.WriteFile(inside, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	res, err := r.Execute(ctx, "shell", map[string]any{"command": "bash log/run.sh"})
	if err != nil {
		t.Fatalf("bash log/run.sh: %v", err)
	}
	if !res.Refused {
		t.Errorf("bash log/run.sh names a file inside the kept place, and is not refused")
	}

	outside := filepath.Join(home, "run.sh")
	if err := os.WriteFile(outside, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	res, err = r.Execute(ctx, "shell", map[string]any{"command": "bash run.sh"})
	if err != nil {
		t.Fatalf("bash run.sh: %v", err)
	}
	if res.Refused {
		t.Errorf("bash run.sh names no kept place, and is refused: %s", res.Error)
	}
}

// GIT'S OWN OPTIONS BEFORE THE SUBCOMMAND, through both routes ("git -C
// <dir> log -1" was refused as naming log/). The word is the same kept name the fixture makes; the list is
// apart from everyWordCommand, which holds one case per program.
func TestGitsOptionsBeforeTheSubcommandHoldThroughBothRoutes(t *testing.T) {
	r, _, home := newWordWallDirs(t)
	ctx := context.Background()
	work := filepath.Join(home, "work")
	for _, cmd := range []string{
		"git -C . log -1 --format=%h",
		"git --no-pager log --oneline -1",
		"git -c core.pager=cat log --oneline -1",
	} {
		for _, args := range []map[string]any{{"command": cmd}, {"command": cmd, "cwd": work}} {
			res, err := r.Execute(ctx, "shell", args)
			if err != nil {
				t.Fatalf("%q: %v", cmd, err)
			}
			if res.Refused {
				t.Errorf("%q (cwd %v) reads its word as a word, and is refused: %s", cmd, args["cwd"] != nil, res.Error)
			}
		}
	}
	// The option's value is a path, and is still asked.
	res, err := r.Execute(ctx, "shell", map[string]any{"command": "git -C log status"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Refused {
		t.Fatalf("git -C log status: the option's value names log/ and was let through: %+v", res)
	}
}
