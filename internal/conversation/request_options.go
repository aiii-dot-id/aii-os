package conversation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

type RequestOptions struct {
	AllowedTools   *[]string           `json:"allowed_tools,omitempty"`
	ResponseFormat *llm.ResponseFormat `json:"response_format,omitempty"`
}

type requestOptionsKey struct{}

type RequestOptionsError struct {
	Reason string
	Err    error
}

func (e *RequestOptionsError) Error() string { return "request options: " + e.Reason }
func (e *RequestOptionsError) Unwrap() error { return e.Err }

func WithRequestOptions(ctx context.Context, in RequestOptions) (context.Context, error) {
	if in.AllowedTools == nil && in.ResponseFormat == nil {
		return ctx, &RequestOptionsError{Reason: "names no limit: give allowed_tools, response_format, or neither member and no request_options"}
	}
	out := RequestOptions{}
	if in.AllowedTools != nil {
		names := append(make([]string, 0, len(*in.AllowedTools)), (*in.AllowedTools)...)
		seen := make(map[string]struct{}, len(names))
		for _, name := range names {
			if name == "" {
				return ctx, &RequestOptionsError{Reason: "allowed tool name is empty"}
			}
			if _, twice := seen[name]; twice {
				return ctx, &RequestOptionsError{Reason: fmt.Sprintf("duplicate allowed tool %q", name)}
			}
			seen[name] = struct{}{}
		}
		out.AllowedTools = &names
	}
	if in.ResponseFormat != nil {
		format, err := llm.CloneResponseFormat(in.ResponseFormat)
		if err != nil {
			return ctx, &RequestOptionsError{Reason: err.Error(), Err: err}
		}
		out.ResponseFormat = format
	}
	ctx = context.WithValue(ctx, requestOptionsKey{}, out)

	told, err := LimitsNote(ctx, nil)
	if err != nil {
		return ctx, err
	}
	if scrubTurnMarkers(told) != told {
		return ctx, &RequestOptionsError{Reason: "the schema holds text reserved for the turn's own block markers"}
	}
	return ctx, nil
}

func RequestOptionsFromContext(ctx context.Context) (RequestOptions, bool) {
	opts, ok := ctx.Value(requestOptionsKey{}).(RequestOptions)
	return opts, ok
}

func FilterRequestTools(ctx context.Context, defs []llm.ToolDefinition) ([]llm.ToolDefinition, error) {
	opts, ok := RequestOptionsFromContext(ctx)
	if !ok || opts.AllowedTools == nil {
		return append([]llm.ToolDefinition(nil), defs...), nil
	}
	known := make(map[string]struct{}, len(defs))
	for _, d := range defs {
		if _, twice := known[d.Function.Name]; twice {
			return nil, &RequestOptionsError{Reason: fmt.Sprintf("duplicate registered tool %q", d.Function.Name)}
		}
		known[d.Function.Name] = struct{}{}
	}
	allowed := make(map[string]struct{}, len(*opts.AllowedTools))
	for _, name := range *opts.AllowedTools {
		if _, exists := known[name]; !exists {
			return nil, &RequestOptionsError{Reason: fmt.Sprintf("allowed tool %q is not registered", name)}
		}
		allowed[name] = struct{}{}
	}
	out := make([]llm.ToolDefinition, 0, len(allowed))
	for _, d := range defs {
		if _, ok := allowed[d.Function.Name]; ok {
			out = append(out, d)
		}
	}
	return out, nil
}

func requestScoped(ctx context.Context) bool {
	opts, ok := RequestOptionsFromContext(ctx)
	return ok && opts.AllowedTools != nil
}

func requestResponseFormat(ctx context.Context) *llm.ResponseFormat {
	opts, _ := RequestOptionsFromContext(ctx)
	return opts.ResponseFormat
}

func LimitsNote(ctx context.Context, offered []llm.ToolDefinition) (string, error) {
	opts, ok := RequestOptionsFromContext(ctx)
	if !ok {
		return "", nil
	}
	lines := []string{"### Limits on this request (this turn only)"}
	if opts.AllowedTools != nil {
		names := make([]string, 0, len(offered))
		for _, d := range offered {
			names = append(names, d.Function.Name)
		}
		if len(names) == 0 {
			names = []string{"none"}
		}
		lines = append(lines, "Tools offered: "+strings.Join(names, ", ")+".")
	}
	if f := opts.ResponseFormat; f != nil && f.JSONSchema != nil {

		var schema bytes.Buffer
		enc := json.NewEncoder(&schema)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(f.JSONSchema.Schema); err != nil {
			return "", &RequestOptionsError{Reason: "the answer's schema cannot be shown to the identity: " + err.Error(), Err: err}
		}
		lines = append(lines, fmt.Sprintf("Answer: one JSON value fitting schema %q: %s.", f.JSONSchema.Name, strings.TrimSuffix(schema.String(), "\n")))
	}
	return strings.Join(lines, "\n"), nil
}

const ReasonToolOutsideRequest = "TOOL_OUTSIDE_REQUEST"

func toolOutsideRequest(name string) string {
	return declare("%s is not offered this turn: this request limits the tools offered", name)
}
