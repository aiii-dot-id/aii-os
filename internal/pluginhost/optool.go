package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/jsonschema"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

type contractSeam interface {
	BeginOperation(broker.OperationScope)
	EndOperation()
	InvokeRefused(operation, detail, effect string)
}

type opDescriptor struct {
	raw          map[string]any
	input        *jsonschema.Schema
	output       *jsonschema.Schema
	outputRaw    map[string]any
	effects      string
	capabilities []string
	capsDeclared bool
	maxResult    int

	summary  string
	family   string
	keywords []string
	examples []string

	operatorConfirms bool
	primary          bool
}

func (d *opDescriptor) discovery(plugin, version, tier, operation string) tools.Discovery {
	disc := tools.Discovery{Plugin: plugin, Version: version, Tier: tier, Operation: operation}
	if d == nil {
		return disc
	}
	disc.Family, disc.Summary, disc.Effects = d.family, d.summary, d.effects
	disc.Capabilities = append([]string(nil), d.capabilities...)
	disc.MaxResultBytes = d.maxResult
	disc.Keywords = append([]string(nil), d.keywords...)
	disc.Examples = append([]string(nil), d.examples...)
	disc.OperatorConfirms = d.operatorConfirms
	disc.Primary = d.primary
	disc.OutputSchema = d.outputRaw
	return disc
}

func (d *opDescriptor) params() map[string]any {
	if d == nil {
		return nil
	}
	return d.raw
}

type operationTool struct {
	owner       *ActivePlugin
	name        string
	description string
	operation   string
	inv         invoker
	parameters  map[string]any
	desc        *opDescriptor
	seam        contractSeam
	inFlight    *opGate
	disc        tools.Discovery
	plugin      string
	acts        ActProposer
	gate        *atomic.Bool
	authorized  func() bool
}

func (t *operationTool) Discovery() tools.Discovery { return t.disc }

func (t *operationTool) ReplaySafe() bool {
	if t.desc == nil || t.disc.OperatorConfirms {
		return false
	}
	if t.desc.effects == "" {
		return false
	}
	return broker.IsReadEffect(t.desc.effects)
}

func (t *operationTool) Name() string        { return t.name }
func (t *operationTool) Description() string { return t.description }

func (t *operationTool) Parameters() map[string]any {
	if t.parameters != nil {
		return t.parameters
	}
	return map[string]any{
		"type":                 "object",
		"description":          "Arguments forwarded verbatim to the plugin operation.",
		"properties":           map[string]any{},
		"additionalProperties": true,
	}
}

type invokeRequest struct {
	JSONRPC string       `json:"jsonrpc"`
	ID      string       `json:"id"`
	Method  string       `json:"method"`
	Params  invokeParams `json:"params"`
}

type invokeParams struct {
	Operation   string         `json:"operation"`
	Arguments   map[string]any `json:"arguments"`
	WorkSession map[string]any `json:"_host_work_session,omitempty"`
}

type invokeResult struct {
	Status          string          `json:"status"`
	OperationResult json.RawMessage `json:"operation_result"`
	Reason          string          `json:"reason"`
	ReasonCode      string          `json:"reasonCode"`
	ReasonCodeSnake string          `json:"reason_code"`
}

type rpcErrorObject struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		ReasonCode string `json:"reasonCode"`
	} `json:"data"`
}

func (t *operationTool) Execute(ctx context.Context, args map[string]any) (result tools.Result, callErr error) {
	return t.execute(ctx, args, false)
}

func (t *operationTool) check(ctx context.Context, args map[string]any) (tools.Result, error) {
	return t.execute(ctx, args, true)
}

