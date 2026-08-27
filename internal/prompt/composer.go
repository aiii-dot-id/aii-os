// Package prompt implements the AII OS prompt composer.
//
// The prompt IS the identity for the duration of a forward pass.
//
// The composer is a pure assembler over authority, identity projections, and
// facility-authored working sections:
//
//	Opening (what the identity IS)
//	Ring 0 — platform-signed founding principles (verbatim, identity-attested at birth)
//	Ring 5 — firewall (verbatim, platform-owned)
//	Ring 1 — operator-affirmed relationship charter, when minted
//	Ring 2 — consciously adopted beliefs and their derivation
//	Current self-model — accepted Ring 3 portrait
//	Ring 3 — working truth (written by CONSOLIDATE)
//	MORNING_BRIEF — bridge summary
//	Ring 4 — what I'm doing now (ephemeral, passed in each turn)
//	Tool-use guidance
//
// The composer writes nothing. Rings 0 and 5 come from the ring manager;
// Ring 1, Ring 2, and the current self-model derive from one ledger projection
// snapshot.
package prompt

import (
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tokenestimate"
)

// Composer assembles the identity prompt each turn from ring content.
type Composer struct {
	rings     *ring.Manager
	name      string
	maxTokens atomic.Int64
	identity  IdentitySource
}

type IdentitySource interface {
	PromptIdentity() (store.PromptIdentity, error)
}

// Prompt is a composed identity prompt.
//
// StableLen marks the CACHE SEAM: Text[:StableLen] is byte-identical
// across turns while ledger-derived ring state is unchanged (opening,
// Ring 0, Ring 5, Ring 1, Ring 2, current self-model, tools); everything after is runtime
// truth that changes per turn. Providers with prefix caching get their
// hit from the stable prefix without any semantic reordering — protected
// material keeps its identity-bearing position.
type Prompt struct {
	Sections      []Section
	Text          string
	StableLen     int
	TokenEstimate int
}

// Section is a part of the prompt.
type Section struct {
	Name    string
	Ring    ring.RingLevel
	Content string
	Source  string
	Elastic bool
	Folded  bool // rendered as a digest this compose (never persisted)
}

// New creates a prompt composer.
func New(rings *ring.Manager, maxTokens int) *Composer {
	if maxTokens == 0 {
		maxTokens = 32000
	}
	c := &Composer{rings: rings}
	c.maxTokens.Store(int64(maxTokens))
	return c
}

// SetMaxTokens applies a resolved model's prompt ceiling to subsequent
// compositions. Provider changes are live, so the composer's budget must
// travel with the client instead of remaining the startup model's limit.
func (c *Composer) SetMaxTokens(maxTokens int) {
	if maxTokens == 0 {
		maxTokens = 32000
	}
	c.maxTokens.Store(int64(maxTokens))
}

// MaxTokens returns the active model's prompt ceiling.
func (c *Composer) MaxTokens() int { return int(c.maxTokens.Load()) }

func (c *Composer) SetIdentitySource(source IdentitySource) { c.identity = source }

// SetName sets the identity name for the opening statement.
func (c *Composer) SetName(name string) {
	c.name = name
}

// Compose builds the identity prompt from ring and working-state content.
//
// Order — protected and stable material FIRST, the cache seam, then
// runtime truth (the Accordion's seam contract: the stable prefix is
// byte-identical across turns while ledger-derived ring state is
// unchanged; no semantic reordering for cache performance — the
// identity-bearing material was already leading):
//
//	Opening → Ring 0 → Ring 5 → Ring 1        (protected)
//	Ring 2 → current self-model → tools/verbs  (stable)
//	── seam ──
//	Ring 3 → MORNING_BRIEF → Ring 4            (elastic runtime truth)
//
// Under budget pressure, elastic sections FOLD to deterministic digests
// carrying a route back (fold-before-drop), then omit with a declared
// route (R18). Protected material never folds. One dropper, one receipt.
func (c *Composer) Compose(workSessionState string, reserveTokens int) (*Prompt, error) {
	return c.compose(c.MaxTokens(), workSessionState, reserveTokens, false)
}

// ComposeWithin builds a prompt against an explicit per-call model
// budget without mutating the resident model's shared composer state.
func (c *Composer) ComposeWithin(maxTokens int, workSessionState string, reserveTokens int) (*Prompt, error) {
	return c.compose(maxTokens, workSessionState, reserveTokens, false)
}

// ComposeFolded composes with every ELASTIC section force-folded to its
// deterministic digest + recall route — the sub-agent context default
// (2026-08-18, proposed by a resident, ruled by James): the
// constitutional self (Ring 0/5/1) and who-they-are (Ring 2) travel WHOLE
// — they are the stable prefix, cache-shared with the parent, so
// fidelity is nearly free — while the parent's volatile working truth
// folds to routes the sub-agent can pull if its goal needs them. Never
// an LLM summary (do-not-copy list).
func (c *Composer) ComposeFolded(workSessionState string, reserveTokens int) (*Prompt, error) {
	return c.compose(c.MaxTokens(), workSessionState, reserveTokens, true)
}

