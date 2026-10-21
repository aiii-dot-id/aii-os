package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

type ShellTool struct {
	timeout time.Duration
	sandbox string

	wall func() (shellWall, error)

	left leftJobs
}

type shellWall struct {
	kept     []*firewall.Rule
	roots    []string
	boundary bool
}

const ReasonShellWallUnavailable = "SHELL_WALL_UNAVAILABLE"

func shellWallUnavailable(cause error) string {
	return "the shell did not run: on this system it runs only behind the operating system's wall, and the wall could not be built: " + cause.Error()
}

const shellLeftJob = "the command left a job running"

var errShellCeiling = errors.New("the shell's ceiling was reached")

const ReasonShellTimeout = "SHELL_TIMEOUT"

func (t *ShellTool) Name() string { return "shell" }
func (t *ShellTool) Description() string {

	return "Execute a command with the host shell (" + shellDialect + ")." + shellWallDescription + " Args: command (required), cwd (optional: the directory to run in)"
}

func (t *ShellTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string", "description": shellDialect + " command to execute"},
			"cwd":     map[string]any{"type": "string", "description": "Directory to run the command in: a path in your home or a granted root, relative to your home or absolute. Relative paths in the command are read from it. Default: your home. Use it instead of cd: the sandbox check does not follow a cd inside the command"},
		},
		"required": []string{"command"},
	}
}

func (t *ShellTool) Execute(ctx context.Context, args map[string]any) (Result, error) {
	command, _ := args["command"].(string)
	if command == "" {
		return Refusal(ReasonArgumentsRequired, "command is required"), nil
	}
	if t.sandbox == "" {
		return Result{Error: "shell unavailable: no sandbox configured"}, nil
	}

	dir := t.sandbox
	if cwd, _ := args["cwd"].(string); cwd != "" {
		dir = from(t.sandbox, cwd)
	}

	ctx, cancel := context.WithTimeoutCause(ctx, t.timeout, errShellCeiling)
	defer cancel()

	bin, argv := shellInvocation()
	argv = append(argv, command)

	w := shellWall{boundary: true}
	if t.wall != nil {
		got, err := t.wall()
		if err != nil {
			return Refusal(ReasonShellWallUnavailable, shellWallUnavailable(err)), nil
		}
		w = got
	}
	wbin, wargv, werr := behindWall(t.sandbox, w, bin, argv)
	if werr != nil {
		return Refusal(ReasonShellWallUnavailable, shellWallUnavailable(werr)), nil
	}
	cmd := exec.CommandContext(ctx, wbin, wargv...)

	tree := &shellTree{}
	prepareTree(cmd)

	var killed atomic.Bool
	cmd.Cancel = func() error { killed.Store(true); return tree.kill(cmd) }
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = dir
	cmd.Env = shellEnv(t.sandbox)

	cmd.Stdin = strings.NewReader("")

	buf := &cappedBuffer{cap: shellOutputCap}
	cmd.Stdout = buf
	cmd.Stderr = buf

	if err := cmd.Start(); err != nil {

		return Result{Error: err.Error() + fmt.Sprintf(" [sandbox=%q]", t.sandbox)}, nil
	}

	tree.adopt(cmd)
	defer tree.close()
	err := cmd.Wait()

	output := normalizeShellOutput(buf.buf.Bytes())
	if buf.dropped > 0 {
		output = append(output, []byte(fmt.Sprintf("\n[output capped at %d bytes; %d more bytes were produced and discarded]", shellOutputCap, buf.dropped))...)
	}

	result := Result{Output: string(output), Truncated: buf.dropped > 0}
	if err != nil {
		var ee *exec.ExitError
		switch {
		case ctx.Err() != nil:

			result.Interrupted = true
			if errors.Is(context.Cause(ctx), errShellCeiling) {
				result.ReasonCode = ReasonShellTimeout
			}
			result.Output = withPartial(string(output), t.endedBy(ctx))
		case errors.As(err, &ee):
			result.Output = withStatus(string(output), fmt.Sprintf("exit status %d", ee.ExitCode()))
		case errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil:

			result.Output = withStatus(string(output),
				fmt.Sprintf("exit status %d; a process the command left running still held its output after the exit — output from here on is not captured", cmd.ProcessState.ExitCode()))
		default:

			result.Error = err.Error() + fmt.Sprintf(" [sandbox=%q]", t.sandbox)
		}
	}

	if !killed.Load() && t.left.keep(cmd) {
		result.Output = withStatus(result.Output, shellLeftJob)
	}

	if !result.Interrupted && result.Error == "" && cmd.ProcessState != nil {
		if code := cmd.ProcessState.ExitCode(); code >= 0 {
			result.ExitStatus = &code
		}
	}
	return result, nil
}

func (t *ShellTool) endedBy(ctx context.Context) string {
	switch {
	case errors.Is(context.Cause(ctx), errShellCeiling):
		return fmt.Sprintf("timed out after %s", t.timeout)
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "the turn ran out of time before this command finished"
	}
	return "the turn was stopped before this command finished"
}

func withPartial(out, why string) string {
	if strings.TrimRight(out, "\r\n") == "" {
		return "[" + why + " — no exit status; no output]"
	}
	return withStatus(out, why+" — no exit status; output above is partial")
}

func withStatus(out, status string) string {

	out = strings.TrimRight(out, "\r\n")
	if out == "" {
		return "[" + status + "; no output]"
	}
	return out + "\n[" + status + "]"
}

const shellOutputCap = 256 * 1024

type cappedBuffer struct {
	buf     bytes.Buffer
	cap     int
	dropped int64
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.cap - b.buf.Len(); room > 0 {
		if len(p) <= room {
			b.buf.Write(p)
			return len(p), nil
		}
		b.buf.Write(p[:room])
		b.dropped += int64(len(p) - room)
		return len(p), nil
	}
	b.dropped += int64(len(p))
	return len(p), nil
}
