package identity

import (
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
// .
// .
// .
// .
// .
func (e *Engine) RecordConversationTurn(role, content string) error {
	if e.inSafeMode() {
		e.safeMu.Lock()
		e.safeTranscript = append(e.safeTranscript, SafeTurn{Role: role, Content: content})
		// .
		// .
		if len(e.safeTranscript) > 200 {
			e.safeTranscript = e.safeTranscript[len(e.safeTranscript)-200:]
		}
		e.safeMu.Unlock()
		return nil
	}
	return e.store.AddConversationTurn(role, content)
}

// .
// .
// .
func (e *Engine) RecordConversationTurnSeq(role, content string) (uint64, error) {
	if e.inSafeMode() {
		return 0, e.RecordConversationTurn(role, content)
	}
	return e.store.AddConversationTurnSeq(role, content)
}

// .
type SafeTurn struct {
	Role    string
	Content string
}

// .
// .
// .
func (e *Engine) SafeTranscript() []SafeTurn {
	e.safeMu.RLock()
	defer e.safeMu.RUnlock()
	out := make([]SafeTurn, len(e.safeTranscript))
	copy(out, e.safeTranscript)
	return out
}

// .
func (e *Engine) SetTimers(t TimerSetter) { e.timers = t }

// .
// .
// .
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

// .
// .
// .
// .
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

// .
// .
// .
func (e *Engine) UndeliveredMessages() ([]store.OutboxMessage, error) {
	return e.store.UndeliveredFor("operator")
}

// .
func (e *Engine) MarkDelivered(msgID, via string) error {
	return e.store.MarkDelivered(msgID, via)
}
