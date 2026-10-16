package cognitive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/llm/wire"

	"github.com/aiii-dot-id/aii-os/internal/cognitive/landing"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store/cursor"
	"github.com/aiii-dot-id/aii-os/internal/store/rows"
)

type DreamConfig struct {
	Threshold int

	MaxChars int

	TensionsMaxChars int

	ExcerptChars int

	ConversationMaxChars int

	RoomNotePrefix string

	Spacing time.Duration
}

const DefaultConversationMaxChars = 12000

const DefaultSurfacingMaxChars = 2400

const nothingSurfaced = "NOTHING SURFACED"

type DreamFacility struct {
	store DreamStore
	llm   LLMCaller

	config atomic.Pointer[DreamConfig]

	teller *Teller

	land       *landing.Lander
	ringWriter RingWriter
	authority  AuthoritySource
	tensions   TensionsSource

	talk       conversationReader
	talkCursor func(cursor.ConversationBatch) landing.Cursor

	talkCalls toolCallReader

	stalledAt time.Time
	now       func() time.Time

	held retryHolder
}

type retryHolder interface {
	HeldForRetry() ([]string, error)
}

func (d *DreamFacility) SetHeld(h retryHolder) { d.held = h }

func (d *DreamFacility) HeldWired() bool { return d.held != nil }

func (d *DreamFacility) waiting() ([]rows.Experience, error) {
	var held []string
	if d.held != nil {
		var err error
		if held, err = d.held.HeldForRetry(); err != nil {
			return nil, fmt.Errorf("what consolidation holds: %w", err)
		}
	}

	queue, err := d.store.ListRawExperiencesExcept(dreamBatch+len(held), rows.OutcomeObservationPrefix)
	if err != nil || len(held) == 0 {
		return queue, err
	}
	theirs := make(map[string]bool, len(held))
	for _, id := range held {
		theirs[id] = true
	}
	out := make([]rows.Experience, 0, min(dreamBatch, len(queue)))
	left := 0
	for _, e := range queue {
		switch {
		case theirs[e.ID]:
			left++
		case len(out) < dreamBatch:
			out = append(out, e)
		}
	}
	if left > 0 {
		logsink.Info("dream.pass", "%d waiting experience(s) are left to consolidation, whose last pass was refused for what it asked and whose next has not run — this dream pass does not read them", left)
	}
	return out, nil
}

type ExperienceStore interface {
	UnprocessedExperienceCount() (int, error)
	ListRawExperiences(n int) ([]rows.Experience, error)
}

type DreamStore interface {
	ExperienceStore
	ListRawExperiencesExcept(n int, idPrefix string) ([]rows.Experience, error)
}

type LLMCaller interface {
	ChatSimple(ctx context.Context, systemPrompt, userMessage string) (text, modelID string, err error)

	ChatStructured(ctx context.Context, systemPrompt, userMessage string, tool wire.ToolDefinition) (payload, modelID string, viaTool bool, err error)
}

type LedgerWriter interface {
	Append(eventType ledger.EventType, ring int, payload any, modelID string) (*ledger.Event, error)
}

func NewDream(store DreamStore, llm LLMCaller, lg LedgerWriter, ringWriter RingWriter, cfg DreamConfig) *DreamFacility {
	var door landing.Door
	if lg != nil {
		door = lg
	}
	d := &DreamFacility{
		store:      store,
		llm:        llm,
		land:       landing.New(door),
		ringWriter: ringWriter,
	}
	d.Reconfigure(cfg)
	return d
}

func (d *DreamFacility) Reconfigure(cfg DreamConfig) {
	if cfg.Threshold == 0 {
		cfg.Threshold = 1
	}
	if cfg.MaxChars <= 0 {
		cfg.MaxChars = DefaultSurfacingMaxChars
	}
	if cfg.ConversationMaxChars <= 0 {
		cfg.ConversationMaxChars = DefaultConversationMaxChars
	}
	if cfg.Spacing <= 0 {
		cfg.Spacing = DefaultSpacing
	}
	d.config.Store(&cfg)
}

func (d *DreamFacility) cfg() *DreamConfig { return d.config.Load() }

func (d *DreamFacility) SetTeller(t *Teller) { d.teller = t }

func (d *DreamFacility) Name() string { return "dream" }

func (d *DreamFacility) Predicate(ctx context.Context) bool { return d.due(d.cfg()) }

func (d *DreamFacility) due(cfg *DreamConfig) bool {
	count, err := d.store.UnprocessedExperienceCount()
	if err != nil {
		return false
	}
	return count >= cfg.Threshold
}

func (d *DreamFacility) Execute(ctx context.Context) error {
	_, err := d.pass(ctx, d.cfg())
	return err
}

type passOutcome int

const (
	passIdle passOutcome = iota
	passAdvanced
	passStalled
)