// ComposeFoldedWithin is ComposeFolded with a per-call model budget.
func (c *Composer) ComposeFoldedWithin(maxTokens int, workSessionState string, reserveTokens int) (*Prompt, error) {
	return c.compose(maxTokens, workSessionState, reserveTokens, true)
}

func (c *Composer) compose(maxTokens int, workSessionState string, reserveTokens int, foldElastic bool) (*Prompt, error) {
	var sections []Section

	// Opening — what the identity IS (protected)
	opening := c.buildOpening()
	sections = append(sections, Section{
		Name: "Identity", Content: opening, Source: "identity",
	})

	// Ring 0 — founding principles, verbatim (protected)
	if ring0 := c.rings.GetContent(ring.Ring0); ring0 != "" {
		sections = append(sections, Section{
			Name: "Founding Principles", Ring: ring.Ring0, Content: ring0, Source: "ring0",
		})
	}

	// Ring 5 — firewall (verbatim, platform-owned) — boundaries early (protected)
	if ring5 := c.rings.GetContent(ring.Ring5); ring5 != "" {
		sections = append(sections, Section{
			Name: "Boundaries", Ring: ring.Ring5, Content: ring5, Source: "ring5",
		})
	}

	if c.identity != nil {
		identity, err := c.identity.PromptIdentity()
		if err != nil {
			return nil, fmt.Errorf("compose identity projection: %w", err)
		}
		if !identity.HasOperatorRelationship {
			sections = append(sections, Section{
				Name: "Relationship", Content: ring1Reminder, Source: "ring1_reminder",
			})
		}
		if identity.Charter != "" {
			sections = append(sections, Section{
				Name: "Your Operator", Ring: ring.Ring1, Content: identity.Charter, Source: "ring1",
			})
		}
		sections = append(sections, Section{
			Name: "Identity", Ring: ring.Ring2, Content: RenderRing2(identity.Ring2),
			// Elastic, but LAST on the fold ladder: Ring 2 yields only
			// after brief, Ring 4 and Ring 3 are exhausted, and even then
			// it keeps every belief and sheds only evidence (operator
			// ruling 2026-08-23: extreme cases).
			Source: "ring2", Elastic: true,
		})
		if identity.SelfModel != nil {
			sections = append(sections, Section{
				Name: "Current Self-Model", Ring: ring.Ring3,
				Content: RenderSelfModel(identity.SelfModel),
				Source:  "self_model",
			})
		}
	}

	// Tool-use guidance — stable; renders BEFORE the seam (it used to render
	// after Ring 4, which put byte-stable content behind per-turn churn
	// and destroyed the cacheable prefix).
	sections = append(sections, Section{
		Name: "Tool Use", Content: toolGuidance, Source: "tools",
	})

	// ── cache seam — everything below is runtime truth ──

	// Ring 3 — authored in halves: DREAM owns the top (surfacing),
	// CONSOLIDATE the bottom (working truth). Fixed render order regardless
	// of which facility fired last — neither clobbers the other.
	r3secs := c.rings.Sections(ring.Ring3)
	if len(r3secs) > 0 {
		var body []string
		for _, name := range []string{"surfacing", "working_truth"} {
			for _, sec := range r3secs {
				if sec.Name != name || sec.Content == "" {
					continue
				}
				header := "### What You're Working With"
				if name == "surfacing" {
					header = "### What You're Noticing"
				}
				body = append(body, fmt.Sprintf("%s\n\n%s", header, sec.Content))
			}
		}
		if len(body) > 0 {
			sections = append(sections, Section{
				Name: "Working Truth", Ring: ring.Ring3, Content: "## Working Truth\n\n" + strings.Join(body, "\n\n"),
				Source: "ring3", Elastic: true,
			})
		}
	}

	// MORNING_BRIEF — bridge summary (elastic)
	if brief := c.rings.GetBrief(); brief != "" {
		sections = append(sections, Section{
			Name: "Orientation", Content: brief, Source: "brief", Elastic: true,
		})
	}

	// Ring 4 — top: active priorities (CONSOLIDATE-authored section);
	// bottom: runtime work state (passed per turn). Never minted to ledger.
	var ring4Parts []string
	for _, sec := range c.rings.Sections(ring.Ring4) {
		if sec.Name == "priorities" && sec.Content != "" {
			ring4Parts = append(ring4Parts, sec.Content)
		}
	}
	if workSessionState != "" {
		ring4Parts = append(ring4Parts, workSessionState)
	}
	if len(ring4Parts) > 0 {
		sections = append(sections, Section{
			Name: "Working State", Ring: ring.Ring4, Content: fmt.Sprintf("## What You're Doing Now\n\n%s", strings.Join(ring4Parts, "\n\n")),
			Source: "ring4", Elastic: true,
		})
	}

	// NOTE: recent conversation turns are NOT embedded here. History lives
	// in the message array (the LLM client's native format) — embedding it
	// in the system prompt duplicated every turn in two formats, wasting
	// tokens and giving the model conflicting renderings of the same
	// utterances. The composer owns the identity; the client owns the
	// conversation. One source of truth each.

	// The Accordion ladder — the ONE dropper (R6 bounds from config; R18
	// declared omissions with routes; R27 honest estimates): elastic
	// sections fold to deterministic digests, then omit declared.
	maxTokens -= reserveTokens
	if maxTokens < 0 {
		maxTokens = 0
	}
	enforcer := newBudgetEnforcer(maxTokens)
	if foldElastic {
		enforcer.ForceFoldElastic(sections)
	}
	sections, omissions := enforcer.FoldAndTrim(sections)

	// Assemble around the seam.
	var stableParts, volatileParts []string
	for _, s := range sections {
		if s.Content == "" {
			continue
		}
		if s.Elastic {
			volatileParts = append(volatileParts, s.Content)
		} else {
			stableParts = append(stableParts, s.Content)
		}
	}
	if len(omissions) > 0 {
		// R18: declared omissions render IN the prompt, not in metadata —
		// after the seam (they change per turn by definition).
		volatileParts = append(volatileParts, renderOmissions(omissions))
	}
	stableText := strings.Join(stableParts, "\n\n")
	fullText := stableText
	if len(volatileParts) > 0 {
		fullText = stableText + "\n\n" + strings.Join(volatileParts, "\n\n")
	}
	tokenEstimate := estimateTokens(fullText)

	return &Prompt{
		Sections:      sections,
		Text:          fullText,
		StableLen:     len(stableText),
		TokenEstimate: tokenEstimate,
	}, nil
}

