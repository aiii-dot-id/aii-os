package pluginhost

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
type VariantRefusal struct {
	VariantID string
	Missing   []string
}

// .
// .
// .
// .
// .
type VariantSelectionError struct {
	PluginID  string
	Host      string
	Refusals  []VariantRefusal
	Ambiguous []string
}

func (e *VariantSelectionError) Error() string {
	if len(e.Ambiguous) > 0 {
		return fmt.Sprintf("pluginhost: plugin %s: %d variants are selectable on %s (%s) and default_variant does not decide — refusing to guess (the publisher's declaration owns precedence)",
			e.PluginID, len(e.Ambiguous), e.Host, strings.Join(e.Ambiguous, ", "))
	}
	var per []string
	for _, r := range e.Refusals {
		per = append(per, fmt.Sprintf("%s: missing %s", r.VariantID, strings.Join(r.Missing, "; ")))
	}
	if len(per) == 0 {
		per = append(per, "no variants declared")
	}
	return fmt.Sprintf("pluginhost: plugin %s: no selectable variant on %s — %s",
		e.PluginID, e.Host, strings.Join(per, " | "))
}

// .
// .
// .
// .
// .
// .
type EntrypointDigestError struct {
	Member string
	Want   string
	Got    string
}

func (e *EntrypointDigestError) Error() string {
	if e.Want == "" {
		return fmt.Sprintf("pluginhost: entrypoint %q has no digest in the verified result; refusing to load unverified bytes", e.Member)
	}
	return fmt.Sprintf("pluginhost: entrypoint %q extracted bytes hash to %s, verified result says %s; refusing to load what was not verified", e.Member, e.Got, e.Want)
}

// .
// .
// .
// .
// .
type ToolNameError struct {
	PluginID string
	Name     string
	Reason   string
}

func (e *ToolNameError) Error() string {
	return fmt.Sprintf("pluginhost: plugin %s tool %q: %s", e.PluginID, e.Name, e.Reason)
}

// .
// .
// .
// .
// .
// .
// .
// .
type ResponseContractError struct {
	Requirement string
	Got         string
}

func (e *ResponseContractError) Error() string {
	return fmt.Sprintf("pluginhost: guest reply violates the JSON-RPC response contract (%s); got: %s", e.Requirement, e.Got)
}