func (t *operationTool) execute(ctx context.Context, args map[string]any, checking bool) (result tools.Result, callErr error) {

	if t.gate != nil && !t.gate.Load() && !checking {
		return tools.Refusal(ReasonNotAdmitted, fmt.Sprintf("plugin %s: this release is being admitted; its operations are not yet callable", t.plugin)), nil
	}

	if t.authorized != nil && !t.authorized() {
		return tools.Refusal(ReasonActivationWithdrawn, fmt.Sprintf("plugin %s: this release was withdrawn; its operations are not callable", t.plugin)), nil
	}

	if t.desc != nil && t.desc.input != nil {
		if verr := t.desc.input.Validate(jsonValue(args)); verr != nil {
			return tools.Refusal(broker.ReasonArgumentInvalid, "arguments refused before the call: "+verr.Error()), nil
		}
	}
	injected := make(map[string]any, len(args)+2)
	for k, v := range args {
		if !strings.HasPrefix(k, "_host") {
			injected[k] = v
		}
	}

	if t.inFlight != nil {
		if err := t.inFlight.acquire(ctx); err != nil {
			return tools.Result{Error: "the call was cancelled while waiting for this plugin's turn; nothing ran", ReasonCode: ReasonCancelledBeforeDispatch}, nil
		}
		defer t.inFlight.release()

		if err := ctx.Err(); err != nil {
			return tools.Result{Error: "the call was cancelled before it was dispatched; nothing ran", ReasonCode: ReasonCancelledBeforeDispatch}, nil
		}
	}

	if t.authorized != nil && !t.authorized() {
		return tools.Refusal(ReasonActivationWithdrawn, fmt.Sprintf("plugin %s: this release was withdrawn; its operations are not callable", t.plugin)), nil
	}

	delivery := calibrationDeliveryFrom(ctx)
	if delivery != nil {
		defer func() { delivery.Finish(delivery.Accept(result, callErr)) }()
	}
	if err := tools.CheckDispatch(ctx); err != nil {
		code := ""
		var changed *tools.DispatchTargetChangedError
		var refused *tools.DispatchRefusedError
		switch {
		case errors.As(err, &changed):
			code = tools.ReasonDispatchTargetChanged
		case errors.As(err, &refused):
			code = refused.Code
		}
		return tools.Refusal(code, err.Error()), nil
	}
	if t.gate != nil && !t.gate.Load() && !checking {
		return tools.Refusal(ReasonNotAdmitted, "activation not admitted"), nil
	}
	injected["_host_now_ms"] = time.Now().UnixMilli()
	acting := ActingSession{}
	var workSession map[string]any
	if !checking {
		acting = ActingSessionFrom(ctx)

		workSession = map[string]any{"id": acting.ID, "reason": acting.Reason, "call_id": acting.CallID}
	}

	if urls := t.owner.webhookURLs(); len(urls) > 0 && !checking {
		injected["_host_webhooks"] = urls
	}

	if gate, ok := t.acts.(OperationGate); ok && t.acts != nil {
		effects := ""
		if t.desc != nil {
			effects = t.desc.effects
		}
		if gerr := gate.AdmitOperation(t.plugin, t.operation, effects); gerr != nil {
			return tools.Refusal(ReasonOperatorGrant, gerr.Error()+"; nothing ran"), nil
		}
	}

	if t.desc != nil && t.desc.operatorConfirms && checking {
		return tools.Refusal(ReasonConfirmationUnavailable, fmt.Sprintf("%s needs the operator's confirmation, which a check cannot give; nothing ran", t.operation)), nil
	}
	if t.desc != nil && t.desc.operatorConfirms {
		act := OperatorActFrom(ctx)

		if act == nil && t.acts != nil {
			if sc, ok := t.acts.(StandingConfirmer); ok {
				if standing, ok := sc.StandingConfirmation(t.plugin, t.operation); ok {
					act = &standing
				}
			}
		}
		if act == nil {
			if t.acts == nil {
				return tools.Refusal(ReasonConfirmationUnavailable, fmt.Sprintf("%s needs the operator's confirmation and this host has no way to ask for it; nothing ran", t.operation)), nil
			}
			id, perr := t.acts.Propose(ctx, ActProposal{Plugin: t.plugin, Tool: t.name, Operation: t.operation, Summary: t.desc.summary, Effects: t.desc.effects, Args: args})
			if perr != nil {
				return tools.Result{Error: fmt.Sprintf("%s was not proposed: %v; nothing ran", t.operation, perr)}, nil
			}
			out, _ := json.Marshal(map[string]any{
				"proposed": true, "act": id, "operation": t.operation,
				"awaiting": "the operator's confirmation on the Plugins page; nothing has run",
				"note":     "the operation runs once, with exactly these arguments, when the operator confirms; the outcome is recorded in the conversation as the operator's act — do not re-issue this call",
			})
			return tools.Result{Output: string(out)}, nil
		}
		injected["_host_operator_act"] = map[string]any{"id": act.ID, "confirmed_at": act.ConfirmedAt.UTC().Format(time.RFC3339)}
	}
	var generation uint64
	if !checking {
		var err error
		generation, err = t.calibrationFrame(ctx, injected, delivery)
		if err != nil {
			return tools.Result{Error: err.Error(), ReasonCode: "CALIBRATION_UNAVAILABLE"}, nil
		}
	}
	frame, err := json.Marshal(invokeRequest{JSONRPC: "2.0", ID: harnessRequestID, Method: "invoke.call", Params: invokeParams{Operation: t.operation, Arguments: injected, WorkSession: workSession}})
	if err != nil {
		return tools.Result{}, err
	}
	if t.seam != nil && (t.desc != nil || checking) {
		scope := broker.OperationScope{Operation: t.operation, Validation: checking}
		scope.WorkSession, scope.SessionReason, scope.CallID = acting.ID, acting.Reason, acting.CallID
		if t.desc != nil {
			scope.Effects, scope.Capabilities, scope.Declared = t.desc.effects, t.desc.capabilities, t.desc.capsDeclared
		}
		t.seam.BeginOperation(scope)
		defer t.seam.EndOperation()
	}
	var reply []byte
	if t.owner != nil && t.owner.CalibrationOperation() != "" && t.owner.sup != nil && !checking {
		reply, err = t.owner.sup.InvokeGeneration(ctx, frame, generation)
	} else {
		reply, err = t.inv.Invoke(ctx, frame)
	}
	if err != nil {
		return tools.Result{}, err
	}
	res, err := decodeReply(reply)
	if err != nil || res.Error != "" {
		return res, err
	}
	accepted := t.acceptResult(res)
	if delivery != nil && accepted.Error == "" && !delivery.Accept(accepted, nil) {
		return tools.Result{Error: "calibration acknowledgement does not match delivery", ReasonCode: "CALIBRATION_ACK_REFUSED"}, nil
	}
	return accepted, nil
}