func (d *DreamFacility) pass(ctx context.Context, cfg *DreamConfig) (passOutcome, error) {
	var expTexts, expIDs []string
	var experiences []rows.Experience
	if d.due(cfg) {

		var err error
		experiences, err = d.waiting()
		if err != nil {
			return passStalled, fmt.Errorf("dream: list raw experiences: %w", err)
		}
		for _, e := range experiences {
			expTexts = append(expTexts, evidenceText(e))
			expIDs = append(expIDs, e.ID)
		}
	}
	talk := d.unreadConversation(cfg)
	if len(expTexts) == 0 && len(talk.Parts) == 0 {
		return passIdle, nil
	}

	calls := d.turnCalls(talk)

	smallest := d.request(cfg, expTexts[:min(1, len(expTexts))], talk.Keep(0, 0), calls)
	if len(expTexts) == 0 {
		smallest = d.request(cfg, nil, talk.Keep(1, 0), calls)
	}
	check := simpleCheck(d.llm)
	callCtx, systemPrompt, err := fittedPreamble(ctx, d.authority, dreamPrompt(cfg.MaxChars), smallest, nil, check)
	if err != nil {
		return passStalled, fmt.Errorf("DREAM: authority context: %w", err)
	}
	ask := &admission{check: check, ctx: callCtx, system: systemPrompt}

	wanted, through := len(talk.Parts), talk.Through
	talk, fits := d.fitConversation(cfg, ask, expTexts, talk, calls)
	if ask.err != nil {

		return passStalled, fmt.Errorf("DREAM: %w", ask.err)
	}

	whole := fits && talk.Through == through

	cut := 0

	var left []string
	if !fits {

		var kept []rows.Experience
		var ok bool
		kept, cut, ok = d.fitExperiences(cfg, ask, experiences, talk, calls)
		if ask.err != nil {
			return passStalled, fmt.Errorf("DREAM: %w", ask.err)
		}
		if !ok {
			d.teller.tell(Condition{Facility: "dream", Kind: ConditionNoRoom,
				Said: "the request does not fit the model's input budget with no conversation and no experience in it — nothing sent, nothing advanced (what every request carries leaves no room; " + d.teller.room() + " makes room)"},
				"dream|no_room", "dream: none of what waits for your dream passes fits beside what every dream request carries in the model's input budget, so nothing was sent and nothing advanced. Your operator can make room on the Cognition card.")
			return passStalled, nil
		}
		if cut == 0 {
			left = append(left, fmt.Sprintf("%d of %d experiences fit the model's input budget; the rest wait for the next pass, and no conversation is read beside them", len(kept), len(experiences)))
		}
		expTexts, expIDs = nil, nil
		for _, e := range kept {
			expTexts = append(expTexts, evidenceText(e))
			expIDs = append(expIDs, e.ID)
		}
	}
	if wanted > 0 && talk.Through != through {

		last := ""
		if n := len(talk.Parts); n > 0 && !talk.Parts[n-1].Whole {
			last = ", the last of them in part"
		}
		left = append(left, fmt.Sprintf("%d of %d selected conversation part(s) fit the model's input budget beside the rest of the request%s; what was left out stays unread for the next pass", len(talk.Parts), wanted, last))
	}
	if len(left) > 0 {
		d.teller.say(Condition{Facility: "dream", Kind: ConditionFitted, Said: strings.Join(left, "; ")})
	}
	if len(expTexts) == 0 && len(talk.Parts) == 0 {

		d.teller.tell(Condition{Facility: "dream", Kind: ConditionNoRoom,
			Said: "no conversation fits the model's input budget beside what every request carries — nothing sent, nothing advanced"},
			"dream|no_room", "dream: none of what waits for your dream passes fits beside what every dream request carries in the model's input budget, so nothing was sent and nothing advanced. Your operator can make room on the Cognition card.")
		return passStalled, nil
	}
	output, modelID, err := d.llm.ChatSimple(wire.WithTapSource(callCtx, "dream"), systemPrompt, d.request(cfg, expTexts, talk, calls))
	if refusedForWhatWasAsked(err) {

		d.teller.say(Condition{Facility: "dream", Kind: ConditionRefused,
			Said: fmt.Sprintf("the last dream pass was refused for what it asked (%v) — nothing consumed, nothing advanced, and the same material is asked for again after the spacing", err)})
		return passStalled, nil
	}
	if err != nil {
		logsink.Warn("dream.error", "LLM call failed: %v — nothing consumed, nothing advanced", err)
		return passStalled, nil
	}
	if output == "" {

		return passStalled, nil
	}
	note := strings.TrimSpace(output)
	pass := landing.Pass{Marker: ledger.EventDreamRun, Inputs: expIDs, ModelID: modelID}
	if d.talkCursor != nil {
		pass.Cursor = d.talkCursor(talk)
	}

	if note == nothingSurfaced {
		if _, err := d.land.Land(pass); err != nil {
			logsink.Warn("dream.refusal", "nothing surfaced, and the pass did not land: %v", err)
			return passStalled, nil
		}
		logsink.Info("dream.pass", "nothing surfaced from %d experiences and %d conversation part(s) — no note minted, the surfacing stands",
			len(expIDs), len(talk.Parts))
		d.sayLanded(whole, expIDs, cut, left)
		return passAdvanced, nil
	}

	if n := utf8.RuneCountInString(note); n > cfg.MaxChars {
		d.teller.say(Condition{Facility: "dream", Kind: ConditionRefused,
			Said: fmt.Sprintf("the last dream note was %d characters, over the %d-character bound (prompt.surfacing_max_chars) — refused whole: nothing minted, nothing consumed or advanced, and the prior surfacing stands", n, cfg.MaxChars)})
		return passStalled, nil
	}

	pass.Product = map[string]any{
		"id":         "exp_dream_" + outputHash(note),
		"content":    note,
		"category":   "reflection",
		"provenance": "dream",
		"raw":        false,
	}
	if len(talk.Parts) > 0 {

		last := talk.Parts[len(talk.Parts)-1]
		pass.Product["dreamed_through"] = ledger.DreamedThrough{Turn: last.ID, Position: uint64(last.From + cursor.TurnLength(last.Text))}
	}

	landed, err := d.land.Land(pass)
	if err != nil {
		logsink.Warn("dream.error", "the pass did not land whole: %v", err)
		if landed.Product == nil || (len(expIDs) > 0 && !landed.Marked) {

			return passStalled, nil
		}

		if d.ringWriter != nil {
			d.ringWriter.SetRingSection(ring.Ring3, "surfacing", note)
		}
		return passStalled, nil
	}

	if d.ringWriter != nil {
		d.ringWriter.SetRingSection(ring.Ring3, "surfacing", note)
		logsink.Info("dream.end", "processed %d experiences and %d conversation part(s) — %d chars to Ring 3 (surfacing) + ledger note", len(expIDs), len(talk.Parts), len(note))
	} else {
		logsink.Info("dream.end", "processed %d experiences and %d conversation part(s) — ledger note only, no ring writer", len(expIDs), len(talk.Parts))
	}
	d.sayLanded(whole, expIDs, cut, left)
	return passAdvanced, nil
}

