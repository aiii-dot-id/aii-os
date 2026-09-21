package cognitive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/llm"

	"github.com/aiii-dot-id/aii-os/internal/cognitive/landing"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
type DreamConfig struct {
	Threshold int
	// .
	// .
	MaxChars int
	// .
	// .
	TensionsMaxChars int
	// .
	// .
	// .
	ConversationMaxChars int
	// .
	// .
	// .
	RoomNotePrefix string
}

// .
// .
// .
const defaultConversationMaxChars = 12000

// .
// .
// .
// .
const defaultSurfacingMaxChars = 2400

// .
// .
// .
// .
// .
const nothingSurfaced = "NOTHING SURFACED"

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
// .
// .
// .
// .
// .
// .
// .
// .
// .
type DreamFacility struct {
	store DreamStore
	llm   LLMCaller
	// .
	// .
	// .
	land       *landing.Lander
	ringWriter RingWriter
	config     DreamConfig
	authority  AuthoritySource
	tensions   TensionsSource
	// .
	// .
	// .
	talk       conversationReader
	talkCursor func(store.ConversationBatch) landing.Cursor
	// .
	// .
	// .
	stalledAt time.Time
	now       func() time.Time
}

// .
// .
// .
type ExperienceStore interface {
	UnprocessedExperienceCount() (int, error)
	ListRawExperiences(n int) ([]store.Experience, error)
}

// .
// .
type DreamStore interface {
	ExperienceStore
	ListRawExperiencesExcept(n int, idPrefix string) ([]store.Experience, error)
}

// .
type LLMCaller interface {
	ChatSimple(ctx context.Context, systemPrompt, userMessage string) (text, modelID string, err error)
	// .
	// .
	// .
	// .
	// .
	ChatStructured(ctx context.Context, systemPrompt, userMessage string, tool llm.ToolDefinition) (payload, modelID string, viaTool bool, err error)
}

// .
type LedgerWriter interface {
	Append(eventType ledger.EventType, ring int, payload interface{}, modelID string) (*ledger.Event, error)
}

// .
func NewDream(store DreamStore, llm LLMCaller, lg LedgerWriter, ringWriter RingWriter, cfg DreamConfig) *DreamFacility {
	if cfg.Threshold == 0 {
		cfg.Threshold = 1
	}
	if cfg.MaxChars <= 0 {
		cfg.MaxChars = defaultSurfacingMaxChars
	}
	if cfg.ConversationMaxChars <= 0 {
		cfg.ConversationMaxChars = defaultConversationMaxChars
	}
	var door landing.Door
	if lg != nil {
		door = lg
	}
	return &DreamFacility{
		store:      store,
		llm:        llm,
		land:       landing.New(door),
		ringWriter: ringWriter,
		config:     cfg,
	}
}

// .
func (d *DreamFacility) Name() string { return "dream" }

