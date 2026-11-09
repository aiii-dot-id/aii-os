package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

const PromptToolDefinitionCeiling = 32

func registryRoom() int { return PromptToolDefinitionCeiling - len(identity.Verbs()) }

func (a *App) buildToolDefinitions() []llm.ToolDefinition {
	_, safe := a.SafeMode()
	return a.buildToolDefinitionsIn(safe)
}

func (a *App) buildToolDefinitionsIn(safe bool) []llm.ToolDefinition {
	var cfg Config
	a.cfgMu.RLock()
	if a.cfg != nil {
		cfg = *a.cfg
	}
	a.cfgMu.RUnlock()
	return a.toolDefinitionsIn(a.residentLimitsFor(cfg).cfg.Agency.checkpoint(), safe)
}

func (a *App) toolDefinitions(turn conversation.Checkpoint) []llm.ToolDefinition {
	_, safe := a.SafeMode()
	return a.toolDefinitionsIn(turn, safe)
}

func (a *App) toolDefinitionsIn(turn conversation.Checkpoint, safe bool) []llm.ToolDefinition {
	defs := a.toolReg.ToolDefinitionsIn(registryRoom(), safe)

	var roles map[string]string
	a.cfgMu.RLock()
	if a.cfg != nil {
		roles = a.cfg.Agency.roleNotes()
	}
	a.cfgMu.RUnlock()
	for _, v := range identity.OfferedVerbsAt(turn, safe, roles) {
		defs = append(defs, llm.ToolDefinition{Type: "function", Function: llm.ToolFunction{
			Name:        v.Name,
			Description: v.Description,
			Parameters:  v.Params,
		}})
	}

	return defs
}

func readOnlyToolCall(name string, args map[string]any) bool {
	switch name {
	case "read", "grep", "ls", "recall":
		return true
	case "work":

		action, _ := args["action"].(string)
		return action == "status" || action == "measure"
	case "shell":
		command, _ := args["command"].(string)
		return strings.TrimSpace(command) != "" && readOnlyShellCommand(command)
	default:
		return false
	}
}

