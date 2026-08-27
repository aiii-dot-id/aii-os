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

// DreamConfig holds DREAM facility parameters.
type DreamConfig struct {
	Threshold int // unprocessed_experience_count >= threshold (default 1)
}

// DreamFacility implements DREAM — divergent thinking that finds
// connections in unprocessed experience. Its evidence carries the
// DERIVED TENSIONS VIEW (live contradiction pairs — the resident's
// contradictions surface here, not in minted state; UNCONSCIOUS_V2
// §2.2).
//
// DREAM fires on a life alarm (capacity-gated, R29). It is NEVER idle-gated.
// "Idleness is the absence of input, not the presence of unprocessed material."
//
// OUTPUT (canon DREAM_AND_CONSOLIDATE, Ring 3 direct action — note(text)):
//   - The surfacing lands as a REAL ledger event: experience.create,
//     category "reflection", provenance "dream", already-metabolized
//     (raw=0). Durable, recallable, inspectable — the unconscious's
//     product is part of the identity's record.
//   - The run closes with a dream.run marker minted LAST ({inputs,
//     outputs}); its MATERIALIZER marks the inputs consumed, so consumed
//     state is f(ledger) and survives replay (external review
//     2026-08-20, H6 — the pre-fix bare UPDATE was wiped every boot).
//   - The same text renders as the Ring 3 "surfacing" section (the
//     prompt's "What You're Noticing"), snapshotted for restarts.
//
// DREAM does NOT create tensions or edges. A contradiction the surfacing
// names becomes a CONTRADICTS edge only through the resident's conscious
// edge.create (R3 commit union) — the unconscious observes, the resident
// commits. (Contradiction-minting is deliberately not automated.)
type DreamFacility struct {
	store      ExperienceStore
	llm        LLMCaller
	ledger     LedgerWriter
	ringWriter RingWriter
	config     DreamConfig
	authority  AuthoritySource
	tensions   TensionsSource // nil = no view (tests)
}

// ExperienceStore is the store interface DREAM needs. Deliberately NO
// consumed-marker method: consumption is the run marker's materialized
// effect (H6) — a facility cannot reach the bare UPDATE by construction.
type ExperienceStore interface {
	UnprocessedExperienceCount() (int, error)
	ListRawExperiences(n int) ([]store.Experience, error)
}

// LLMCaller is the interface for LLM calls.
type LLMCaller interface {
	ChatSimple(ctx context.Context, systemPrompt, userMessage string) (text, modelID string, err error)
	// ChatStructured offers ONE tool and accepts either channel back:
	// the tool arguments when the model called it, the text when it did
	// not. Facilities parse the payload the same way either way, which
	// is what keeps a weak local substrate able to consolidate at all —
	// the emission-agnostic contract, with structure when it is on offer.
	ChatStructured(ctx context.Context, systemPrompt, userMessage string, tool llm.ToolDefinition) (payload, modelID string, viaTool bool, err error)
}

// LedgerWriter is the interface for ledger appends.
type LedgerWriter interface {
	Append(eventType ledger.EventType, ring int, payload interface{}, modelID string) (*ledger.Event, error)
}

// NewDream creates a DREAM facility.
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

// Name returns the facility name.
func (d *DreamFacility) Name() string { return "dream" }

// Predicate checks DREAM's material threshold. R29's independent
// budget-remaining gate is not owned by this source predicate.
func (d *DreamFacility) Predicate(ctx context.Context) bool {
	count, err := d.store.UnprocessedExperienceCount()
	if err != nil {
		return false
	}
	return count >= d.config.Threshold
}

// TensionsSource is the derived-contradiction surface DREAM reads.
type TensionsSource interface {
	TensionsView() ([]store.TensionPair, error)
	StatementsFor(ids []string) (map[string]string, error)
}