// .
// .
func (d *DreamFacility) Predicate(ctx context.Context) bool {
	count, err := d.store.UnprocessedExperienceCount()
	if err != nil {
		return false
	}
	return count >= d.config.Threshold
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
func (d *DreamFacility) Execute(ctx context.Context) error {
	_, err := d.pass(ctx)
	return err
}

// .
type passOutcome int

const (
	passIdle passOutcome = iota
	passAdvanced
	passStalled
)

// .
func (d *DreamFacility) pass(ctx context.Context) (passOutcome, error) {
	var expTexts, expIDs []string
	if d.Predicate(ctx) {
		// .
		// .
		// .
		// .
		// .
		experiences, err := d.store.ListRawExperiencesExcept(20, store.OutcomeObservationPrefix)
		if err != nil {
			return passStalled, fmt.Errorf("dream: list raw experiences: %w", err)
		}
		for _, e := range experiences {
			expTexts = append(expTexts, evidenceText(e))
			expIDs = append(expIDs, e.ID)
		}
	}
	talk := d.unreadConversation(d.config.ConversationMaxChars)
	if len(expTexts) == 0 && len(talk.Parts) == 0 {
		return passIdle, nil
	}

	callCtx, systemPrompt, err := withPreamble(ctx, d.authority, dreamSystemPrompt)
	if err != nil {
		return passStalled, fmt.Errorf("DREAM: authority context: %w", err)
	}
	// .
	// .
	// .
	// .
	// .
	// .
	wanted := len(talk.Parts)
	talk, fits := d.fitConversation(callCtx, systemPrompt, expTexts, talk)
	if !fits {
		logsink.Warn("dream.budget", "the request does not fit the model's input limit even with no conversation in it — nothing sent, nothing advanced (prompt.dream_conversation_max_chars cannot help: the required context leaves no room)")
		return passStalled, nil
	}
	if len(talk.Parts) < wanted {
		logsink.Info("dream.budget", "%d of %d selected conversation part(s) fit the model's input limit beside the rest of the request; what was left out stays unread", len(talk.Parts), wanted)
	}
	if len(expTexts) == 0 && len(talk.Parts) == 0 {
		// .
		// .
		// .
		logsink.Warn("dream.budget", "no conversation fits the model's input limit beside the required context — nothing sent, nothing advanced")
		return passStalled, nil
	}
	output, modelID, err := d.llm.ChatSimple(callCtx, systemPrompt, d.request(expTexts, talk))
	if err != nil {
		logsink.Warn("dream.error", "LLM call failed: %v — nothing consumed, nothing advanced", err)
		return passStalled, nil
	}
	if output == "" {
		// .
		// .
		return passStalled, nil
	}
	note := strings.TrimSpace(output)
	pass := landing.Pass{Marker: ledger.EventDreamRun, Inputs: expIDs, ModelID: modelID}
	if d.talkCursor != nil {
		pass.Cursor = d.talkCursor(talk)
	}

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if note == nothingSurfaced {
		if _, err := d.land.Land(pass); err != nil {
			logsink.Warn("dream.refusal", "nothing surfaced, and the pass did not land: %v", err)
			return passStalled, nil
		}
		logsink.Info("dream.pass", "nothing surfaced from %d experiences and %d conversation part(s) — no note minted, the surfacing stands",
			len(expIDs), len(talk.Parts))
		return passAdvanced, nil
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
	if n := utf8.RuneCountInString(note); n > d.config.MaxChars {
		logsink.Warn("dream.refusal", "note REFUSED — %d characters over the %d-character bound (prompt.surfacing_max_chars); "+
			"nothing minted, nothing rendered, nothing consumed or advanced, and the prior surfacing stands",
			n, d.config.MaxChars)
		return passStalled, nil
	}

	// .
	// .
	// .
	// .
	pass.Product = map[string]interface{}{
		"id":         "exp_dream_" + outputHash(note),
		"content":    note,
		"category":   "reflection",
		"provenance": "dream",
		"raw":        false,
	}
	if len(talk.Parts) > 0 {
		// .
		// .
		// .
		// .
		// .
		last := talk.Parts[len(talk.Parts)-1]
		pass.Product["dreamed_through"] = ledger.DreamedThrough{Turn: last.ID, Position: uint64(last.From + utf8.RuneCountInString(last.Text))}
	}
	// .
	// .
	// .
	// .
	// .
	// .
	landed, err := d.land.Land(pass)
	if err != nil {
		logsink.Warn("dream.error", "the pass did not land whole: %v", err)
		if landed.Product == nil || (len(expIDs) > 0 && !landed.Marked) {
			// .
			// .
			return passStalled, nil
		}
		// .
		// .
		// .
		// .
		// .
		if d.ringWriter != nil {
			d.ringWriter.SetRingSection(ring.Ring3, "surfacing", note)
		}
		return passStalled, nil
	}

	// .
	// .
	// .
	if d.ringWriter != nil {
		d.ringWriter.SetRingSection(ring.Ring3, "surfacing", note)
		logsink.Info("dream.end", "processed %d experiences and %d conversation part(s) — %d chars to Ring 3 (surfacing) + ledger note", len(expIDs), len(talk.Parts), len(note))
	} else {
		logsink.Info("dream.end", "processed %d experiences and %d conversation part(s) — ledger note only, no ring writer", len(expIDs), len(talk.Parts))
	}
	return passAdvanced, nil
}

// .
func (d *DreamFacility) request(expTexts []string, talk store.ConversationBatch) string {
	var parts []string
	if len(expTexts) > 0 {
		parts = append(parts, fmt.Sprintf("Experiences:\n%s", joinLines(expTexts)))
	}
	if len(talk.Parts) > 0 {
		parts = append(parts, renderConversation(talk))
	}
	userMsg := strings.Join(parts, "\n\n")

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if d.ringWriter != nil {
		if prior := d.ringWriter.RingSection(ring.Ring3, "surfacing"); prior != "" {
			userMsg += "\n\nWhat you surfaced last time (context for noticing what's NEW or connected — do not restate):\n" + prior
		}
	}

	// .
	// .
	// .
	// .
	if view, err := renderTensions(d.tensions, nil, d.config.TensionsMaxChars); err != nil {
		logsink.Warn("dream.error", "%v — the pass runs without the contradiction view", err)
	} else if view != "" {
		userMsg += "\n\nContradictions currently standing in your record:\n" + view
	}
	return userMsg
}

// .
func (d *DreamFacility) SetAuthority(src AuthoritySource) { d.authority = src }

// .
func (d *DreamFacility) SetTensions(ts TensionsSource) { d.tensions = ts }

// .
func (d *DreamFacility) OnAlarm(ctx context.Context, alarmID string, clock string, deadline int64, payload string) AlarmResult {
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
	now := time.Now()
	if d.now != nil {
		now = d.now()
	}
	if !d.stalledAt.IsZero() && now.Sub(d.stalledAt) < consolidateSpacing {
		return AlarmResult{Accepted: false}
	}
	if !d.Predicate(ctx) && !d.ConversationPending() {
		return AlarmResult{Accepted: false}
	}

	// .
	// .
	// .
	outcome, err := d.pass(ctx)
	if outcome == passStalled {
		d.stalledAt = now
		logsink.Warn("dream.refusal", "the pass advanced nothing — DREAM waits %s before it is tried again; the material waits with it", consolidateSpacing)
	}
	if err != nil {
		logsink.Warn("dream.error", "execute error: %v", err)
		return AlarmResult{Accepted: false}
	}

	return AlarmResult{Accepted: true}
}

func joinLines(lines []string) string {
	result := ""
	for i, line := range lines {
		if i > 0 {
			result += "\n"
		}
		result += fmt.Sprintf("- %s", line)
	}
	return result
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
// .
// .
// .
// .
// .
func outputHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:16])
}