func (t *operationTool) acceptResult(res tools.Result) tools.Result {
	if t.desc == nil {
		return res
	}
	if t.desc.maxResult > 0 && len(res.Output) > t.desc.maxResult {
		return t.refuse(fmt.Sprintf("result of %d bytes exceeds the operation's declared bound of %d", len(res.Output), t.desc.maxResult))
	}
	if t.desc.output != nil {
		var v any
		if err := json.Unmarshal([]byte(res.Output), &v); err != nil {
			return t.refuse("result is not JSON, and the operation declared an output schema")
		}
		if verr := t.desc.output.Validate(v); verr != nil {
			return t.refuse("result outside the declared output schema: " + verr.Error())
		}
	}
	return res
}

const ReasonResultRefused = "PLUGIN_RESULT_REFUSED"

const resultRefusedEffect = "; the operation ran, and whether it had its effect on the service is not known — check before asking again"

func (t *operationTool) refuse(detail string) tools.Result {
	effect := ""

	if t.desc == nil || t.desc.effects == "" || !broker.IsReadEffect(t.desc.effects) {
		detail += resultRefusedEffect
		effect = broker.EffectUnknown
	}
	if t.seam != nil {
		t.seam.InvokeRefused(t.operation, detail, effect)
	}
	return tools.Result{Error: "result refused: " + detail, ReasonCode: ReasonResultRefused}
}

func jsonValue(args map[string]any) any {
	raw, err := json.Marshal(args)
	if err != nil {
		return args
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return args
	}
	return v
}

func decodeReply(reply []byte) (tools.Result, error) {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(reply, &members); err != nil {
		return tools.Result{}, &ResponseContractError{Requirement: "reply must be a JSON object", Got: excerpt(reply)}
	}
	var version string
	if err := json.Unmarshal(members["jsonrpc"], &version); err != nil || version != "2.0" {
		return tools.Result{}, &ResponseContractError{Requirement: `jsonrpc must be exactly "2.0"`, Got: excerpt(reply)}
	}
	if _, hasMethod := members["method"]; hasMethod {
		return tools.Result{}, &ResponseContractError{Requirement: "a response carries no method member (a request is not a response)", Got: excerpt(reply)}
	}
	var id any
	if err := json.Unmarshal(members["id"], &id); err != nil {
		return tools.Result{}, &ResponseContractError{Requirement: "id must be present", Got: excerpt(reply)}
	}
	if id != harnessRequestID {
		return tools.Result{}, &ResponseContractError{Requirement: fmt.Sprintf("id must echo %q verbatim", harnessRequestID), Got: excerpt(reply)}
	}
	resultRaw, hasResult := members["result"]
	errorRaw, hasError := members["error"]
	if hasResult == hasError {
		return tools.Result{}, &ResponseContractError{Requirement: "exactly one of result|error", Got: excerpt(reply)}
	}

	if hasError {

		var eo rpcErrorObject
		if err := json.Unmarshal(errorRaw, &eo); err != nil {
			return tools.Result{}, &ResponseContractError{Requirement: "error member must be a JSON-RPC error object", Got: excerpt(reply)}
		}
		msg := fmt.Sprintf("plugin error %d: %s", eo.Code, eo.Message)
		if eo.Data.ReasonCode != "" {
			msg += fmt.Sprintf(" (reasonCode %s)", eo.Data.ReasonCode)
		}
		return tools.Result{Error: msg, ReasonCode: eo.Data.ReasonCode}, nil
	}

	var resultMembers map[string]json.RawMessage
	if json.Unmarshal(resultRaw, &resultMembers) == nil {
		if _, forged := resultMembers["external_receipt"]; forged {
			return tools.Result{}, &ResponseContractError{
				Requirement: "a plugin response must not carry external_receipt (receipts are host-authored; the daemon injects them)",
				Got:         excerpt(reply),
			}
		}
	}

	var ir invokeResult
	if err := json.Unmarshal(resultRaw, &ir); err != nil {

		return tools.Result{Output: string(resultRaw)}, nil
	}
	switch ir.Status {
	case "", "succeeded":
		if len(ir.OperationResult) > 0 {
			return tools.Result{Output: string(ir.OperationResult)}, nil
		}
		return tools.Result{Output: string(resultRaw)}, nil
	default:
		reason := ir.Reason
		code := firstNonEmpty(ir.ReasonCode, ir.ReasonCodeSnake)
		switch {
		case reason != "" && code != "" && reason != code:
			reason = reason + " (" + code + ")"
		case reason == "":
			reason = firstNonEmpty(code, "no reason given")
		}
		return tools.Result{Error: fmt.Sprintf("operation %s: %s", ir.Status, reason), ReasonCode: code}, nil
	}
}
