package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// .
// .
// .
// .
// .
// .
// .
// .
const PromptToolDefinitionCeiling = 32

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

	// .
	// .
	// .
	for _, v := range identity.Verbs() {
		defs = append(defs, llm.ToolDefinition{Type: "function", Function: llm.ToolFunction{
			Name:        v.Name,
			Description: v.Description,
			Parameters:  v.Params,
		}})
	}

	return defs
}

// .
// .
// .
// .
// .
// .
func readOnlyToolCall(name, argsJSON string) bool {
	switch name {
	case "read", "grep", "ls", "recall", "memory.search", "memory.recent", "memory.stats", "memory.get":
		return true
	case "work":
		// .
		// .
		// .
		var a struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal([]byte(argsJSON), &a); err != nil {
			return false
		}
		return a.Action == "status" || a.Action == "measure"
	case "shell":
		var a struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal([]byte(argsJSON), &a); err != nil || strings.TrimSpace(a.Command) == "" {
			return false
		}
		return readOnlyShellCommand(a.Command)
	default:
		return false
	}
}

// .
// .
// .
// .
// .
func readOnlyShellCommand(command string) bool {
	fields := strings.Fields(command)
	i := 0
	// .
	for i < len(fields) && fields[i] == "cd" {
		j := i + 1
		if j < len(fields) && fields[j] != "&&" && fields[j] != ";" {
			j++
		}
		for j < len(fields) && fields[j] != "&&" && fields[j] != ";" {
			j++
		}
		i = j + 1
	}
	if i >= len(fields) {
		return false
	}
	verb := fields[i]
	switch verb {
	case "cat", "head", "tail", "rg", "grep", "find", "ls", "wc", "file":
		return true
	case "git":
		if i+1 < len(fields) {
			switch fields[i+1] {
			case "status", "log", "diff", "show", "branch":
				return true
			}
		}
		return false
	default:
		return false
	}
}