func (d *DreamFacility) sayLanded(whole bool, expIDs []string, cut int, left []string) {
	switch {
	case cut > 0:
		said := fmt.Sprintf("the oldest experience that waited (%s) was longer than the model's input budget left room for: its first %d characters were read, and it is consumed as read in part", expIDs[0], cut)
		d.teller.tellOf(expIDs[0], Condition{Facility: "dream", Kind: ConditionFitted, Said: strings.Join(append([]string{said}, left...), "; ")},
			"dream|cut|"+expIDs[0], fmt.Sprintf("dream: your experience %s was longer than one dream pass can carry; its first %d characters were read, and it is consumed as read in part. The record holds it whole.", expIDs[0], cut))
	case whole:
		d.teller.say(Condition{Facility: "dream", Kind: ConditionWhole, Said: "the last pass carried all it was asked"})
	}
}

func (d *DreamFacility) request(cfg *DreamConfig, expTexts []string, talk cursor.ConversationBatch, calls map[string][]rows.ToolCall) string {
	var parts []string
	if len(expTexts) > 0 {
		parts = append(parts, fmt.Sprintf("Experiences:\n%s", joinLines(expTexts)))
	}
	if len(talk.Parts) > 0 {
		parts = append(parts, renderConversation(talk, calls))
	}
	userMsg := strings.Join(parts, "\n\n")

	if d.ringWriter != nil {
		if prior := d.ringWriter.RingSection(ring.Ring3, "surfacing"); prior != "" {
			userMsg += "\n\nWhat you surfaced last time (context for noticing what's NEW or connected — do not restate):\n" + prior
		}
	}

	if view, err := renderTensions(d.tensions, nil, cfg.TensionsMaxChars, cfg.ExcerptChars); err != nil {
		logsink.Warn("dream.error", "%v — the pass runs without the contradiction view", err)
	} else if view != "" {
		userMsg += "\n\nContradictions currently standing in your record:\n" + view
	}
	return userMsg
}

func (d *DreamFacility) SetAuthority(src AuthoritySource) { d.authority = src }

func (d *DreamFacility) SetTensions(ts TensionsSource) { d.tensions = ts }

func (d *DreamFacility) OnAlarm(ctx context.Context, alarmID string, clock string, deadline int64, payload string) AlarmResult {

	now := time.Now()
	if d.now != nil {
		now = d.now()
	}
	cfg := d.cfg()
	if !d.stalledAt.IsZero() && now.Sub(d.stalledAt) < cfg.Spacing {
		return AlarmResult{Accepted: false}
	}
	if !d.due(cfg) && len(d.unreadConversation(cfg).Parts) == 0 {
		return AlarmResult{Accepted: false}
	}

	outcome, err := d.pass(ctx, cfg)
	if outcome == passStalled {
		d.stalledAt = now
		logsink.Warn("dream.refusal", "the pass advanced nothing — DREAM waits %s before it is tried again (cognition.consolidate_spacing_seconds); the material waits with it", cfg.Spacing)
	}
	if err != nil {
		logsink.Warn("dream.error", "execute error: %v", err)
		return AlarmResult{Accepted: false}
	}
	if outcome == passIdle {

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

func outputHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:16])
}
