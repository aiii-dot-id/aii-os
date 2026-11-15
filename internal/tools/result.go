package tools

import (
	"context"
	"errors"
	"io/fs"
	"syscall"

	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

type ResultImage struct {
	Path      string
	MediaType string

	Width, Height int
}

type Result struct {
	Output string

	Image     *ResultImage
	Truncated bool
	Error     string

	ReasonCode string

	Refused bool

	Interrupted bool

	ExitStatus *int

	AnsweredRefused bool
}

func (r Result) Failed() bool {
	return r.Error != "" || r.Interrupted || r.AnsweredRefused || r.ExitStatus != nil && *r.ExitStatus != 0
}

func Refusal(code, msg string) Result {
	return Result{Error: msg, ReasonCode: code, Refused: true}
}

const (
	ReasonNotFound        = "NOT_FOUND"
	ReasonPermission      = "PERMISSION_DENIED"
	ReasonNotADirectory   = "NOT_A_DIRECTORY"
	ReasonIsADirectory    = "IS_A_DIRECTORY"
	ReasonNotAFile        = "NOT_A_FILE"
	ReasonCancelled       = "CANCELLED"
	ReasonIO              = "IO_ERROR"
	ReasonArgumentInvalid = "ARGUMENT_INVALID"
	ReasonEditNoMatch     = "EDIT_NO_MATCH"
	ReasonEditNoChange    = "EDIT_NO_CHANGE"
	ReasonEditNotUnique   = "EDIT_NOT_UNIQUE"
)

func failedOn(err error) Result {
	return Result{Error: err.Error(), ReasonCode: osReason(err)}
}

func failed(code, msg string) Result { return Result{Error: msg, ReasonCode: code} }

func osReason(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return ReasonNotFound
	case errors.Is(err, fs.ErrPermission):
		return ReasonPermission
	case errors.Is(err, syscall.ENOTDIR):
		return ReasonNotADirectory
	case errors.Is(err, syscall.EISDIR):
		return ReasonIsADirectory
	case errors.Is(err, errNotRegular):
		return ReasonNotAFile
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return ReasonCancelled
	}
	return ReasonIO
}

const (
	ReasonPathRefused = "PATH_REFUSED"
	ReasonRootItself  = "ROOT_ITSELF"
	ReasonRootInside  = "ROOT_INSIDE"
	ReasonShellWall   = "SHELL_WALL"
)

const ReasonHostOnly = "HOST_ONLY"

type untrustedPluginError struct {
	cause  error
	source string
}

func (e *untrustedPluginError) Error() string { return untrusted.Wrap(e.source, e.cause.Error()) }
func (e *untrustedPluginError) Unwrap() error { return e.cause }

func (r Result) Text() string {
	text := r.Output
	if r.Error != "" {
		text = "Error: " + r.Error
	}
	if r.Truncated {
		return text + "\n[truncated]"
	}
	return text
}