func readOnlyShellCommand(command string) bool {
	fields := strings.Fields(command)
	i := 0

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

func (a *App) executeSeated(ctx context.Context, tc llm.ToolCall, seat callSeat) (obs conversation.Observation) {
	defer func() {
		if obs.WorkSession == "" && obs.SessionReason == "" {
			obs.WorkSession, obs.SessionReason = seat.session, seat.reason
		}
	}()
	var args map[string]any
	if tc.Function.Arguments != "" {
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {

			logsink.Warn("tool.refusal", "malformed tool arguments for %s: %v (raw %.160q)", tc.Function.Name, err, tc.Function.Arguments)
			if a.toolReg != nil {
				a.toolReg.CountMalformed()
			}
			return conversation.Observation{Text: fmt.Sprintf("Error: malformed tool arguments for %s: %v — reissue the call with valid JSON arguments", tc.Function.Name, err), Failed: true}
		}

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

	if args == nil {
		args = map[string]any{}
	}

	call, inner, routed, err := callRoute(a.toolReg, tc, args)
	if err != nil {
		obs := conversation.Observation{Text: "Error: " + err.Error(), Failed: true, Refused: true}
		if errors.Is(err, errCallArgs) {
			obs.ReasonCode = tools.ReasonArgumentsRequired
			obs.Text += a.callCard(call.Function.Name)
		}
		return obs
	}
	tc, args = call, inner
	name := tc.Function.Name

	if a.toolReg != nil {
		if code, reason, ok := a.toolReg.Unavailable(name); ok {
			shown, inTurn := conversation.ShownThisTurn(ctx, name)
			var res tools.Result
			switch {
			case code != "":
				res = tools.Refusal(code, fmt.Sprintf("%s is unavailable (%s); nothing ran.", name, reason))
			case !routed && inTurn && !shown:
				res = tools.Refusal(tools.ReasonNotInToolList, fmt.Sprintf("%s is not in your tool list; call it with tools action=call name=%s args={…} (tools action=show name=%s gives its arguments). Nothing ran.", name, name, name))
			}
			if res.Refused {
				return conversation.Observation{Text: res.Text(), Failed: true, Refused: true, ReasonCode: res.ReasonCode}
			}
		}
	}

	countExecuted := func() {
		if seat.actor == actorSubagent {

			if name == "work" {
				a.noteSubagentWorkCall(seat.leg, args, tc.EmissionOrdinal)
			}
			return
		}

		a.countToolCall(name, args)
		if name == "work" {
			a.countSuccessfulWorkCall(args, tc.EmissionOrdinal)
		}
	}

	for _, v := range identity.Verbs() {
		if v.Name != name {
			continue
		}

		unknown := slices.DeleteFunc(tools.UnknownArguments(v.Params, args), func(k string) bool { return slices.Contains(v.Retired, k) })
		if len(unknown) > 0 {
			if a.toolReg != nil {
				a.toolReg.CountMalformed()
			}
			res := tools.UnknownArgumentRefusal(name, unknown, v.Params)
			return conversation.Observation{Text: res.Text(), Failed: true, Refused: true, ReasonCode: res.ReasonCode}
		}
		var started string
		if name == "work" {
			ctx = context.WithValue(ctx, identity.StartedSession{}, &started)
		}
		result, err := a.engine.ExecuteAction(ctx, "verb", name, args)
		if err != nil {
			obs := conversation.Observation{Text: fmt.Sprintf("Error: %v", err), Failed: true, ReasonCode: identity.ReasonCode(name, args, err)}

			var rejection *identity.Rejection
			var refusal interface{ RefusedBeforeEffect() bool }
			if errors.As(err, &rejection) || errors.As(err, &refusal) && refusal.RefusedBeforeEffect() {
				obs.Refused = true
			}
			var size *identity.WorkStatusSizeError
			if errors.As(err, &size) {
				obs.Refused, obs.ReasonCode = true, "status.size"
				if utf8.RuneCountInString(obs.Text) > a.engine.ResultLimit(ctx) {

					obs.HostNote = obs.Text
				}
			}
			return obs
		}
		if name == "work" && a.store != nil {
			_, steps, independent := workDeclaration(args)
			if steps > 0 || independent > 0 {
				if seat.lookup != nil {
					return conversation.Observation{Text: "Work changed, but forecast recording could not read its session: " + seat.lookup.Error(), Failed: true}
				}
				session := seat.session
				if act, _ := args["action"].(string); act == "start" || act == "" {

					if started == "" {
						return conversation.Observation{Text: "Work started, but forecast recording could not read its session: the start named none", Failed: true}
					}
					session = started
				}
				var sp, ip *int
				if steps > 0 {
					sp = &steps
				}
				if independent > 0 {
					ip = &independent
				}
				if err := a.store.RecordWorkForecast(session, interaction.TurnID(ctx), sp, ip); err != nil {
					return conversation.Observation{Text: "Work changed, but forecast was not recorded: " + err.Error(), Failed: true}
				}
			}
		}
		if name == "work" {

			if a.dashboard != nil {
				a.dashboard.BroadcastWork()
			}

			if act, ok := args["action"].(string); ok && act == "yield" {
				countExecuted()
				answer, _ := args["answer"].(string)
				return conversation.Observation{Text: result, EndTurn: true, EndTurnAnswer: strings.TrimSpace(answer)}
			}

			if act, ok := args["action"].(string); ok && act == "deliver" {
				if seat.actor == actorSubagent {
					countExecuted()
					return conversation.Observation{Text: result, EndTurn: true}
				}
			}
		}
		countExecuted()

		obs := conversation.Observation{Text: result}
		if name == "recall" {
			obs.HostNote = identity.RecallHostNote(args)
		}
		return obs
	}

	ctx = pluginhost.WithActingSession(ctx, pluginhost.ActingSession{ID: seat.session, Reason: seat.reason, CallID: store.ToolCallRecordID(interaction.TurnID(ctx), tc.EmissionOrdinal), Delivered: seat.delivered})
	a.keepSandboxOffPlaces()
	started := time.Now()
	result, err := a.toolReg.ExecuteModel(ctx, name, args)
	elapsed := time.Since(started).Milliseconds()
	if err != nil {

		return conversation.Observation{Text: fmt.Sprintf("Error: %v", err), Failed: true, Refused: errors.Is(err, tools.ErrUnknownTool), ReasonCode: pluginFailureCode(err)}
	}
	if result.ReasonCode == tools.ReasonHostOnly {
		return conversation.Observation{Text: result.Text(), Failed: true, Refused: true, ReasonCode: result.ReasonCode}
	}
	countExecuted()

	a.emitPluginEventAttributed(pluginhost.TopicToolCalled, map[string]any{"tool": name, "failed": result.Failed(), "duration_ms": elapsed, "actor": seat.actor, "session": seat.session}, seat.lookup != nil)
	text, picture := a.readPicture(ctx, result)
	if routed && (result.ReasonCode == tools.ReasonArgumentsRequired || result.ReasonCode == broker.ReasonArgumentInvalid) {

		text += a.callCard(name)
	}
	return conversation.Observation{Text: text, Image: picture, Failed: result.Failed(), Refused: result.Refused, Truncated: result.Truncated, DurationMS: &elapsed, ExitStatus: result.ExitStatus, WorkSession: seat.session, SessionReason: seat.reason, ReasonCode: result.ReasonCode}
}

const (
	actorMain     = "main"
	actorSafe     = "safe"
	actorSubagent = "subagent"
)

type callSeat struct {
	actor string

	leg     string
	session string
	reason  string
	lookup  error

	delivered string
}

func (a *App) startedSeat(ctx context.Context, tc llm.ToolCall) callSeat {
	turn := interaction.TurnID(ctx)
	var (
		rec   store.CallSeat
		found bool
		err   error
	)

	if a.safeTools != nil {
		rec, found = a.safeTools.seat(turn, tc.EmissionOrdinal)
	}
	if !found && a.store != nil {
		rec, found, err = a.store.ToolCallSeat(turn, tc.EmissionOrdinal)
	}
	seat := callSeat{actor: actorMain}
	if _, child := ctx.Value(identity.SubagentWorkSession{}).(string); child {
		seat.actor = actorSubagent
	}
	switch {
	case err != nil:
		seat.reason, seat.lookup = "session_lookup_failed", err
		return seat
	case !found:
		seat.reason, seat.lookup = "session_lookup_failed", fmt.Errorf("no record holds the start of call %d of turn %s", tc.EmissionOrdinal, turn)
		return seat
	}

	switch rec.Actor {
	case "main":
		seat.actor = actorMain
	case "safe":
		seat.actor = actorSafe
	default:
		seat.actor, seat.leg = actorSubagent, rec.Actor
	}
	seat.session, seat.reason = rec.Session, rec.Reason
	if seat.reason == "session_lookup_failed" {
		seat.lookup = errors.New("the session lookup failed when this call started")
	}
	a.readDelivered(ctx, &seat)
	return seat
}

func (a *App) callSeat(ctx context.Context) callSeat {
	if child, ok := ctx.Value(identity.SubagentWorkSession{}).(string); ok {
		seat := callSeat{actor: actorSubagent, session: child, leg: child}
		if child == "" {
			seat.reason = "no_session"
		}
		return seat
	}
	seat := callSeat{actor: actorMain}
	if a.currentMode() == ModeSafe {
		seat.actor = actorSafe
	}

	if id := interaction.ResumeSession(ctx); id != "" {
		seat.session = id
		return seat
	}
	if a.store != nil {

		id, several, err := a.store.ActingSession()
		switch {
		case err != nil:
			seat.reason, seat.lookup = "session_lookup_failed", err
		case several:
			seat.session, seat.reason = id, store.SeveralActive
		default:
			seat.session = id
		}
	}
	if seat.session == "" && seat.reason == "" {
		seat.reason = "no_session"
	}
	a.readDelivered(ctx, &seat)
	return seat
}

func (a *App) readDelivered(ctx context.Context, seat *callSeat) {
	if seat.session != "" || seat.reason != "no_session" || a.store == nil {
		return
	}

	delivered, err := a.store.SessionDeliveredInTurn(interaction.TurnID(ctx))
	if err != nil {
		seat.reason, seat.lookup = "session_lookup_failed", err
	}
	seat.delivered = delivered
}

var errCallArgs = errors.New("args must be an object: the operation's arguments")

func callRoute(reg *tools.Registry, tc llm.ToolCall, args map[string]any) (llm.ToolCall, map[string]any, bool, error) {
	if action, _ := args["action"].(string); tc.Function.Name != "tools" || strings.ToLower(strings.TrimSpace(action)) != "call" {
		return tc, args, false, nil
	}
	ref, _ := args["name"].(string)
	if strings.TrimSpace(ref) == "" {
		return tc, args, false, fmt.Errorf("tools action=call needs name=<operation> and args={…}; tools action=show name=<operation> gives its arguments")
	}
	if reg == nil {
		return tc, args, false, fmt.Errorf("no plugin operations are installed beside you")
	}
	target, err := reg.Resolve(ref)
	if err != nil {
		return tc, args, false, err
	}
	tc.Function.Name = target
	var inner map[string]any
	switch v := args["args"].(type) {
	case nil:
		inner = map[string]any{}
	case map[string]any:
		inner = v
	case string:

		if json.Unmarshal([]byte(v), &inner) != nil || inner == nil {
			return tc, args, false, errCallArgs
		}
	default:
		return tc, args, false, errCallArgs
	}
	raw, err := json.Marshal(inner)
	if err != nil {
		return tc, args, false, fmt.Errorf("the arguments for %s do not encode: %w", target, err)
	}
	tc.Function.Arguments = string(raw)
	return tc, inner, true, nil
}

func routedCall(reg *tools.Registry, tc llm.ToolCall) llm.ToolCall {
	if tc.Function.Name != "tools" {
		return tc
	}
	var args map[string]any
	if json.Unmarshal([]byte(tc.Function.Arguments), &args) != nil {
		return tc
	}
	if call, _, routed, err := callRoute(reg, tc, args); err == nil && routed {
		return call
	}
	return tc
}

func (a *App) callCard(name string) string {
	if a.toolReg == nil {
		return ""
	}
	card, err := toolDiscovererAdapter{a.toolReg}.Show(name)
	if err != nil {
		return ""
	}
	return "\n\n" + identity.ToolCardText(card, false)
}

const (
	ReasonPluginTimeout          = "PLUGIN_TIMEOUT"
	ReasonPluginTrap             = "PLUGIN_TRAP"
	ReasonPluginResponseContract = "PLUGIN_RESPONSE_CONTRACT"
	ReasonPluginFault            = "PLUGIN_FAULT"
)

func pluginFailureCode(err error) string {
	var timeout *pluginworker.TimeoutError
	var trap *pluginworker.TrapError
	var limit *pluginworker.ResourceLimitError
	var contract *pluginhost.ResponseContractError
	switch {
	case errors.Is(err, tools.ErrUnknownTool):
		return ""
	case errors.Is(err, context.Canceled):
		return ""
	case errors.As(err, &timeout), errors.Is(err, context.DeadlineExceeded):
		return ReasonPluginTimeout
	case errors.As(err, &trap), errors.As(err, &limit):
		return ReasonPluginTrap
	case errors.As(err, &contract):
		return ReasonPluginResponseContract
	}
	return ReasonPluginFault
}