// Execute runs DREAM: find connections in unprocessed experiences,
// write divergent surfacing to Ring 3 (not Ring 1 — relationship
// observations are Ring 3 working truth, not charter).
func (d *DreamFacility) Execute(ctx context.Context) error {
	if !d.Predicate(ctx) {
		return nil
	}

	// Raw queue, oldest first — guaranteed progress (no starvation under
	// steady note inflow).
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

	// THE OUTPUT-BECOMES-INPUT LOOP (James's constraint, 2026-08-17): the
	// prior surfacing enters ONLY alongside new material — the predicate
	// above guarantees raw experiences exist, so this context enables
	// novelty detection ("connects to what I noticed before" / "genuinely
	// new") instead of each pass being first-impressions. Without new
	// material this code is unreachable: no raw queue, no dream, no
	// rumination-by-iteration-on-nothing.
	if d.ringWriter != nil {
		if prior := d.ringWriter.RingSection(ring.Ring3, "surfacing"); prior != "" {
			userMsg += "\n\nWhat you surfaced last time (context for noticing what's NEW or connected — do not restate):\n" + prior
		}
	}

	// The tensions view: live contradiction pairs, resolved statements
	// when they exist. DREAM's prompt already asks it to name
	// contradictions plainly — here it SEES the standing ones.
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

	systemPrompt, err := withPreamble(d.authority, dreamSystemPrompt)
	if err != nil {
		return fmt.Errorf("DREAM: authority context: %w", err)
	}
	output, modelID, err := d.llm.ChatSimple(ctx, systemPrompt, userMsg)
	if err != nil {
		log.Printf("DREAM: LLM call failed: %v — experiences remain unprocessed", err)
		return nil
	}
	if output == "" {
		// An honest empty pass is a valid pass — but consume nothing
		return nil
	}

	// 1. The product lands as a REAL ledger event (canon note(text)):
	//    category "reflection", provenance "dream", already-metabolized
	//    (raw=0) so it does not re-enter its own processing loop.
	//    Durable: survives restart, recallable by the resident.
	if d.ledger == nil {
		// No door, no durable product — and consumption is the run
		// marker's materialized effect now, so nothing is consumed either.
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
			"raw":        false, // facility product, not raw material
		}, modelID,
	)
	if err != nil {
		log.Printf("DREAM: ledger mint failed: %v — experiences remain unprocessed", err)
		return nil
	}

	// 2. The run marker, minted LAST (commit-marker ordering — crash
	//    before it = clean re-run; the note's content-derived id absorbs
	//    the duplicate). Its materializer marks the inputs consumed:
	//    consumed state is f(ledger), replay restores it (H6).
	if _, err := d.ledger.Append(ledger.EventDreamRun, 3,
		store.FacilityRunPayload{Inputs: expIDs, Outputs: []uint64{noteEvt.Seq}}, modelID); err != nil {
		log.Printf("DREAM: run marker refused: %v — nothing consumed, pass will re-run", err)
		return nil
	}

	// 3. The surfacing section (prompt's "What You're Noticing") —
	//    snapshotted for restart by the persisting ring writer; a display
	//    cache of the minted note, not the truth itself.
	if d.ringWriter != nil {
		d.ringWriter.SetRingSection(ring.Ring3, "surfacing", output)
		log.Printf("DREAM: wrote %d chars to Ring 3 (surfacing) + ledger note", len(output))
	}

	log.Printf("DREAM: processed %d experiences", len(expIDs))
	return nil
}

// SetAuthority wires the authority-preamble source (nil-safe; tests omit it).
func (d *DreamFacility) SetAuthority(src AuthoritySource) { d.authority = src }

// SetTensions wires the derived-contradiction view (nil-safe).
func (d *DreamFacility) SetTensions(ts TensionsSource) { d.tensions = ts }

// OnAlarm handles TIME alarm dispatch.
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

// outputHash derives a stable id suffix from content. It is the id of a
// DURABLE ROW, so it has to be a hash nothing can collide.
//
// It was FNV-1a 32-bit, with a comment calling collisions harmless
// because the write is INSERT OR REPLACE. That is what makes them
// harmful: two dream notes sharing a suffix means the second REPLACES
// the first, and two beliefs sharing one means ON CONFLICT rewrites an
// unrelated statement — identity truth quietly overwritten by an
// unrelated thought. Birthday collisions arrive around tens of
// thousands of products, which is one identity's lifetime.
//
// Crafted collisions were the sharper half. 32 bits is trivially
// brute-forced, and dream and consolidation content is derived from
// material the identity READ — a message, a fetched page. Choosing
// which belief gets overwritten should not be within reach of anyone
// who can get text in front of the identity.
//
// SHA-256 truncated to 128 bits: the same primitive and the same
// assumption the ledger's own content hashes already rest on.
func outputHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:16])
}
