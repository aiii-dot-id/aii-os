package cognitive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"

	"github.com/aiii-dot-id/aii-os/internal/llm"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
type DreamConfig struct {
	Threshold int
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
// .
// .
// .
// .
// .
// .
type DreamFacility struct {
	store      ExperienceStore
	llm        LLMCaller
	ledger     LedgerWriter
	ringWriter RingWriter
	config     DreamConfig
	authority  AuthoritySource
	tensions   TensionsSource
}

// .
// .
// .
type ExperienceStore interface {
	UnprocessedExperienceCount() (int, error)
	ListRawExperiences(n int) ([]store.Experience, error)
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
func NewDream(store ExperienceStore, llm LLMCaller, lg LedgerWriter, ringWriter RingWriter, cfg DreamConfig) *DreamFacility {
	if cfg.Threshold == 0 {
		cfg.Threshold = 1
	}
	return &DreamFacility{
		store:      store,
		llm:        llm,
		ledger:     lg,
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
type TensionsSource interface {
	TensionsView() ([]store.TensionPair, error)
	StatementsFor(ids []string) (map[string]string, error)
}

// .
// .
// .
func (d *DreamFacility) Execute(ctx context.Context) error {
	if !d.Predicate(ctx) {
		return nil
	}

	// .
	// .
	experiences, err := d.store.ListRawExperiences(20)
	if err != nil {
		return fmt.Errorf("dream: list raw experiences: %w", err)
	}

	var expTexts []string
	var expIDs []string
	for _, e := range experiences {
		expTexts = append(expTexts, evidenceText(e))
		expIDs = append(expIDs, e.ID)
	}

	if len(expTexts) == 0 {
		return nil
	}

	userMsg := fmt.Sprintf("Experiences:\n%s", joinLines(expTexts))

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
	if d.tensions != nil {
		if pairs, err := d.tensions.TensionsView(); err == nil && len(pairs) > 0 {
			ids := make([]string, 0, len(pairs)*2)
			for _, p := range pairs {
				ids = append(ids, p.LeftID, p.RightID)
			}
			stmts, _ := d.tensions.StatementsFor(ids)
			var tlines []string
			for _, p := range pairs {
				l, lok := stmts[p.LeftID]
				r, rok := stmts[p.RightID]
				if lok && rok {
					tlines = append(tlines, fmt.Sprintf("- %q stands against %q", l, r))
				} else {
					tlines = append(tlines, fmt.Sprintf("- %s stands against %s", p.LeftID, p.RightID))
				}
			}
			userMsg += "\n\nContradictions currently standing between your beliefs:\n" + joinLines(tlines)
		}
	}

	callCtx, systemPrompt, err := withPreamble(ctx, d.authority, dreamSystemPrompt)
	if err != nil {
		return fmt.Errorf("DREAM: authority context: %w", err)
	}
	output, modelID, err := d.llm.ChatSimple(callCtx, systemPrompt, userMsg)
	if err != nil {
		log.Printf("DREAM: LLM call failed: %v — experiences remain unprocessed", err)
		return nil
	}
	if output == "" {
		// .
		return nil
	}

	// .
	// .
	// .
	// .
	if d.ledger == nil {
		// .
		// .
		log.Printf("DREAM: no ledger door — surfacing not minted, experiences remain unprocessed")
		return nil
	}
	noteEvt, err := d.ledger.Append(
		ledger.EventExperienceCreate,
		3,
		map[string]interface{}{
			"id":         "exp_dream_" + outputHash(output),
			"content":    output,
			"category":   "reflection",
			"provenance": "dream",
			"raw":        false,
		}, modelID,
	)
	if err != nil {
		log.Printf("DREAM: ledger mint failed: %v — experiences remain unprocessed", err)
		return nil
	}

	// .
	// .
	// .
	// .
	if _, err := d.ledger.Append(ledger.EventDreamRun, 3,
		store.FacilityRunPayload{Inputs: expIDs, Outputs: []uint64{noteEvt.Seq}}, modelID); err != nil {
		log.Printf("DREAM: run marker refused: %v — nothing consumed, pass will re-run", err)
		return nil
	}

	// .
	// .
	// .
	if d.ringWriter != nil {
		d.ringWriter.SetRingSection(ring.Ring3, "surfacing", output)
		log.Printf("DREAM: wrote %d chars to Ring 3 (surfacing) + ledger note", len(output))
	}

	log.Printf("DREAM: processed %d experiences", len(expIDs))
	return nil
}

// .
func (d *DreamFacility) SetAuthority(src AuthoritySource) { d.authority = src }

// .
func (d *DreamFacility) SetTensions(ts TensionsSource) { d.tensions = ts }

// .
func (d *DreamFacility) OnAlarm(ctx context.Context, alarmID string, clock string, deadline int64, payload string) AlarmResult {
	if !d.Predicate(ctx) {
		return AlarmResult{Accepted: false}
	}

	if err := d.Execute(ctx); err != nil {
		log.Printf("DREAM: execute error: %v", err)
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
