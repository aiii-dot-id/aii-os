package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

// A SUBCOMMAND IS A WORD IN EVERY STAGE OF A PLAIN PIPELINE. The grammar exemption was offered to one simple command only,
// so "git log --oneline | head -1" was refused as naming <home>/log, on
// a host with a shell wall and on one without. A pipe hands the
// next program git's OUTPUT, never git's words, so the subcommand is a
// word of git's grammar wherever git stands in the pipeline. The pattern
// exemption has worked per stage of a plain pipeline since 2026-11-06;
// this takes the subcommand exemption to the same place, with the same
// rules: no ; & < > ` newline, substitution or ||, and the word occurs
// once in the whole command.
//
// The control half is every way a word can still be a path: a second
// occurrence anywhere in the command, a stage that opens it, a shell fed
// the word, and every separator the exemption does not reach.
func TestASubcommandIsAWordInEveryStageOfAPlainPipeline(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(home, "log")
	if err := os.MkdirAll(kept, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "notes"), 0o700); err != nil {
		t.Fatal(err)
	}
	policy := firewall.DefaultPolicy()
	policy.SetPlaces([]*firewall.Rule{{ID: "sub.logs", Kind: firewall.KindSubstrate, Path: kept,
		Reason: "kept", Enforced: true, ReadOpen: true}})
	r := NewRegistry(home, policy, Timeouts{ShellSeconds: 30})

	// The false refusals: the subcommand of a program that stands in a
	// pipeline, first, last or between.
	for _, cmd := range []string{
		"git log --oneline -3 | head -1",
		"git log --format=%s | wc -l",
		"git -C notes log -1 --format=%h | cat",
		"git --no-pager log | head",
		"cat notes/x | git hash-object --stdin",
		"git log --oneline | grep -c fix",
		"go list ./... | wc -l",
		"docker ps | grep x",
		"git log | sh",
	} {
		if why, _ := r.shellWallFrom(policy, home, cmd); why != "" {
			t.Errorf("%q: a subcommand in a pipeline was refused as a path: %s", cmd, why)
		}
	}

	// The real refusals: the word used, or usable, as a path.
	for _, cmd := range []string{
		"echo log | sh",
		"git log | tee log",
		"git log | cat log",
		"git log | xargs rm -r log",
		"git status | tee log",
		"git log | git -C log status",
		"cat log | git hash-object --stdin",
		"git log -- log | head",
		"git log | head > log",
		"git log >log | head",
		"git log; rm -r log",
		"git log && rm -r log",
		"git log || rm -r log",
		"git log & cat log",
		"git log | $(echo cat) log",
		"git log | `echo cat` log",
		"git log |\ncat log",
		"ls log | head",
		"git log | ls log",
	} {
		if why, _ := r.shellWallFrom(policy, home, cmd); why == "" {
			t.Errorf("%q reads the word as a path, and is let through", cmd)
		}
	}
}

// A PIPE INSIDE QUOTES ENDS NO STAGE, AND ONE AFTER A BACKSLASH ENDS
// ONE ONLY WHERE THE SHELL SAYS SO. Both exemptions cut the command at every |, and an unterminated
// quote reads to the end of its piece, so "git log '|' cat log" counted
// log once and exempted the last word, which the shell hands to git as a
// path. A cut is trusted only when the stages read, in order, as the
// whole command does; otherwise nothing is exempt, for the subcommand and
// for the pattern alike.
//
// A backslash is the dialect's question. Bash escapes the | after it:
// "cat x\|git log" runs cat with x|git and log, so git is no stage and
// log is cat's path. PowerShell escapes nothing with a backslash: the
// same text is two stages there, cat x\ and git log, and log is git's
// subcommand (run on the Windows guest through the shell tool, session
// 1, 2026-11-11: git log printed its commit after cat's error). What
// PowerShell does escape is a backtick, and the exemptions decline any
// command holding one, on both dialects. The invariant is one: a word is
// exempted only across a pipe the shell that runs the command sees.
func TestAQuotedOrEscapedPipeEndsNoStage(t *testing.T) {
	r, policy, home := newWordWall(t)
	if err := os.WriteFile(filepath.Join(home, "notes.txt"), []byte("the log word"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Refused on every dialect: the word is, or may be, a path.
	refused := []string{
		"git log '|' cat log",
		`git log "|" cat log`,
		"git log 'a|b' log",
		"grep -r log '|' x log",
		`grep -r log "|" x log`,
		// Fails closed: a quoted | in an option's value is not told apart
		// from one that hides a stage, so this asks every word.
		"git log --format='%h|%s' | head -1",
		// A backtick: PowerShell's escape (one stage, cat with x|git and
		// log) and bash's substitution. Declined on both.
		"cat x`|git log",
		// A real path in another stage stays a path, after a backslash as
		// anywhere: Get-Content is no program with subcommands.
		`cat x\|Get-Content log`,
		`cat x\|git log | Out-File log`,
	}
	// Read per stage on every dialect: the stages are the shell's own.
	allowed := []string{
		"git log --oneline | head -1",
		"cat notes.txt | grep -c log",
		`git log --format="%h %s" | head -1`,
	}
	escaped := []string{`cat x\|git log`, `cat x\|grep log`}
	switch shellDialect {
	case "bash":
		refused = append(refused, escaped...)
	case "PowerShell":
		allowed = append(allowed, escaped...)
	default:
		t.Fatalf("no expectation for the dialect %q: a new dialect states how it reads a backslash before this passes", shellDialect)
	}
	for _, cmd := range refused {
		why, _ := r.shellWallFrom(policy, home, cmd)
		if why == "" || !strings.Contains(why, "log") {
			t.Errorf("%q: a word was exempted across a pipe the %s shell does not see: %q", cmd, shellDialect, why)
		}
	}
	for _, cmd := range allowed {
		if why, _ := r.shellWallFrom(policy, home, cmd); why != "" {
			t.Errorf("%q: a word of a stage the %s shell runs was refused: %s", cmd, shellDialect, why)
		}
	}
}
