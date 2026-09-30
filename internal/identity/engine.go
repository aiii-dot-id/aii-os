package identity

import (
	"context"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func (e *Engine) RecordConversationTurn(role, content string) error {
	_, err := e.RecordConversationRef(context.Background(), role, content, interaction.Details{})
	return err
}

func (e *Engine) RecordConversationTurnSeq(role, content string) (uint64, error) {
	ref, err := e.RecordConversationRef(context.Background(), role, content, interaction.Details{})
	return ref.Sequence, err
}

type SafeTurn struct {
	Record  interaction.Record
	Role    string
	Content string
}

func (e *Engine) SafeTranscript() []SafeTurn {
	e.safeMu.RLock()
	defer e.safeMu.RUnlock()
	out := make([]SafeTurn, 0, len(e.safeTranscript))
	for _, t := range e.safeTranscript {
		if t.Record.Details.Origin == "safe_conversation" {
			out = append(out, t)
		}
	}
	return out
}

func (e *Engine) SetTimers(t TimerSetter) { e.timers = t }

func (e *Engine) NoteExternalFetch(url string) {
	const maxFetches = 256
	e.fetchMu.Lock()
	defer e.fetchMu.Unlock()
	if e.fetchedURLs == nil {
		e.fetchedURLs = make(map[string]bool)
	}
	if e.fetchedURLs[url] {
		return
	}
	e.fetchedURLs[url] = true
	e.fetchedOrder = append(e.fetchedOrder, url)
	if len(e.fetchedOrder) > maxFetches {
		delete(e.fetchedURLs, e.fetchedOrder[0])
		e.fetchedOrder = e.fetchedOrder[1:]
	}
}

func (e *Engine) hasRecentFetch(url string) bool {
	e.fetchMu.Lock()
	defer e.fetchMu.Unlock()
	return e.fetchedURLs[url]
}

func (e *Engine) SetSafeMode(reason string) {
	e.safeMu.Lock()
	e.safeReason = reason
	e.safeMu.Unlock()
}

func (e *Engine) inSafeMode() bool { return e.safeModeReason() != "" }

func (e *Engine) safeModeReason() string {
	e.safeMu.RLock()
	defer e.safeMu.RUnlock()
	return e.safeReason
}

type hostStore interface {
	RecordRelatedConversation(ctx context.Context, role, content, turn, related string, details interaction.Details) (store.InteractionRef, error)
	InteractionIncarnation() string
	PageNotices() ([]store.OutboxMessage, error)
	MarkDelivered(messageID, via string) error
	AddOutboxNoticeWithWork(id, content string, work *store.WorkItem) (bool, error)
}

func (e *Engine) UndeliveredMessages() ([]store.OutboxMessage, error) {
	return e.store.PageNotices()
}

func (e *Engine) MarkDelivered(msgID, via string) error {
	return e.store.MarkDelivered(msgID, via)
}
