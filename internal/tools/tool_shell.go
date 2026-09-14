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

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

type ShellTool struct {
	timeout time.Duration
	sandbox string
}

func (t *ShellTool) Name() string { return "shell" }
func (t *ShellTool) Description() string {
	// .
	// .
	// .
	// .
	// .
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

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	bin, argv := shellInvocation()
	cmd := exec.CommandContext(ctx, bin, append(argv, command)...)
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	tree := &shellTree{}
	prepareTree(cmd)
	cmd.Cancel = func() error { return tree.kill(cmd) }
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = t.sandbox
	cmd.Env = shellEnv(t.sandbox)
	// .
	// .
	// .
	// .
	cmd.Stdin = strings.NewReader("")
	// .
	// .
	// .
	// .
	// .
	// .
	buf := &cappedBuffer{cap: shellOutputCap}
	cmd.Stdout = buf
	cmd.Stderr = buf

	if err := cmd.Start(); err != nil {
		// .
		// .
		return Result{Error: err.Error() + fmt.Sprintf(" [sandbox=%q]", t.sandbox)}, nil
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	tree.adopt(cmd)
	defer tree.close()
	err := cmd.Wait()
	// .
	// .
	output := normalizeShellOutput(buf.buf.Bytes())
	if buf.dropped > 0 {
		output = append(output, []byte(fmt.Sprintf("\n[output capped at %d bytes; %d more bytes were produced and discarded]", shellOutputCap, buf.dropped))...)
	}

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	result := Result{Output: string(output), Truncated: buf.dropped > 0}
	if err != nil {
		var ee *exec.ExitError
		switch {
		case ctx.Err() != nil:
			// .
			// .
			result.Output = withStatus(string(output),
				fmt.Sprintf("timed out after %s — no exit status; output above is partial", t.timeout))
		case errors.As(err, &ee):
			result.Output = withStatus(string(output), fmt.Sprintf("exit status %d", ee.ExitCode()))
		case errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil:
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			result.Output = withStatus(string(output),
				fmt.Sprintf("exit status %d; a process the command left running still held its output after the exit — output from here on is not captured", cmd.ProcessState.ExitCode()))
		default:
			// .
			// .
			// .
			result.Error = err.Error() + fmt.Sprintf(" [sandbox=%q]", t.sandbox)
		}
	}
	return result, nil
}

// .
// .
// .
func withStatus(out, status string) string {
	// .
	// .
	// .
	// .
	out = strings.TrimRight(out, "\r\n")
	if out == "" {
		return "[" + status + "; no output]"
	}
	return out + "\n[" + status + "]"
}

// .
// .
// .
const shellOutputCap = 256 * 1024

// .
// .
// .
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
