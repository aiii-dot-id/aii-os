package identity

import (
	"context"
	"encoding/json"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/google/uuid"
)

func (e *Engine) SetInteractionObserver(fn func()) {
	e.safeMu.Lock()
	e.interactionObserver = fn
	e.safeMu.Unlock()
}
func (e *Engine) RecordConversationRef(ctx context.Context, role, content string, details interaction.Details) (store.InteractionRef, error) {
	return e.RecordRelatedConversationRef(ctx, role, content, "", details)
}
func (e *Engine) RecordRelatedConversationRef(ctx context.Context, role, content, related string, details interaction.Details) (store.InteractionRef, error) {
	ref, _, err := e.recordConversationLocated(ctx, role, content, related, details)
	return ref, err
}

func (e *Engine) RecordConversationLocated(ctx context.Context, role, content string, details interaction.Details) (store.InteractionRef, interaction.Location, error) {
	return e.recordConversationLocated(ctx, role, content, "", details)
}

func (e *Engine) recordConversationLocated(ctx context.Context, role, content, related string, details interaction.Details) (store.InteractionRef, interaction.Location, error) {
	turn := interaction.TurnID(ctx)
	if e.inSafeMode() {
		ref, loc := e.retainTransient("safe_conversation", role, content, turn, "", related, details)
		return ref, loc, nil
	}
	ref, err := e.store.RecordRelatedConversation(ctx, role, content, turn, related, details)
	if err != nil {
		details.RecordingError = err.Error()
		_, loc := e.retainTransient("unrecorded_output", role, content, turn, ref.ID, related, details)
		return ref, loc, err
	}
	return ref, interaction.Location{Source: "recorded", Incarnation: e.store.InteractionIncarnation(), ID: ref.ID}, nil
}
func (e *Engine) retainTransient(origin, role, content, turn, id, related string, details interaction.Details) (store.InteractionRef, interaction.Location) {
	e.safeMu.Lock()
	if e.transientIncarnation == "" {
		e.transientIncarnation = uuid.NewString()
	}
	e.transientSeq++
	if id == "" {
		id = "transient_" + uuid.NewString()
	}
	stamp, _ := interaction.NormalizeTime(time.Now().UTC())
	kind := interaction.Message
	if role == "system" {
		kind = interaction.Notice
	}
	details.Origin = origin
	r := interaction.Record{ID: id, Sequence: e.transientSeq, Role: interaction.Role(role), Kind: kind, Content: content, TurnID: turn, RelatedID: related, CreatedAt: stamp, RecordedAt: stamp, Details: details}

	raw, _ := json.Marshal(r)
	_ = json.Unmarshal(raw, &r)
	e.safeTranscript = append(e.safeTranscript, SafeTurn{Role: role, Content: content, Record: r})
	if len(e.safeTranscript) > 200 {
		e.transientLost += uint64(len(e.safeTranscript) - 200)
		e.safeTranscript = append([]SafeTurn(nil), e.safeTranscript[len(e.safeTranscript)-200:]...)
	}
	fn := e.interactionObserver
	loc := interaction.Location{Source: "transient", Incarnation: e.transientIncarnation, ID: id}
	e.safeMu.Unlock()
	if fn != nil {
		fn()
	}
	return store.InteractionRef{ID: id}, loc
}
func (e *Engine) TransientInteractions() (string, []interaction.Record, uint64) {
	e.safeMu.Lock()
	defer e.safeMu.Unlock()
	if e.transientIncarnation == "" {
		e.transientIncarnation = uuid.NewString()
	}
	out := make([]interaction.Record, 0, len(e.safeTranscript))
	for _, r := range e.safeTranscript {
		out = append(out, r.Record)
	}
	raw, _ := json.Marshal(out)
	_ = json.Unmarshal(raw, &out)
	lost := e.transientLost
	return e.transientIncarnation, out, lost
}

func (e *Engine) StampTransient() (uint64, string) {
	e.safeMu.Lock()
	defer e.safeMu.Unlock()
	if e.transientIncarnation == "" {
		e.transientIncarnation = uuid.NewString()
	}
	e.transientSeq++
	stamp, _ := interaction.NormalizeTime(time.Now().UTC())
	return e.transientSeq, stamp
}
func (e *Engine) InvalidateInteractions() {
	e.safeMu.RLock()
	fn := e.interactionObserver
	e.safeMu.RUnlock()
	if fn != nil {
		fn()
	}
}
