package pluginhost

import (
	"fmt"
	"strings"
)

// Typed activation and invocation refusals (R39 pattern: every failure
// names its requirement). Package-verification failures pass through as
// *packagefmt.Error; worker containment failures pass through as
// pluginworker's taxonomy; supervised-channel failures as the
// supervisor's; these are the harness's own.

// VariantRefusal records why one declared variant was not selectable
// on this host: the missing predicates verbatim (the C
// missing_requirements spelling) plus host-fact mismatches in prose.
type VariantRefusal struct {
	VariantID string
	Missing   []string
}

// VariantSelectionError is the typed selection refusal (PLATFORM_SEAMS
// §3.3: "zero selectable variants is a typed refusal naming the
// missing predicates" — the operator must see exactly why). Ambiguous
// carries the C daemon's other refusal shape: several selectable
// variants and no deciding default_variant (manifest.c:1647-1650).
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

// EntrypointDigestError reports the verified-bytes-are-loaded-bytes
// invariant failing: the entrypoint bytes extracted for loading do not
// hash to the digest the verified Result recorded (or the verified
// Result records no digest for that member at all — Want empty). The
// window it closes is the file changing on disk between the verify
// pass and the read pass: refuse, never load.
type EntrypointDigestError struct {
	Member string
	Want   string // verified digest; "" when the member is not in FileDigests
	Got    string // digest of the extracted bytes; "" when nothing was extracted
}

func (e *EntrypointDigestError) Error() string {
	if e.Want == "" {
		return fmt.Sprintf("pluginhost: entrypoint %q has no digest in the verified result; refusing to load unverified bytes", e.Member)
	}
	return fmt.Sprintf("pluginhost: entrypoint %q extracted bytes hash to %s, verified result says %s; refusing to load what was not verified", e.Member, e.Got, e.Want)
}

// ToolNameError reports a tool name the harness cannot register: over
// the provider ceiling, a duplicate within the plugin's own manifest,
// or a collision with a name already in the registry. Activation fails
// closed — a silent rename would break the operator's ability to
// attribute a tool to its signed manifest.
type ToolNameError struct {
	PluginID string
	Name     string
	Reason   string
}

func (e *ToolNameError) Error() string {
	return fmt.Sprintf("pluginhost: plugin %s tool %q: %s", e.PluginID, e.Name, e.Reason)
}

// ResponseContractError reports a guest reply that is not a well-formed
// JSON-RPC response to the harness's request: wrong or missing jsonrpc,
// an id not echoed verbatim, a method member (a request is not a
// response), neither or both of result|error, or not JSON at all
// (BBB_V2_AUDIT §4 response rules). Got carries a bounded excerpt of
// what actually came back — the error names the evidence, and for a
// denied capability call that excerpt IS the audited denial the guest
// relayed.
type ResponseContractError struct {
	Requirement string
	Got         string
}

func (e *ResponseContractError) Error() string {
	return fmt.Sprintf("pluginhost: guest reply violates the JSON-RPC response contract (%s); got: %s", e.Requirement, e.Got)
}
