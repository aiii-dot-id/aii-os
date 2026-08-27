package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func (a *App) buildToolDefinitions() []llm.ToolDefinition {
	rawDefs := a.toolReg.ToolDefinitions()
	defs := make([]llm.ToolDefinition, 0, len(rawDefs))
	for _, raw := range rawDefs {
		m := raw.(map[string]interface{})
		fn := m["function"].(map[string]interface{})
		params, _ := fn["parameters"].(map[string]interface{})
		if params == nil {
			params = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
		}
		defs = append(defs, llm.ToolDefinition{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        fn["name"].(string),
				Description: fn["description"].(string),
				Parameters:  params,
			},
		})
	}

	// Identity verbs as tools — DERIVED from the canonical registry
	// (one source of truth: the tools verb, this schema, the prompt
	// prose, and the dashboard all render VerbRegistry; 2026-08-17).
	for _, v := range identity.Verbs() {
		defs = append(defs, llm.ToolDefinition{Type: "function", Function: llm.ToolFunction{
			Name:        v.Name,
			Description: v.Description,
			Parameters:  v.Params,
		}})
	}

	return defs
}

// executeToolCall runs a single tool call and returns the result string.
func (a *App) executeToolCall(ctx context.Context, tc llm.ToolCall) string {
	var args map[string]interface{}
	if tc.Function.Arguments != "" {
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			// Malformed arguments must not dispatch as a DIFFERENT call
			// than the one the model issued (live pattern 2026-08-22:
			// corrupted tool calls sailed through this seam as nil args
			// and failed downstream as path-policy errors). Reject with
			// a named error so the model can retry.
			log.Printf("app: malformed tool arguments for %s: %v (raw %.160q)", tc.Function.Name, err, tc.Function.Arguments)
			if a.toolReg != nil {
				a.toolReg.CountMalformed() // P3: count at the parse seam too
			}
			return fmt.Sprintf("Error: malformed tool arguments for %s: %v — reissue the call with valid JSON arguments", tc.Function.Name, err)
		}

		// P3: valid JSON that repeated an argument key dispatches on its
		// LAST copy. The parse seam above cannot see it (it parsed),
		// schema validation cannot (the key is present), and target-miss
		// telemetry sees only the subset whose surviving value also fails
		// to exist.
		//
		// Count every repetition; refuse only disagreement. Field scan
		// over 2036 production tool calls (AII_FIELD_LOG, 2026-08-25):
		// 21 repeated a key — 13 carried a drifted final copy, 8 restated
		// the same value verbatim. Refusing all 21 would block 8 calls
		// whose intent was never ambiguous; executing the 13 dispatches
		// on a value nobody chose and returns "not found" — an answer
		// that reads exactly like a fact about the world.
		if dups := tools.DuplicateArgKeys(tc.Function.Arguments); len(dups) > 0 {
			if a.toolReg != nil {
				a.toolReg.CountDuplicateArgKeys()
			}
			if conflicts := tools.ConflictingArgKeys(tc.Function.Arguments); len(conflicts) > 0 {
				log.Printf("app: conflicting argument keys %v in %s call — REFUSED, nothing executed (raw %.200q)", conflicts, tc.Function.Name, tc.Function.Arguments)
				return fmt.Sprintf("Error: argument key(s) %v appear more than once in this %s call with DIFFERENT values — the emission was corrupted and NOTHING was executed. Go decodes the LAST copy; in the field scan the last copy was the drifted one and the first copy was intact. Do not treat any earlier result from this call as a fact about the world. Reissue the call once, with each key appearing exactly once.", conflicts, tc.Function.Name)
			}
			log.Printf("app: duplicate argument keys %v in %s call — copies agree, dispatching normally (raw %.200q)", dups, tc.Function.Name, tc.Function.Arguments)
		}
	}

	name := tc.Function.Name
	if args == nil {
		args = map[string]interface{}{}
	}

	// Identity verbs — routed from the SAME registry that advertises
	// them. The capability surface has FOUR legs that must move
	// together: schema, registry description, charter prose, and THIS
	// dispatch. A hand-enumerated switch here listed four verbs while
	// identity.Verbs() advertised five, so a work call fell through to
	// the physical registry as "unknown tool: work" (live, 2026-08-18);
	// the switch's hand-rebuilt arg maps also silently dropped
	// advertised parameters (note.duplicate_ok and citations,
	// recall.after_seq, send.to=peer). Args pass through whole — the
	// verb layer owns its own argument reading.
	for _, v := range identity.Verbs() {
		if v.Name != name {
			continue
		}
		if name == "send" {
			if _, ok := args["to"]; !ok {
				args["to"] = "operator"
			}
		}
		result, err := a.engine.ExecuteAction(ctx, "verb", name, args)
		if err != nil {
			return fmt.Sprintf("Error: %v", err)
		}
		return result
	}

	// Physical tools
	result, err := a.toolReg.Execute(ctx, name, args)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	return result.Text()
}
