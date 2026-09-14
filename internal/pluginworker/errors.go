package pluginworker

import (
	"errors"
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
// .
// .
// .
// .
// .
// .
type ArtifactFormatError struct {
	Offset int
	Detail string
}

func (e *ArtifactFormatError) Error() string {
	return fmt.Sprintf("pluginworker: malformed wasm artifact at byte %d: %s", e.Offset, e.Detail)
}

// .
// .
// .
// .
// .
type ArtifactTooLargeError struct {
	What  string
	Size  int
	Limit int
}

func (e *ArtifactTooLargeError) Error() string {
	return fmt.Sprintf("pluginworker: %s is %d bytes, above the %d-byte admission ceiling", e.What, e.Size, e.Limit)
}

// .
// .
// .
// .
// .
// .
// .
// .
type NestedComponentError struct {
	Offset int
}

func (e *NestedComponentError) Error() string {
	return fmt.Sprintf("pluginworker: component embeds a nested component (section id 4 at byte %d); the worker unwraps one layer only", e.Offset)
}

// .
// .
// .
type NoCandidateModuleError struct {
	EmbeddedModules int
}

func (e *NoCandidateModuleError) Error() string {
	return fmt.Sprintf("pluginworker: component embeds %d core module(s), none exporting the world surface (%s)",
		e.EmbeddedModules, strings.Join(worldSurfaceExports, ", "))
}

// .
// .
// .
// .
type AmbiguousCandidateError struct {
	Modules         []int
	EmbeddedModules int
}

func (e *AmbiguousCandidateError) Error() string {
	return fmt.Sprintf("pluginworker: component embeds %d core modules and more than one (%v, by section order) exports the world surface; refusing to guess which guest runs",
		e.EmbeddedModules, e.Modules)
}

// .
// .
// .
// .
// .
// .
type ForbiddenImportError struct {
	Module string
	Name   string
	Reason string
}

func (e *ForbiddenImportError) Error() string {
	return fmt.Sprintf("pluginworker: forbidden import %q.%q: %s", e.Module, e.Name, e.Reason)
}

// .
// .
// .
// .
// .
// .
// .
// .
type ExportError struct {
	Name   string
	Reason string
}

func (e *ExportError) Error() string {
	return fmt.Sprintf("pluginworker: guest export %q: %s", e.Name, e.Reason)
}

// .
// .
// .
// .
// .
// .
// .
type ProtocolVersionError struct {
	Got uint32
}

func (e *ProtocolVersionError) Error() string {
	return fmt.Sprintf("pluginworker: guest bbb_protocol_version %d, require %d (audited manifest const)",
		e.Got, RequiredProtocolVersion)
}

// .
// .
// .
type SmokeCodeError struct {
	Got uint32
}

func (e *SmokeCodeError) Error() string {
	return fmt.Sprintf("pluginworker: guest smoke code %d, require %d", e.Got, RequiredSmokeCode)
}

// .
// .
// .
// .
// .
type TrapError struct {
	Reason string
}

func (e *TrapError) Error() string {
	return fmt.Sprintf("pluginworker: guest trap: %s", e.Reason)
}

// .
// .
// .
// .
// .
// .
type TimeoutError struct {
	Err error
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("pluginworker: invocation terminated by deadline/cancellation: %v", e.Err)
}

func (e *TimeoutError) Unwrap() error { return e.Err }

// .
// .
// .
// .
// .
// .
// .
// .
// .
type ResourceLimitError struct {
	LimitBytes uint64
	Cause      error
}

func (e *ResourceLimitError) Error() string {
	return fmt.Sprintf("pluginworker: guest exceeded resource envelope (memory cap %d bytes): %v", e.LimitBytes, e.Cause)
}

func (e *ResourceLimitError) Unwrap() error { return e.Cause }

// .
// .
// .
// .
// .
// .
type FrameTooLargeError struct {
	Direction string
	Size      int
	Limit     int
}

func (e *FrameTooLargeError) Error() string {
	return fmt.Sprintf("pluginworker: %s payload %d bytes exceeds the %d-byte plugin-side ceiling", e.Direction, e.Size, e.Limit)
}

// .
// .
// .
// .
type AbiError struct {
	Detail string
}

func (e *AbiError) Error() string {
	return fmt.Sprintf("pluginworker: guest violated canonical ABI: %s", e.Detail)
}

// .
// .
// .
// .
// .
type ModuleUnusableError struct {
	Cause error
}

func (e *ModuleUnusableError) Error() string {
	return fmt.Sprintf("pluginworker: module retired by earlier fatal error: %v", e.Cause)
}

func (e *ModuleUnusableError) Unwrap() error { return e.Cause }

// .
// .
// .
// .
var ErrNoOnEvent = errors.New("pluginworker: guest does not export on_event")

// .
// .
// .
// .
// .
// .
// .
type FatalDispatchError struct{ Err error }

func (e *FatalDispatchError) Error() string { return e.Err.Error() }
func (e *FatalDispatchError) Unwrap() error { return e.Err }
