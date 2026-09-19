package supervisor

import (
	"fmt"
	"strings"
)

// .
// .
// .
// .

// .
// .
// .
// .
type SpawnRefusedError struct {
	PluginID string
	Cause    error
}

func (e *SpawnRefusedError) Error() string {
	return fmt.Sprintf("supervisor: plugin %s: spawn refused: %v", e.PluginID, e.Cause)
}
func (e *SpawnRefusedError) Unwrap() error { return e.Cause }

// .
// .
// .
// .
// .
type ChildExitError struct {
	PluginID   string
	Code       int
	Meaning    string
	Phase      string
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

// .
// .
// .
// .
// .
// .
type StartCancelledError struct {
	PluginID   string
	Cause      error
	StderrTail []string
}

func (e *StartCancelledError) Error() string {
	msg := fmt.Sprintf("supervisor: plugin %s: start cancelled before readiness (%v); the child was killed", e.PluginID, e.Cause)
	if len(e.StderrTail) > 0 {
		msg += "; stderr tail: " + strings.Join(e.StderrTail, " | ")
	}
	return msg
}
func (e *StartCancelledError) Unwrap() error { return e.Cause }

// .
// .
// .
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

// .
// .
// .
type UnavailableError struct {
	PluginID string
	State    State
	Reason   error
}

func (e *UnavailableError) Error() string {
	if e.Reason != nil {
		return fmt.Sprintf("supervisor: plugin %s: unavailable (%s): %v", e.PluginID, e.State, e.Reason)
	}
	return fmt.Sprintf("supervisor: plugin %s: unavailable (%s)", e.PluginID, e.State)
}
func (e *UnavailableError) Unwrap() error { return e.Reason }

// .
// .
// .
type ProtocolError struct {
	PluginID    string
	Requirement string
	Evidence    string
}

func (e *ProtocolError) Error() string {
	return fmt.Sprintf("supervisor: plugin %s: child violated the wire contract (%s); got: %s",
		e.PluginID, e.Requirement, e.Evidence)
}

// .
// .
// .
type ChildIOError struct {
	PluginID string
	Op       string
	Err      error
}

func (e *ChildIOError) Error() string {
	return fmt.Sprintf("supervisor: plugin %s: stream %s failed: %v", e.PluginID, e.Op, e.Err)
}
func (e *ChildIOError) Unwrap() error { return e.Err }

// .
// .
// .
type InvokeTimeoutError struct {
	PluginID string
	Err      error
}

func (e *InvokeTimeoutError) Error() string {
	return fmt.Sprintf("supervisor: plugin %s: invocation deadline exceeded; child killed for restart: %v", e.PluginID, e.Err)
}
func (e *InvokeTimeoutError) Unwrap() error { return e.Err }