// .
func (a *App) executeToolCall(ctx context.Context, tc llm.ToolCall) conversation.Observation {
	var args map[string]interface{}
	if tc.Function.Arguments != "" {
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			// .
			// .
			// .
			// .
			// .
			logsink.Warn("tool.refusal", "malformed tool arguments for %s: %v (raw %.160q)", tc.Function.Name, err, tc.Function.Arguments)
			if a.toolReg != nil {
				a.toolReg.CountMalformed()
			}
			return conversation.Observation{Text: fmt.Sprintf("Error: malformed tool arguments for %s: %v — reissue the call with valid JSON arguments", tc.Function.Name, err), Failed: true}
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
		// .
		if dups := tools.DuplicateArgKeys(tc.Function.Arguments); len(dups) > 0 {
			if a.toolReg != nil {
				a.toolReg.CountDuplicateArgKeys()
			}
			if conflicts := tools.ConflictingArgKeys(tc.Function.Arguments); len(conflicts) > 0 {
				logsink.Warn("tool.refusal", "conflicting argument keys %v in %s call — REFUSED, nothing executed (raw %.200q)", conflicts, tc.Function.Name, tc.Function.Arguments)
				return conversation.Observation{Text: fmt.Sprintf("Error: argument key(s) %v appear more than once in this %s call with DIFFERENT values — the emission was corrupted and NOTHING was executed. Go decodes the LAST copy; in the field scan the last copy was the drifted one and the first copy was intact. Do not treat any earlier result from this call as a fact about the world. Reissue the call once, with each key appearing exactly once.", conflicts, tc.Function.Name), Failed: true}
			}
			logsink.Info("tool.decision", "duplicate argument keys %v in %s call — copies agree, dispatching normally (raw %.200q)", dups, tc.Function.Name, tc.Function.Arguments)
		}
	}

	name := tc.Function.Name
	if args == nil {
		args = map[string]interface{}{}
	}

	// .
	// .
	// .
	// .
	// .
	// .
	if a.toolReg != nil {
		if state, reason, ok := a.toolReg.State(name); ok && state != tools.StateOffered {
			detail := string(state)
			if reason != "" {
				detail += ": " + reason
			}
			return conversation.Observation{Text: fmt.Sprintf("Error: %s is not in your active offer (%s) — inspect it with tools action=show name=%s, then tools action=offer name=%s to make it callable from your next turn.", name, detail, name, name), Failed: true}
		}
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
	subSession, isSub := ctx.Value(identity.SubagentWorkSession{}).(string)
	countExecuted := func() {
		if isSub {
			// .
			// .
			if name == "work" {
				a.noteSubagentWorkCall(subSession, tc.Function.Arguments)
			}
			return
		}
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		a.countToolCall(name, tc.Function.Arguments)
		if name == "work" {
			a.countSuccessfulWorkCall(tc.Function.Arguments, tc.EmissionOrdinal)
		}
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
			return conversation.Observation{Text: fmt.Sprintf("Error: %v", err), Failed: true}
		}
		if name == "work" {
			// .
			// .
			// .
			// .
			if a.dashboard != nil {
				a.dashboard.BroadcastWork()
			}
			// .
			// .
			// .
			// .
			if act, ok := args["action"].(string); ok && act == "yield" {
				countExecuted()
				answer, _ := args["answer"].(string)
				return conversation.Observation{Text: result, EndTurn: true, EndTurnAnswer: strings.TrimSpace(answer)}
			}
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			// .
			if act, ok := args["action"].(string); ok && act == "deliver" {
				if sid, _ := ctx.Value(identity.SubagentWorkSession{}).(string); sid != "" {
					countExecuted()
					return conversation.Observation{Text: result, EndTurn: true}
				}
			}
		}
		countExecuted()
		return conversation.Observation{Text: result}
	}

	// .
	// .
	// .
	// .
	started := time.Now()
	result, err := a.toolReg.Execute(ctx, name, args)
	if err != nil {
		// .
		return conversation.Observation{Text: fmt.Sprintf("Error: %v", err), Failed: true}
	}
	countExecuted()
	// .
	// .
	// .
	// .
	// .
	// .
	actor, session := "main", ""
	if isSub {
		actor, session = "subagent", subSession
	} else {
		if a.currentMode() == ModeSafe {
			actor = "safe"
		}
		if a.store != nil {
			if ws, werr := a.store.ActiveWorkSession(); werr == nil && ws != nil {
				session = ws.ID
			}
		}
	}
	a.emitPluginEvent(pluginhost.TopicToolCalled, map[string]interface{}{"tool": name, "failed": result.Error != "", "duration_ms": time.Since(started).Milliseconds(), "actor": actor, "session": session})
	return conversation.Observation{Text: result.Text(), Failed: result.Error != "", Truncated: result.Truncated}
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
func actToolCall(name, argsJSON string) bool {
	switch name {
	case "work", "tools":
		return false
	case "shell":
		var a struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal([]byte(argsJSON), &a); err != nil || strings.TrimSpace(a.Command) == "" {
			return true
		}
		return !readOnlyShellChain(a.Command)
	}
	if strings.HasPrefix(name, "pl_") {
		if i := strings.LastIndex(name, "_"); i >= 0 {
			switch name[i+1:] {
			case "search", "get", "recent", "stats", "health", "list", "query":
				return false
			}
		}
	}
	return !readOnlyToolCall(name, argsJSON)
}

// .
// .
// .
// .
// .
// .
// .
func readOnlyShellChain(command string) bool {
	decided := false
	for _, seg := range splitShellChain(command) {
		fields := strings.Fields(seg)
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "cd" && len(fields) <= 2 {
			continue
		}
		if redirectsIntoFile(seg) || !readOnlyShellCommand(seg) {
			return false
		}
		decided = true
	}
	return decided
}

// .
// .
func splitShellChain(command string) []string {
	var out []string
	start := 0
	for i := 0; i < len(command); i++ {
		switch {
		case strings.HasPrefix(command[i:], "&&"), strings.HasPrefix(command[i:], "||"):
			out = append(out, command[start:i])
			i++
			start = i + 1
		case command[i] == ';', command[i] == '|':
			out = append(out, command[start:i])
			start = i + 1
		}
	}
	return append(out, command[start:])
}

// .
// .
func redirectsIntoFile(seg string) bool {
	for i := 0; i < len(seg); i++ {
		if seg[i] != '>' {
			continue
		}
		j := i + 1
		for j < len(seg) && seg[j] == '>' {
			j++
		}
		rest := strings.TrimSpace(seg[j:])
		if rest == "" || strings.HasPrefix(rest, "&") || strings.HasPrefix(rest, "/dev/null") {
			i = j
			continue
		}
		return true
	}
	return false
}
