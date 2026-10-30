package app

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

const defaultWorkCardBeliefs = 3

var workCardBeliefsSetting = cognitionSetting{key: "prompt.work_card_beliefs", min: 1, max: 50, unit: "beliefs", zero: true,
	def: fixed(defaultWorkCardBeliefs),
	num: func(c *Config) *int { return &c.Prompt.WorkCardBeliefs }}

const (
	workCardBeliefsHead = "Your working beliefs that share words with this work (fallible; check them against what you find):"
	workCardBeliefLine  = "- %s: %s"
)

func (a *App) workCardBeliefs(ctx context.Context, ws *store.WorkSession) string {
	if ws == nil || a.store == nil || strings.TrimSpace(ws.Description) == "" {
		return ""
	}
	cfg := a.boundsConfig()
	n := workCardBeliefsSetting.n(cfg)
	chars := cfg.Prompt.ExcerptChars
	if chars <= 0 {
		chars = cognitive.DefaultExcerptChars
	}
	res, err := memory.New(a.store).Recall(ctx, memory.Query{
		Text: ws.Description, AnyWords: true, Stores: []string{"beliefs"},
		Limit: memory.MaxLimit, Reinforce: false,
	})
	if err != nil {
		logsink.Warn("prompt.error", "work card beliefs: unreadable this turn, the card goes without them: %v", err)
		return ""
	}
	lines := []string{workCardBeliefsHead}
	for _, h := range res.Hits {
		if h.Ring != 3 {
			continue
		}
		text := strings.Join(strings.Fields(h.Text), " ")
		if utf8.RuneCountInString(text) > chars {
			text = string([]rune(text)[:chars]) + "…"
		}
		lines = append(lines, fmt.Sprintf(workCardBeliefLine, h.ID, text))
		if len(lines) > n {
			break
		}
	}
	if len(lines) == 1 {
		return ""
	}
	return strings.Join(lines, "\n")
}
