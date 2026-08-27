package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// What a tool SAYS BACK is half the conversation the identity is having.
// These pin the half that was silent, misleading, or wrong.

// The whole point: a command that fails is usually a command that has
// something to say. Reported as an Error, Result.Text() returns the error
// INSTEAD of the output, and the reasons are gone.
func TestFailingCommandKeepsItsOutput(t *testing.T) {
	bt := &ShellTool{timeout: 10 * time.Second, sandbox: t.TempDir()}
	res, err := bt.Execute(context.Background(), map[string]interface{}{
		"command": "echo 'main.go:7:2: undefined: doTheThing' >&2; exit 2",
	})
	if err != nil {
		t.Fatal(err)
	}
	text := res.Text()
	if !strings.Contains(text, "undefined: doTheThing") {
		t.Fatalf("the compiler's reason never reached the model: %q", text)
	}
	if !strings.Contains(text, "exit status 2") {
		t.Fatalf("the status was dropped along with the failure: %q", text)
	}
	if res.Error != "" {
		t.Fatalf("a command that ran and answered is not an error: Error=%q", res.Error)
	}
}

// grep's ordinary "nothing matched" is exit 1. It must not look like a
// broken tool, because an identity that cannot trust a negative pays for
// every one of them twice.
func TestNoMatchIsAnAnswerNotAFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hay\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bt := &ShellTool{timeout: 10 * time.Second, sandbox: dir}
	res, _ := bt.Execute(context.Background(), map[string]interface{}{"command": "grep needle f.txt"})
	if res.Error != "" {
		t.Fatalf("a search that found nothing was reported as a failure: %q", res.Error)
	}
	if !strings.Contains(res.Text(), "exit status 1") {
		t.Fatalf("the identity cannot tell an empty search from an empty file: %q", res.Text())
	}
}

// A command that cannot RUN is still an error — nothing ran, so there is
// no output to lose. Without this the change would have made every
// failure look like a result.
func TestACommandThatCannotRunIsStillAnError(t *testing.T) {
	bt := &ShellTool{timeout: 10 * time.Second, sandbox: filepath.Join(t.TempDir(), "does-not-exist")}
	res, err := bt.Execute(context.Background(), map[string]interface{}{"command": "echo hi"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Error == "" {
		t.Fatalf("a command that could not start reported success: %+v", res)
	}
}

// The grep TOOL, not the shell: no matches is output, and a pattern
// written in the wrong dialect is named rather than left silent.
func TestGrepToolNamesTheDialectWhenNothingMatched(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("func newOverlayServer() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := &GrepTool{}

	// RE2 alternation finds it.
	ok, _ := g.Execute(context.Background(), map[string]interface{}{
		"pattern": "newOverlayServer|captureLog", "path": dir,
	})
	if ok.Error != "" || !strings.Contains(ok.Output, "newOverlayServer") {
		t.Fatalf("RE2 alternation did not match: %+v", ok)
	}

	// The same intent in grep(1) BRE compiles clean and matches nothing.
	res, _ := g.Execute(context.Background(), map[string]interface{}{
		"pattern": `newOverlayServer\|captureLog`, "path": dir,
	})
	if res.Error != "" {
		t.Fatalf("a search that found nothing is not an error: %q", res.Error)
	}
	if !strings.Contains(res.Output, "no matches") {
		t.Fatalf("the empty result did not say so: %q", res.Output)
	}
	if !strings.Contains(res.Output, "RE2") {
		t.Fatalf("a BRE pattern found nothing and the tool stayed silent about why: %q", res.Output)
	}
}

// A pattern with no BRE escapes must NOT get the hint — otherwise the
// hint is noise on every empty search and stops being read.
func TestNoDialectHintWhenThePatternIsFine(t *testing.T) {
	g := &GrepTool{}
	res, _ := g.Execute(context.Background(), map[string]interface{}{
		"pattern": "definitelyAbsentToken", "path": t.TempDir(),
	})
	if strings.Contains(res.Output, "RE2") {
		t.Fatalf("a well-formed pattern was second-guessed: %q", res.Output)
	}
}

// The tool's description is read BEFORE the first call. That is where the
// dialect has to be, or the identity writes BRE and is silently wrong.
func TestGrepDescriptionStatesItsDialect(t *testing.T) {
	d := (&GrepTool{}).Description()
	if !strings.Contains(d, "RE2") {
		t.Fatalf("the description does not say what language the pattern is in: %q", d)
	}
}

// A refusal that will not say WHAT it refused leaves the identity varying
// the command instead of the path.
func TestSandboxRefusalNamesThePathAndTheRoot(t *testing.T) {
	sandbox := t.TempDir()
	r := NewRegistry(sandbox, nil, Timeouts{})

	why := r.shellRefusal("grep -n Broadcast /work/aiii/aii-os/internal/dashboard/server.go")
	if why == "" {
		t.Fatal("a path outside the sandbox was allowed")
	}
	if !strings.Contains(why, "/work/aiii/aii-os/internal/dashboard/server.go") {
		t.Fatalf("the refusal did not name the offending path: %q", why)
	}
	if !strings.Contains(why, sandbox) {
		t.Fatalf("the refusal did not say where the sandbox IS, so there is nothing to correct toward: %q", why)
	}
	// And the bool stays derived from the same answer.
	if !r.shellCommandEscapes("grep -n x /work/aiii/aii-os/f.go") {
		t.Fatal("shellCommandEscapes disagreed with shellRefusal")
	}
	if r.shellCommandEscapes("grep -n x ./inside.go") {
		t.Fatal("a path inside the sandbox was refused")
	}
}
