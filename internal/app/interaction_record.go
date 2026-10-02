package app

import (
	"context"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/conversation"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/project"
	"github.com/google/uuid"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

type replyRecordKey struct{}
type inboundRecordKey struct{}

func (a *App) recordConversationRef(ctx context.Context, role, text string) (store.InteractionRef, error) {
	return a.recordConversationForReceipt(ctx, ctx, role, text)
}

func (a *App) recordConversationForReceipt(ctx, receiptCtx context.Context, role, text string) (store.InteractionRef, error) {
	fingerprint := ""
	if a.keyPair != nil {
		fingerprint = a.keyPair.Fingerprint()
	}
	ref, loc, err := a.engine.RecordConversationLocated(ctx, role, text, interaction.Details{})
	if role == string(interaction.Participant) && err == nil && ref.Sequence != 0 && a.store != nil {
		if id := inboundID(receiptCtx); id != "" {
			if err := a.store.AnnotateTurn(ref.Sequence, "turn_event.arrival", id, `{}`); err != nil {
				logsink.Warn("channel.error", "arrival input retained but source annotation failed; notice may repeat: %v", err)
			}
		}
	}
	if role == string(interaction.Operator) {
		loc.Identity = fingerprint
		dashboard.ChatRecorded(receiptCtx, &loc, err)
	}
	if role == string(interaction.Resident) && err == nil {
		if sink, ok := ctx.Value(replyRecordKey{}).(*store.InteractionRef); ok {
			*sink = ref
		}
	}
	return ref, err
}

func (a *App) recordReply(ctx context.Context, text string, result conversation.Result) (store.InteractionRef, error) {
	ref, err := a.recordConversationRef(ctx, string(interaction.Resident), text)
	if err != nil || ref.Sequence == 0 || a.store == nil {
		return ref, err
	}
	if err := a.store.AddReasoning(ref.Sequence, replyReasoning(result)); err != nil {
		logsink.Warn("turn.error", "reasoning of turn %d not recorded: %v", ref.Sequence, err)
	}
	return ref, nil
}

func replyReasoning(r conversation.Result) []store.ReasoningCall {
	out := make([]store.ReasoningCall, 0, len(r.Reasoning))
	for _, c := range r.Reasoning {
		out = append(out, store.ReasoningCall{Call: c.Call, Model: c.Model, Text: c.Text})
	}
	return out
}

func (a *App) answer(ctx context.Context, text string, result conversation.Result) (string, error) {
	if _, err := a.recordReply(ctx, text, result); err != nil {
		return text, fmt.Errorf("the host could not record this reply: %w", err)
	}
	return text, nil
}

func (a *App) deliverReply(id, spoken string, recorded store.InteractionRef) error {
	if recorded.ID == "" {
		return a.store.AddOutboxMessage(id, "operator", "", spoken, nil)
	}
	return a.store.AddOutboxMessageForInteraction(id, "operator", "", spoken, recorded)
}

func (a *App) recordConversation(ctx context.Context, role, text string) error {
	_, err := a.recordConversationRef(ctx, role, text)
	return err
}
func (a *App) wakeRecorded(ctx context.Context, role, fact string) (string, store.InteractionRef, error) {
	var ref store.InteractionRef
	text, err := a.wake(context.WithValue(ctx, replyRecordKey{}, &ref), role, fact)
	return text, ref, err
}

func (a *App) wireProjectInteractionRecorder() {
	if a.projects == nil || a.store == nil {
		return
	}
	a.projects.SetContractRecorder(func(id string, before *project.Contract, after project.Contract) error {
		copyContract := func(c project.Contract) interaction.ProjectContract {
			return interaction.ProjectContract{Outcome: c.Outcome, Acceptance: append([]string(nil), c.Acceptance...), Constraints: append([]string(nil), c.Constraints...)}
		}
		change := &interaction.ProjectChange{After: copyContract(after)}
		if before != nil {
			prior := copyContract(*before)
			change.Before = &prior
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := a.store.RecordInteraction(ctx, interaction.Input{ID: "project_" + uuid.NewString(), Kind: interaction.Notice, Role: interaction.System, ProjectID: id, Content: "Project criteria accepted", Details: interaction.Details{Project: change}})
		return err
	})
}
