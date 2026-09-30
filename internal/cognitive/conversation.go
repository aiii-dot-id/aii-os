package cognitive

import (
	"context"
	"fmt"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/cognitive/landing"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/store/cursor"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

type ConversationSource interface {
	NextConversation(budget int, exclude string) (cursor.ConversationBatch, error)
	PublishConversationCursor(batch cursor.ConversationBatch) error
}

type conversationReader interface {
	NextConversation(budget int, exclude string) (cursor.ConversationBatch, error)
}

type toolCallReader interface {
	TurnToolCalls(turnIDs []string) (map[string][]store.ToolCall, error)
}

func (d *DreamFacility) SetConversation(src ConversationSource) {
	if src == nil {
		d.talk, d.talkCursor, d.talkCalls = nil, nil, nil
		return
	}
	d.talk, d.talkCursor = src, landing.ConversationCursors(src)

	d.talkCalls, _ = src.(toolCallReader)
}

func (d *DreamFacility) ConversationWired() bool { return d.talk != nil }

func (d *DreamFacility) ConversationPending() bool {
	return len(d.unreadConversation(d.config.ConversationMaxChars).Parts) > 0
}

const maxRoomStretches = 8

func (d *DreamFacility) unreadConversation(budget int) cursor.ConversationBatch {
	if d.talk == nil || budget <= 0 {
		return cursor.ConversationBatch{}
	}
	for i := 0; i < maxRoomStretches; i++ {
		batch, err := d.talk.NextConversation(budget, d.config.RoomNotePrefix)
		if err != nil {
			logsink.Warn("dream.error", "conversation not read: %v — nothing advanced", err)
			return cursor.ConversationBatch{}
		}
		if len(batch.Parts) > 0 || !batch.Moved() {
			return batch
		}

		moved, err := d.land.PassOver(d.talkCursor(batch))
		if err != nil {
			logsink.Warn("dream.error", "conversation cursor not published: %v — the same turns are read again", err)
		}
		if !moved {
			return cursor.ConversationBatch{}
		}
	}
	return cursor.ConversationBatch{}
}

func renderConversation(batch cursor.ConversationBatch, calls map[string][]store.ToolCall) string {
	last := map[string]int{}
	for i, p := range batch.Parts {
		if p.Role == "resident" && p.TurnID != "" {
			last[p.TurnID] = i
		}
	}
	var b strings.Builder
	b.WriteString("Conversation since your last pass, oldest first (\"you\" is you, the identity):")
	for i, p := range batch.Parts {
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

		text := p.Text
		if p.Role == "tool" || p.Role == "operator_act" {
			text = untrusted.Wrap("operator-authorized tool/report", text)
			if p.Role == "tool" {
				who = "operator-authorized tool result"
			} else {
				who = "operator act report"
			}
		}
		fmt.Fprintf(&b, "\n[%s] %s", who, text)
		if p.Role != "resident" || p.TurnID == "" || !p.Whole || last[p.TurnID] != i {
			continue
		}
		if list, ok := calls[p.TurnID]; ok {
			b.WriteString("\n  [tool calls this turn: " + toolCallList(list) + "]")
		}
	}
	return b.String()
}

const maxCallGroups = 12

func toolCallList(calls []store.ToolCall) string {
	if len(calls) == 0 {
		return "none"
	}
	type group struct {
		tool, outcome string
		n             int
	}
	var groups []*group
	seen := map[[2]string]*group{}
	for _, c := range calls {
		k := [2]string{c.Tool, c.Outcome}
		g := seen[k]
		if g == nil {
			g = &group{tool: c.Tool, outcome: c.Outcome}
			seen[k] = g
			groups = append(groups, g)
		}
		g.n++
	}
	var out []string
	for i, g := range groups {
		if i == maxCallGroups {
			rest := 0
			for _, h := range groups[i:] {
				rest += h.n
			}
			out = append(out, fmt.Sprintf("and %d more", rest))
			break
		}
		s := g.tool
		if s == "" {
			s = "a tool"
		}
		if g.n > 1 {
			s += fmt.Sprintf(" ×%d", g.n)
		}
		switch g.outcome {
		case "succeeded":
		case "":
			s += " (unfinished)"
		case "unknown":
			s += " (outcome unknown)"
		default:
			s += " (" + g.outcome + ")"
		}
		out = append(out, s)
	}
	return strings.Join(out, ", ")
}

func (d *DreamFacility) turnCalls(talk cursor.ConversationBatch) map[string][]store.ToolCall {
	if d.talkCalls == nil {
		return nil
	}
	var ids []string
	for _, p := range talk.Parts {
		if p.Role == "resident" && p.TurnID != "" {
			ids = append(ids, p.TurnID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	calls, err := d.talkCalls.TurnToolCalls(ids)
	if err != nil {
		logsink.Warn("dream.error", "the turns' tool calls could not be read: %v — the pass runs without them, and no turn shows a list", err)
		return nil
	}
	return calls
}

type InputChecker interface {
	CheckSimple(ctx context.Context, systemPrompt, userMessage string) error
}

func (d *DreamFacility) fitConversation(ctx context.Context, systemPrompt string, expTexts []string, talk cursor.ConversationBatch, calls map[string][]store.ToolCall) (kept cursor.ConversationBatch, fits bool) {
	checker, ok := d.llm.(InputChecker)
	if !ok {
		return talk, true
	}
	admits := func(b cursor.ConversationBatch) bool {
		return checker.CheckSimple(ctx, systemPrompt, d.request(expTexts, b, calls)) == nil
	}
	if admits(talk) {
		return talk, true
	}
	none := talk.Keep(0, 0)
	if !admits(none) {
		return none, false
	}

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

	lo, hi = 0, cursor.TurnLength(talk.Parts[0].Text)
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
