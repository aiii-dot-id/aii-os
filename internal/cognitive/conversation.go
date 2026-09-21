package cognitive

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/cognitive/landing"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
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
// .
// .
// .

// .
// .
// .
// .
// .
type ConversationSource interface {
	NextConversation(budget int, exclude string) (store.ConversationBatch, error)
	PublishConversationCursor(batch store.ConversationBatch) error
}

// .
type conversationReader interface {
	NextConversation(budget int, exclude string) (store.ConversationBatch, error)
}

// .
// .
func (d *DreamFacility) SetConversation(src ConversationSource) {
	if src == nil {
		d.talk, d.talkCursor = nil, nil
		return
	}
	d.talk, d.talkCursor = src, landing.ConversationCursors(src)
}

// .
// .
// .
func (d *DreamFacility) ConversationWired() bool { return d.talk != nil }

// .
// .
// .
func (d *DreamFacility) ConversationPending() bool {
	return len(d.unreadConversation(d.config.ConversationMaxChars).Parts) > 0
}

// .
// .
// .
// .
// .
// .
const maxRoomStretches = 8

// .
// .
// .
// .
func (d *DreamFacility) unreadConversation(budget int) store.ConversationBatch {
	if d.talk == nil || budget <= 0 {
		return store.ConversationBatch{}
	}
	for i := 0; i < maxRoomStretches; i++ {
		batch, err := d.talk.NextConversation(budget, d.config.RoomNotePrefix)
		if err != nil {
			logsink.Warn("dream.error", "conversation not read: %v — nothing advanced", err)
			return store.ConversationBatch{}
		}
		if len(batch.Parts) > 0 || !batch.Moved() {
			return batch
		}
		// .
		// .
		moved, err := d.land.PassOver(d.talkCursor(batch))
		if err != nil {
			logsink.Warn("dream.error", "conversation cursor not published: %v — the same turns are read again", err)
		}
		if !moved {
			return store.ConversationBatch{}
		}
	}
	return store.ConversationBatch{}
}

// .
// .
// .
// .
func renderConversation(batch store.ConversationBatch) string {
	var b strings.Builder
	b.WriteString("Conversation since your last pass, oldest first (\"you\" is you, the identity):")
	for _, p := range batch.Parts {
		who := p.Role
		if who == "resident" {
			who = "you"
		}
		switch {
		case p.From > 0 && !p.Whole:
			who += ", the middle of a long turn"
		case p.From > 0:
			who += ", the rest of a long turn begun in an earlier pass"
		case !p.Whole:
			who += ", the beginning of a long turn — the rest comes in a later pass"
		}
		fmt.Fprintf(&b, "\n[%s] %s", who, p.Text)
	}
	return b.String()
}

// .
// .
// .
// .
type InputChecker interface {
	CheckSimple(ctx context.Context, systemPrompt, userMessage string) error
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
func (d *DreamFacility) fitConversation(ctx context.Context, systemPrompt string, expTexts []string, talk store.ConversationBatch) (kept store.ConversationBatch, fits bool) {
	checker, ok := d.llm.(InputChecker)
	if !ok {
		return talk, true
	}
	admits := func(b store.ConversationBatch) bool {
		return checker.CheckSimple(ctx, systemPrompt, d.request(expTexts, b)) == nil
	}
	if admits(talk) {
		return talk, true
	}
	none := talk.Keep(0, 0)
	if !admits(none) {
		return none, false
	}
	// .
	lo, hi := 0, len(talk.Parts)
	for hi-lo > 1 {
		mid := (lo + hi) / 2
		if admits(talk.Keep(mid, 0)) {
			lo = mid
		} else {
			hi = mid
		}
	}
	if lo > 0 {
		return talk.Keep(lo, 0), true
	}
	// .
	lo, hi = 0, utf8.RuneCountInString(talk.Parts[0].Text)
	for hi-lo > 1 {
		mid := (lo + hi) / 2
		if admits(talk.Keep(1, mid)) {
			lo = mid
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return none, true
	}
	return talk.Keep(1, lo), true
}
