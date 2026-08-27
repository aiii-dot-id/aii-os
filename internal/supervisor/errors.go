package supervisor

import (
	"fmt"
	"strings"
)

// Typed refusals and failures (R39: every failure names its
// requirement). Everything the supervisor reports carries the plugin
// id — the operator reads these lines in the host log, out-of-band by
// design (threat model §7).

// SpawnRefusedError: the child was never started — artifact
// verification failed (the verified-bytes-are-loaded-bytes discipline,
// extended across restarts) or exec itself refused. Refusals stop the
// supervisor; they are not crashes and are never retried.
type SpawnRefusedError struct {
	PluginID string
	Cause    error
}

func (e *SpawnRefusedError) Error() string {
	return fmt.Sprintf("supervisor: plugin %s: spawn refused: %v", e.PluginID, e.Cause)
}
func (e *SpawnRefusedError) Unwrap() error { return e.Cause }

// ChildExitError: the child process exited. Code carries the exit
// status (-1 = signal-killed), Meaning the taxonomy rendering (the
// worker's documented contract on the worker lane), Phase where in the
// lifecycle it died, StderrTail the last out-of-band lines as bounded
// evidence.
type ChildExitError struct {
	PluginID   string
	Code       int
	Meaning    string
	Phase      string // start | run | invoke
	StderrTail []string
}

func (e *ChildExitError) Error() string {
	msg := fmt.Sprintf("supervisor: plugin %s: child exited during %s: code=%d (%s)",
		e.PluginID, e.Phase, e.Code, e.Meaning)
	if len(e.StderrTail) > 0 {
		msg += "; stderr tail: " + strings.Join(e.StderrTail, " | ")
	}
	return msg
}

// RestartCeilingError: the bounded restart policy ran out — the plugin
// is deactivated with the last failure as evidence. This is the typed
// reason the operator sees for a flapping plugin.
type RestartCeilingError struct {
	PluginID string
	Restarts int
	Last     error
}

func (e *RestartCeilingError) Error() string {
	return fmt.Sprintf("supervisor: plugin %s: deactivated after %d restarts (ceiling); last failure: %v",
		e.PluginID, e.Restarts, e.Last)
}
func (e *RestartCeilingError) Unwrap() error { return e.Last }

// UnavailableError: an invocation arrived while no child can serve it
// (restarting after a crash, or stopped). The tool call fails honestly
// and immediately — no queueing exists by design.
type UnavailableError struct {
	PluginID string
	State    State
	Reason   error // stop reason when State == StateStopped
}

func (e *UnavailableError) Error() string {
	if e.Reason != nil {
		return fmt.Sprintf("supervisor: plugin %s: unavailable (%s): %v", e.PluginID, e.State, e.Reason)
	}
	return fmt.Sprintf("supervisor: plugin %s: unavailable (%s)", e.PluginID, e.State)
}
func (e *UnavailableError) Unwrap() error { return e.Reason }

// ProtocolError: the child broke the wire contract (non-JSON frame, an
// id not echoed byte-form verbatim, both-or-neither of result|error).
// The stream has no resync, so the child is killed and restarted.
type ProtocolError struct {
	PluginID    string
	Requirement string
	Evidence    string
}

func (e *ProtocolError) Error() string {
	return fmt.Sprintf("supervisor: plugin %s: child violated the wire contract (%s); got: %s",
		e.PluginID, e.Requirement, e.Evidence)
}

// ChildIOError: a stream read or write failed mid-invocation — the
// framed connection is dead (no resync exists) and the child was
// killed for restart.
type ChildIOError struct {
	PluginID string
	Op       string
	Err      error
}

func (e *ChildIOError) Error() string {
	return fmt.Sprintf("supervisor: plugin %s: stream %s failed: %v", e.PluginID, e.Op, e.Err)
}
func (e *ChildIOError) Unwrap() error { return e.Err }

// InvokeTimeoutError: the invocation deadline passed — the adopted
// cancellation model killed the child (DELTA_D1 N-8: no in-band cancel
// exists; kill and restart, the identity survives).
type InvokeTimeoutError struct {
	PluginID string
	Err      error
}

func (e *InvokeTimeoutError) Error() string {
	return fmt.Sprintf("supervisor: plugin %s: invocation deadline exceeded; child killed for restart: %v", e.PluginID, e.Err)
}
func (e *InvokeTimeoutError) Unwrap() error { return e.Err }