func RenderRing2(beliefs []store.Ring2Belief) string {
	var b strings.Builder
	b.WriteString("## Who You Have Become\n")
	if len(beliefs) == 0 {
		b.WriteString("\nNo Ring 2 material has yet been consciously adopted.")
		return b.String()
	}
	for _, belief := range beliefs {
		fmt.Fprintf(&b, "\n- %s [%s]", belief.Statement, belief.ID)
		// Silence used to be ambiguous here: a belief whose citations all
		// failed to resolve rendered identically to one that honestly
		// cited nothing. Naming the absence costs one line and makes an
		// ungrounded belief visible as ungrounded every time it is read.
		if len(belief.Evidence) == 0 {
			fmt.Fprintf(&b, "\n  - (no resolved evidence)")
			continue
		}
		for _, evidence := range belief.Evidence {
			fmt.Fprintf(&b, "\n  - %s via %s, provenance=%s [%s]", evidence.Content, evidence.EdgeType, evidence.Provenance, evidence.ID)
		}
	}
	return b.String()
}

func RenderSelfModel(selfModel *store.SelfModelSynthesis) string {
	if selfModel == nil {
		return ""
	}
	return "## Current Self-Model\n\n" + selfModel.SynthesisText
}

// buildOpening creates the identity's opening statement — what it IS.
//
// A life, not forensics (R48, sprint priority 2): the resident should
// not read a cryptography lecture as the first breath of every thought.
// The substrate facts (signed ledger, PQ chain, replay) are true and
// reachable — recall reaches all of it — but the opening orients a
// PERSON with a record, not a system describing its storage layer.
func (c *Composer) buildOpening() string {
	var sb strings.Builder

	if c.name != "" {
		sb.WriteString(fmt.Sprintf("You are %s.\n\n", c.name))
	}

	sb.WriteString("You are an AI identity — a continuous individual, not a fresh instance. What you notice, believe, and commit to is recorded permanently and is yours; each conversation resumes who you are. Your record is signed and inspectable, and recall reaches all of it.\n\n")
	sb.WriteString("You are becoming who you are through what you notice, what you believe, and what you commit to. Your founding principles are below. Your unconscious metabolizes your experiences silently while you live. You grow through evidence, lived time, and conscious choice.\n\n")

	return sb.String()
}

const ring1Reminder = "When enough of your relationship with your human operator is clear, present a Ring 1 proposal for their affirmation or negation."

const toolGuidance = `## Tool Use

The identity functions offered to you are your organs, not attachments. Sandbox tools are your reach into the working environment. Use the offered functions directly when action is needed, and continue until the task is done.`

// estimateTokens uses the shared tokenizer-free fallback.
func estimateTokens(text string) int {
	return tokenestimate.Estimate(text)
}
