package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

const (
	opInteractionQuery = "interaction.query"
	opInteractionRead  = "interaction.read"
	capInteractionRead = "interaction.read"
)

func (b *Binding) dispatchInteraction(ctx context.Context, p invokeParams, g Grant) ([]byte, error) {
	declared := false
	for _, cap := range b.envelope {
		declared = declared || cap == capInteractionRead
	}
	if !declared {
		return errorReply(-32000, "interaction.read is not in the signed envelope", &errorData{ReasonCode: reasonNotInEnvelope, DeniedAt: deniedAtCapEval})
	}
	if !g.Interactions {
		return errorReply(-32000, "operator-visible interaction history is not granted to this plugin", &errorData{ReasonCode: reasonPolicyDeny, DeniedAt: deniedAtCapEval})
	}

	scope := b.scope.Load()
	if scope == nil || !scope.Declared {
		return errorReply(-32000, "interaction.read requires an in-flight operation with declared capabilities", &errorData{ReasonCode: reasonNotInEnvelope, DeniedAt: deniedAtCapEval})
	}
	if reply, denied := b.scopeDenies(p.Operation, capInteractionRead); denied {
		return reply, nil
	}

	p.PluginOperation = scope.Operation
	emit := func(o outcome) ([]byte, error) {
		wire, err := b.resultReply(p.Operation, p.PluginOperation, "", o)
		if err == nil && len(wire) > interaction.MaxPageBytes {
			return errorReply(-32603, "interaction response exceeds the bounded frame", &errorData{ReasonCode: "INTERACTION_CONTENT_UNAVAILABLE"})
		}
		return wire, err
	}
	reply := func(value any, err error) ([]byte, error) {
		if err != nil {
			reason := "INTERACTION_SOURCE_UNAVAILABLE"
			var e *interaction.Error
			if errors.As(err, &e) {
				reason = e.Code
			}
			return emit(outcome{status: statusFailed, reason: reason, detail: err.Error()})
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}

		if len(raw) > interaction.MaxPageBytes-8192 {
			return emit(outcome{status: statusFailed, reason: "INTERACTION_CONTENT_UNAVAILABLE", detail: "interaction response exceeds the bounded frame"})
		}
		return emit(outcome{status: statusSucceeded, transportOK: true, operationResult: raw})
	}
	if len(p.Target) > 0 && string(p.Target) != "null" && string(p.Target) != "{}" {
		return reply(nil, interaction.Invalid("interaction reads have no caller-selected identity target"))
	}
	if b.host.cfg.Interactions == nil {
		return reply(nil, fmt.Errorf("interaction reader is unavailable"))
	}
	switch p.Operation {
	case opInteractionQuery:
		var q interaction.Query
		if err := interaction.Strict(p.Arguments, &q); err != nil {
			return reply(nil, err)
		}
		value, err := b.host.cfg.Interactions.QueryInteractions(ctx, q)
		return reply(value, err)
	case opInteractionRead:
		var r interaction.ReadRequest
		if err := interaction.Strict(p.Arguments, &r); err != nil {
			return reply(nil, err)
		}
		value, err := b.host.cfg.Interactions.ReadInteraction(ctx, r)
		return reply(value, err)
	default:
		return reply(nil, interaction.Invalid("unknown interaction operation"))
	}
}
