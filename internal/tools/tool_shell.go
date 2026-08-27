package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// (split from tools.go 2026-08-17 — one file per tool; registry and
// sandbox enforcement live in tools.go)
//
// ONE organ, five platforms (R79, 2026-08-27): the capability is "run
// a command with the host's shell"; WHICH shell is a host fact, not a
// capability difference. This file is the whole tool — the host facts
// (dialect name, invocation, scrubbed environment, tree containment)
// live in shell_unix.go / shell_windows.go, as data and one small
// type, so the tool itself never branches on GOOS. Hosts with no
// shell at all (mobile: both OSes forbid spawning from app sandboxes)
// stay honestly absent via hostcap.Shell — the registration gate in
// tools.go.

type ShellTool struct {
	timeout time.Duration
	sandbox string // cwd/HOME for the scrubbed execution environment
}

func (t *ShellTool) Name() string { return "shell" }
func (t *ShellTool) Description() string {
	// The dialect is part of the contract: an identity writes commands
	// in the language this names. Its first carrier was a tool named
	// "bash" that Windows advertised and failed on every call (the
	// pre-2026-08-18 build) — the name now says what the organ IS and
	// the parenthesis says what the host speaks.
	return "Execute a command with the host shell (" + shellDialect + "). Args: command (required)"
}

func (t *ShellTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"command": map[string]interface{}{"type": "string", "description": shellDialect + " command to execute"},
		},
		"required": []string{"command"},
	}
}

func (t *ShellTool) Execute(ctx context.Context, args map[string]interface{}) (Result, error) {
	command, _ := args["command"].(string)
	if command == "" {
		return Result{Error: "command is required"}, nil
	}
	if t.sandbox == "" {
		return Result{Error: "shell unavailable: no sandbox configured"}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	// Run scrubbed: cwd and HOME/USERPROFILE are the identity's
	// sandbox, the environment carries only what the host shell needs
	// to run. The host env — API keys, service vars, operator secrets —
	// is not the identity's to read. (First-birth lesson: `env` was one
	// command away.)
	//
	// THE ONE EXEC CALL SITE for the shell organ (execguard): the
	// binary and argv prefix are host facts from shellInvocation; only
	// the identity's command is appended.
	bin, argv := shellInvocation()
	cmd := exec.CommandContext(ctx, bin, append(argv, command)...)
	// THE CEILING BOUNDS THE TREE, NOT THE SHELL (external review P1,
	// 2026-08-26): CommandContext's cancel kills only the shell itself —
	// a backgrounded child survived, kept the output pipe open, and
	// Run() blocked past the operator's ceiling for as long as the
	// orphan lived. Tree containment is a host fact too: unix puts the
	// shell in its own process group and cancellation kills the GROUP;
	// Windows adopts the shell into a job object and cancellation
	// terminates the JOB (the ruling recorded in the old stub: "its
	// tree-kill is a job object, not a process group"). WaitDelay is
	// the second fence — if anything still holds the pipe after the
	// kill, Wait abandons it rather than inheriting its lifetime.
	tree := &shellTree{}
	prepareTree(cmd)
	cmd.Cancel = func() error { return tree.kill(cmd) }
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = t.sandbox
	cmd.Env = shellEnv(t.sandbox)
	// Explicit empty stdin via strings.Reader: exec.Cmd with nil Stdin
	// opens /dev/null for the child, which fails under user namespaces
	// (bwrap) with EPERM before the command runs. An empty reader is
	// equivalent (immediate EOF) and namespace-safe.
	cmd.Stdin = strings.NewReader("")
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	if err := cmd.Start(); err != nil {
		// Could not start at all: no shell, no permission, no sandbox.
		// Nothing ran, so there is no output to lose.
		return Result{Error: err.Error() + fmt.Sprintf(" [sandbox=%q]", t.sandbox)}, nil
	}
	// Adopt AFTER Start (Windows: the pid must exist to join the job;
	// unix: no-op, the group was made at start). The gap between Start
	// and adopt is the same bound the supervisor accepted for plugin
	// containment (D24): a shell that spawns in its first instants can
	// place that child outside the tree. Close is the backstop — on
	// Windows the job dies with its handle, so nothing outlives the
	// call.
	tree.adopt(cmd)
	defer tree.close()
	err := cmd.Wait()
	output := buf.Bytes()

	// WHAT THE IDENTITY IS TOLD.
	//
	// Result.Text() returns the error INSTEAD of the output, so setting
	// Error here throws the output away. Every non-zero exit did: a build
	// that failed reached the identity as "exit status 2" with the
	// compiler's reasons discarded, and grep's ordinary "nothing matched"
	// (exit 1) arrived as a failure indistinguishable from a broken tool.
	//
	// A non-zero exit is an ANSWER for most of what gets run here — grep,
	// diff, test, build, lint all say what they found that way. So the
	// status is reported BESIDE the output, not instead of it. Only a
	// command that could not run, or ran out of time, is an error.
	result := Result{Output: string(output)}
	if err != nil {
		var ee *exec.ExitError
		switch {
		case ctx.Err() != nil:
			// The deadline is the operator's ceiling, and partial output
			// is often the whole point of a command that ran long.
			result.Output = withStatus(string(output),
				fmt.Sprintf("timed out after %s — no exit status; output above is partial", t.timeout))
		case errors.As(err, &ee):
			result.Output = withStatus(string(output), fmt.Sprintf("exit status %d", ee.ExitCode()))
		default:
			// Wait failed for a reason that is not an exit status (a
			// pipe error, an abandoned wait): the run is not trustworthy
			// as an answer, so it reports as an error.
			result.Error = err.Error() + fmt.Sprintf(" [sandbox=%q]", t.sandbox)
		}
	}
	return result, nil
}

// withStatus puts the exit status beside the output rather than in place
// of it. An empty run says so: "" and "exit status 1" are different
// answers, and a bare status with nothing above it is the second one.
func withStatus(out, status string) string {
	// CRLF cutset: PowerShell output ends lines \r\n, and trimming only
	// \n left a stray \r against the status bracket (found by the
	// R79 review probe on the win11 VM). Harmless on unix — bash never
	// tails a bare \r.
	out = strings.TrimRight(out, "\r\n")
	if out == "" {
		return "[" + status + "; no output]"
	}
	return out + "\n[" + status + "]"
}
