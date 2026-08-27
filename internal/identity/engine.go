package identity

import (
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// RecordConversationTurn records a turn — durably in NORMAL, transiently
// in SAFE. Canon IDENTITY_SEMANTICS §10 admits NO database writes while
// identity integrity is unverified, and the Go mode design (SAFE_DEGRADED
// P3) promises conversation "read-only and transient": the person still
// converses, the session still coheres, and nothing lands in a record
// that can't be trusted. Transient turns live only in memory and die
// with the process; they are also never citable by R52 (minting is
// frozen in SAFE anyway).
func (e *Engine) RecordConversationTurn(role, content string) error {
	if e.inSafeMode() {
		e.safeMu.Lock()
		e.safeTranscript = append(e.safeTranscript, SafeTurn{Role: role, Content: content})
		// 200 is a STRUCTURAL memory bound (R15), not an operator
		// tunable — SAFE is an emergency posture, not a configuration.
		if len(e.safeTranscript) > 200 {
			e.safeTranscript = e.safeTranscript[len(e.safeTranscript)-200:]
		}
		e.safeMu.Unlock()
		return nil
	}
	return e.store.AddConversationTurn(role, content)
}

// SafeTurn is one transient conversation turn held in memory during SAFE.
type SafeTurn struct {
	Role    string
	Content string
}

// SafeTranscript returns the transient turns recorded while in SAFE mode
// (empty in NORMAL) — the history assembler appends them so the live
// conversation stays coherent without touching the store.
func (e *Engine) SafeTranscript() []SafeTurn {
	e.safeMu.RLock()
	defer e.safeMu.RUnlock()
	out := make([]SafeTurn, len(e.safeTranscript))
	copy(out, e.safeTranscript)
	return out
}

// SetTimers wires the resident-timer backend (durable alarm rows).
func (e *Engine) SetTimers(t TimerSetter) { e.timers = t }

// NoteExternalFetch records that a URL was actually fetched (wired from
// the web_fetch tool by the app). Bounded: the registry exists only to
// verify source_url citations within a session (H3).
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

// safeMode is the engine's SAFE refusal reason ("" = normal). Verbs
// that MINT refuse honestly in SAFE — the record they would write into
// cannot be verified (docs/SAFE_DEGRADED.md §2). Guarded (S6): set from
// the app's mode machinery, read from verb goroutines.
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

// UndeliveredMessages returns outbox messages for the dashboard to
// deliver — which is the OPERATOR's mail, and only that. The doc always
// said "for the dashboard to deliver"; the query did not agree.
func (e *Engine) UndeliveredMessages() ([]store.OutboxMessage, error) {
	return e.store.UndeliveredFor("operator")
}

// MarkDelivered marks an outbox message as delivered.
func (e *Engine) MarkDelivered(msgID, via string) error {
	return e.store.MarkDelivered(msgID, via)
}
