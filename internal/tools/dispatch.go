package tools

import (
	"context"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/hold"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"maps"
	"os"
	"runtime/debug"
	"slices"
	"sort"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

type dispatchOwnerKey struct{}
type dispatchCheckKey struct{}

type DispatchTargetChangedError struct{}

func (*DispatchTargetChangedError) Error() string { return "dispatch target changed" }

const ReasonDispatchTargetChanged = "DISPATCH_TARGET_CHANGED"

type DispatchRefusedError struct {
	Code   string
	Detail string
}

func (e *DispatchRefusedError) Error() string { return e.Detail }

const ReasonArgumentsRequired = "ARGUMENTS_REQUIRED"

const ReasonUnknownArgument = "UNKNOWN_ARGUMENT"

const unknownArgumentWords = "%s does not take %s; nothing ran. It takes: %s."

func UnknownArguments(schema, args map[string]any) []string {
	props, _ := schema["properties"].(map[string]any)
	var unknown []string
	for k := range args {
		if _, ok := props[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	return unknown
}

func UnknownArgumentRefusal(tool string, unknown []string, schema map[string]any) Result {
	props, _ := schema["properties"].(map[string]any)
	takes := slices.Sorted(maps.Keys(props))
	return Refusal(ReasonUnknownArgument, fmt.Sprintf(unknownArgumentWords, tool, strings.Join(unknown, ", "), strings.Join(takes, ", ")))
}

type dispatchOwned interface{ DispatchOwner() any }

func WithDispatchOwner(ctx context.Context, owner any) context.Context {
	return context.WithValue(ctx, dispatchOwnerKey{}, owner)
}
func CheckDispatch(ctx context.Context) error {
	if f, ok := ctx.Value(dispatchCheckKey{}).(func() error); ok {
		return f()
	}
	return nil
}

func (r *Registry) Execute(ctx context.Context, name string, args map[string]any) (res Result, err error) {
	return r.execute(ctx, name, args, false)
}

func (r *Registry) ExecuteModel(ctx context.Context, name string, args map[string]any) (Result, error) {
	return r.execute(ctx, name, args, true)
}

func (r *Registry) execute(ctx context.Context, name string, args map[string]any, model bool) (res Result, err error) {
	source := ""

	defer func() {
		if p := recover(); p != nil {
			logsink.Error("tools.error", "tool %s panicked on model-authored arguments: %v\n%s", name, p, debug.Stack())
			message := fmt.Sprintf("tool %s failed internally: %v", name, p)
			if model && source != "" && source != "builtin" {
				message = untrusted.Wrap("plugin "+source, message)
			}
			res, err = Result{Error: message}, nil
		}
	}()

	t, ok, hostOnly, source := r.lookupWithHostOnly(name)
	expected := ctx.Value(dispatchOwnerKey{})
	if expected != nil {
		owned, yes := t.(dispatchOwned)
		if !yes || owned.DispatchOwner() != expected {
			return Refusal("HOST_OWNER_CHANGED", "host dispatch owner changed"), nil
		}
	}
	contextRead, _ := ctx.Value(contextSelectionKey{}).(bool)
	if contextRead && !r.contextEligible(name) {
		return Refusal("CONTEXT_NOT_SELECTED", "plugin context is not currently selected"), nil
	}

	ctx = context.WithValue(ctx, dispatchCheckKey{}, func() error {
		current, exists, _, _ := r.lookupWithHostOnly(name)
		if !exists || current != t {
			return &DispatchTargetChangedError{}
		}
		if contextRead && !r.contextEligible(name) {
			return &DispatchRefusedError{Code: "CONTEXT_NOT_SELECTED", Detail: "plugin context selection changed"}
		}
		if !r.ToolEnabled(name) {
			return &DispatchRefusedError{Code: ReasonOperatorDisabled, Detail: "tool disabled by operator"}
		}
		if r.safeSource != nil && source != "" && source != "builtin" {
			if h, safe := r.safeSource(); safe {
				return &DispatchRefusedError{Code: ReasonSafeSuspended, Detail: h.Name() + ": " + h.Reason}
			}
		}
		return nil
	})
	if model && hostOnly {
		return Refusal(ReasonHostOnly, name+" is host-only; only its host ceremony may call it"), nil
	}
	if !r.ToolEnabled(name) {
		return Refusal(ReasonOperatorDisabled, "access denied: tool disabled by operator"), nil
	}

	if msg := validateRequired(t, name, args); msg != "" {
		if !contextRead {
			r.malformedCalls.Add(1)
		}
		return Refusal(ReasonArgumentsRequired, msg), nil
	}

	if model && t != nil && source == "builtin" {
		if unknown := UnknownArguments(t.Parameters(), args); len(unknown) > 0 {
			r.malformedCalls.Add(1)
			return UnknownArgumentRefusal(name, unknown, t.Parameters()), nil
		}
	}

	if r.safeSource != nil && ok && !continuesInSafe(name, source) {
		if h, safe := r.safeSource(); safe {
			if h.Waiting {
				return Refusal(ReasonSafeSuspended, fmt.Sprintf("refused: this start is DEGRADED — %s. Only read-only tools continue %s.", h.Reason, hold.While)), nil
			}
			return Refusal(ReasonSafeSuspended, fmt.Sprintf("refused: I am in safe mode — %s. Only read-only tools continue while SAFE holds.", h.Reason)), nil
		}
	}

	look := r.lookFor(t)
	ctx = context.WithValue(ctx, callLook{}, look)

	if name == "read" || name == "write" || name == "edit" {
		for _, key := range []string{"file_path", "to"} {
			path, ok := args[key].(string)
			if !ok || key == "to" && (name != "write" || path == "") {
				continue
			}

			resolved := r.rooted(path)
			args[key] = resolved
			_, rel, err := r.admitOwn(look, name, resolved)

			if unchecked, ok := err.(*firewall.UncheckedError); ok &&
				name == "write" && key == "file_path" && args["action"] == "delete" && source == "builtin" {
				if link, undone, wall, ok := r.undo(look, unchecked, resolved); ok {
					resolved, args[key] = link, link
					rel, err = undone, nil

					ctx = context.WithValue(ctx, undoWall{}, wall)
				}
			}
			if err != nil {
				if key == "to" {
					return Refusal(ReasonPathRefused, "access denied: to "+err.Error()), nil
				}
				return Refusal(ReasonPathRefused, "access denied: "+err.Error()), nil
			}

			if action, _ := args["action"].(string); key == "file_path" && (action == "delete" || action == "move") {
				if rel == "." {
					return Refusal(ReasonRootItself, "access denied: "+resolved+" is the home or a granted root itself; what is inside it may be "+action+"d, not the root"), nil
				}
				if root := r.rootTakenBy(resolved); root != "" {
					return Refusal(ReasonRootInside, "access denied: "+resolved+" holds "+root+", the home or a granted root, which may not be "+action+"d with it; the operator removes that grant first"), nil
				}
			}
		}
		if r.afterAdmit != nil {
			r.afterAdmit()
		}
	}

	if name == "shell" {

		base, refused := r.shellBase(look, args)
		if refused != nil {
			return *refused, nil
		}
		if cmd, ok := args["command"].(string); ok {
			if why, outside, literal := r.shellWallWord(look, base, cmd); why != "" {

				hint := literalWordHint(literal)
				if literal == "" {
					hint = slashSearchHint(cmd, outside) + cdHint(cmd, outside, base) + notAPathHint(cmd, outside)
				}
				return Refusal(ReasonShellWall, "access denied: "+why+
					" (best-effort check; run under a container for a hard boundary)"+hint), nil
			}
		}
	}

	if name == "grep" || name == "ls" {
		key := "path"

		if v, given := args[key]; !given || v == nil || v == "" {
			args[key] = r.sandbox
		}
		if path, ok := args[key].(string); ok {
			resolved := r.rooted(path)
			args[key] = resolved

			if _, _, err := r.admitOwn(look, name, resolved); err != nil {
				return Refusal(ReasonPathRefused, "access denied: "+err.Error()), nil
			}
		}
	}

	if !ok {
		return Result{}, fmt.Errorf("%w: %s", ErrUnknownTool, name)
	}

	if name == "read" || name == "grep" || name == "ls" {
		key := "file_path"
		if name == "grep" || name == "ls" {
			key = "path"
		}
		if p, ok := args[key].(string); ok && p != "" {
			r.observePathSuspicion(name, p)
		}
	}
	result, runErr := t.Execute(ctx, args)
	if result.ReasonCode == ReasonArgumentsRequired && source == "builtin" {

		r.malformedCalls.Add(1)
	}
	if !model || source == "" || source == "builtin" {
		return result, runErr
	}
	label := "plugin " + source
	if runErr != nil {
		return result, &untrustedPluginError{cause: runErr, source: label}
	}
	if result.Output != "" {
		result.Output = untrusted.Wrap(label, result.Output)
	}
	if result.Error != "" {
		result.Error = untrusted.Wrap(label, result.Error)
	}
	return result, nil
}

func validateRequired(t Tool, name string, args map[string]any) string {
	if t == nil {
		return ""
	}

	var reqd []string
	switch v := t.Parameters()["required"].(type) {
	case nil:
		return ""
	case []string:
		reqd = v
	case []any:
		for i, e := range v {
			k, ok := e.(string)
			if !ok || k == "" {
				return fmt.Sprintf("malformed tool schema: required[%d] is not a non-empty string for %s", i, name)
			}
			reqd = append(reqd, k)
		}
	default:
		return fmt.Sprintf("malformed tool schema: required is %T, want an array of strings, for %s", v, name)
	}
	var missing []string
	for _, k := range reqd {
		if v, present := args[k]; !present || v == nil {
			missing = append(missing, k)
		}
	}
	if len(missing) == 0 {
		return ""
	}
	sort.Strings(missing)
	return fmt.Sprintf("malformed arguments: missing required %s for %s", strings.Join(missing, ", "), name)
}

func (r *Registry) MalformedCallCount() uint64 { return r.malformedCalls.Load() }

func (r *Registry) SuspiciousPathCount() uint64 { return r.suspiciousPathCalls.Load() }

func (r *Registry) observePathSuspicion(name, resolved string) {

	if _, err := os.Stat(resolved); err != nil {
		r.suspiciousPathCalls.Add(1)
	}
}

func (r *Registry) CountMalformed() { r.malformedCalls.Add(1) }
