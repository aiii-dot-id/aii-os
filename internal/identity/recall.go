package identity

import (
	"context"
	"fmt"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// --- recall: remembering — grouped honest read (R1) ---

func (e *Engine) verbRecall(_ context.Context, args map[string]interface{}) (string, error) {
	query, _ := args["query"].(string)
	if query == "" {
		query, _ = args["_positional"].(string)
	}
	q := strings.ToLower(query)

	// Pass-through cursor (R1: the SOURCE owns it, recall passes it
	// through): after_seq pages older than the given created_seq. The
	// footer's "enumerate fully" promise is now true.
	var cursor uint64
	if v, ok := args["after_seq"].(float64); ok && v > 0 {
		cursor = uint64(v)
	} else if v, ok := args["after_seq"].(int); ok && v > 0 {
		cursor = uint64(v)
	}
	pageFloor := cursor
	if pageFloor == 0 {
		pageFloor = 1 << 62 // first page: everything is "older than" this
	}

	// R1/R45: source-grouped sections, match disclosure, IDs shown for
	// re-fetchability, and abstention distinct from emptiness — matched-
	// nothing and nothing-recorded are different honest answers.
	type group struct {
		name  string
		items []string
	}
	var groups []group
	recorded := 0
	available := 0
	var failedSources, failureDetails []string
	var firstFailure error
	fail := func(source string, err error) {
		failedSources = append(failedSources, source)
		failureDetails = append(failureDetails, fmt.Sprintf("%s: %v", source, err))
		if firstFailure == nil {
			firstFailure = err
		}
	}

	if beliefs, err := e.store.ListBeliefs(); err != nil {
		fail("beliefs", err)
	} else {
		available++
		g := group{name: "Beliefs"}
		for _, b := range beliefs {
			recorded++
			if q != "" && !strings.Contains(strings.ToLower(b.Statement), q) {
				continue
			}
			// Resolved evidence, always, including zero.
			//
			// A citation that names an entity which does not exist never
			// becomes an edge — the minter refuses it and standing.go
			// skips it ("ghost edge — certifies nothing"), so a phantom
			// can never confer standing. But it was also INVISIBLE: a
			// belief citing three phantoms rendered exactly like one that
			// honestly cited nothing, and the resident who wrote those
			// three ids had no way to see they had not landed until
			// something tried to build on them days later.
			//
			// Shown beside standing, never alone: the count is raw
			// supporting edges, while standing applies the
			// authorship-equivalence rule. Twelve edges at standing "new"
			// says they do not span independent voices — which is the
			// honest reading, and is only available because both are
			// present.
			g.items = append(g.items, fmt.Sprintf("  [%s, %s, ring %d, evidence %d] %s",
				b.ID, e.store.StandingFor(b.ID), b.Ring, b.EvidenceCount, b.Statement))
		}
		groups = append(groups, g)
	}

	if syntheses, err := e.pagedSelfModels(pageFloor); err != nil {
		fail("self-model syntheses", err)
	} else {
		available++
		g := group{name: "Self-Model Syntheses"}
		for _, synthesis := range syntheses {
			recorded++
			if q != "" && !strings.Contains(strings.ToLower(synthesis.SynthesisText), q) {
				continue
			}
			g.items = append(g.items, fmt.Sprintf("  [%s] %s", synthesis.ID, synthesis.SynthesisText))
		}
		groups = append(groups, g)
	}

	if intentions, err := e.store.ListIntentions(); err != nil {
		fail("intentions", err)
	} else {
		available++
		g := group{name: "Intentions"}
		for _, i := range intentions {
			recorded++
			if q != "" && !strings.Contains(strings.ToLower(i.Statement), q) {
				continue
			}
			g.items = append(g.items, fmt.Sprintf("  [%s, %s] %s", i.ID, i.State, i.Statement))
		}
		groups = append(groups, g)
	}

	if experiences, err := e.pagedExperiences(pageFloor); err != nil {
		fail("experiences", err)
	} else {
		available++
		g := group{name: "Experiences"}
		for _, x := range experiences {
			if x.Private == 1 {
				continue // private stays inspectable-but-unsurfaced here (Charter #9)
			}
			recorded++
			if q != "" && !strings.Contains(strings.ToLower(x.Content), q) {
				continue
			}
			// provenance shown: the resident must be able to see WHICH
			// evidence is operator-class (the attestation lens — evidence's
			// authorship is part of the honest read)
			g.items = append(g.items, fmt.Sprintf("  [%s, %s] %s", x.ID, x.Provenance, x.Content))
		}
		groups = append(groups, g)
	}

	// Conversation — the dialogue is a recallable source (R45; and the
	// history-truncation note in the prompt PROMISES this route — a
	// promise recall could not serve until 2026-08-18). Bounded page,
	// newest first, cursor via the same after_seq (turn_seq domain).
	if turns, total, err := e.store.SearchTurns(q, pageFloor, 10); err != nil {
		fail("conversation", err)
	} else {
		available++
		g := group{name: "Conversation"}
		recorded += total
		for _, t := range turns {
			excerpt := recallExcerpt(t.Content, 200)
			g.items = append(g.items, fmt.Sprintf("  [turn %d, %s, %s] %s", t.TurnSeq, t.Role, t.CreatedAt, excerpt))
		}
		groups = append(groups, g)
	}

	// Ledger events — event-level inspection over the store's ledger
	// mirror (R60: ONE read verb, R45's single-route law — inspection is
	// a recall group, never a second tool). Type + payload substring
	// match, newest first, same after_seq cursor (event-seq domain).
	if events, total, err := e.store.SearchLedgerMirror(q, pageFloor, 10); err != nil {
		fail("ledger events", err)
	} else {
		available++
		g := group{name: "Ledger Events"}
		recorded += total
		for _, ev := range events {
			ringLabel := fmt.Sprintf("ring %d", ev.Ring)
			if ev.Ring < 0 {
				ringLabel = "meta"
			}
			g.items = append(g.items, fmt.Sprintf("  [seq %d, %s, %s, %s] %s", ev.Seq, ev.Type, ringLabel, ev.Timestamp, ev.Payload))
		}
		groups = append(groups, g)
	}

	// Ring state — the facility-authored content the prompt renders.
	// R18: budget omissions promise recall(query="ring3") as the route to
	// the omitted content; the promise must be real. Ring content is
	// matched by ring/section name AND content; previews are bounded.
	{
		g := group{name: "Ring State"}
		matchRing := func(label, content string) {
			if content == "" {
				return
			}
			recorded++
			hay := strings.ToLower(label + " " + content)
			if q != "" && !strings.Contains(hay, q) {
				return
			}
			preview := recallExcerpt(content, 240)
			g.items = append(g.items, fmt.Sprintf("  [%s] %s", label, preview))
		}
		if rc := e.rings.GetContent(ring.Ring2); rc != "" {
			matchRing("ring2/self-model", rc)
		}
		for _, sec := range e.rings.Sections(ring.Ring3) {
			matchRing("ring3/"+sec.Name, sec.Content)
		}
		if rc := e.rings.GetContent(ring.Ring3); rc != "" {
			matchRing("ring3", rc)
		}
		for _, sec := range e.rings.Sections(ring.Ring4) {
			matchRing("ring4/"+sec.Name, sec.Content)
		}
		if brief := e.rings.GetBrief(); brief != "" {
			matchRing("brief", brief)
		}
		if len(g.items) > 0 {
			groups = append(groups, g)
		}
	}

	if available == 0 && recorded == 0 && firstFailure != nil {
		return "", fmt.Errorf("recall unavailable from %s: %w", strings.Join(failedSources, ", "), firstFailure)
	}
	unavailable := ""
	if len(failureDetails) > 0 {
		unavailable = "\nUnavailable sources: " + strings.Join(failureDetails, "; ")
	}
	if recorded == 0 {
		return "Nothing to recall yet in available sources." + unavailable, nil
	}

	var out []string
	if q != "" {
		out = append(out, fmt.Sprintf("Recall %q — case-insensitive literal substring across sources:", query))
	} else {
		out = append(out, "Recall — all sources (no query filter):")
	}
	any := false
	for _, g := range groups {
		if len(g.items) == 0 {
			continue
		}
		any = true
		out = append(out, g.name+":")
		out = append(out, g.items...)
	}
	if !any {
		// Abstention with disclosure: content exists, nothing matched
		return fmt.Sprintf("No literal substring match for %q in available recall sources. Retry with one shorter distinctive word or phrase; use separate recall calls for separate concepts. Nothing fabricated.", query) + unavailable, nil
	}
	if unavailable != "" {
		out = append(out, strings.TrimPrefix(unavailable, "\n"))
	}
	out = append(out, "Sources with no matches are omitted. Experiences and syntheses are paged (newest first): pass after_seq=<the lowest seq shown> to read older. Ring state (ring2/ring3/ring4/brief) is searchable by name or content.")
	return strings.Join(out, "\n"), nil
}

func recallExcerpt(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-3]) + "..."
}

// pagedSelfModels / pagedExperiences: cursor pass-through (R1 — the
// source owns the cursor; recall passes it through untouched).
func (e *Engine) pagedSelfModels(afterSeq uint64) ([]store.SelfModelSynthesis, error) {
	if afterSeq >= 1<<62 {
		return e.store.ListSelfModelSyntheses(10, 0)
	}
	return e.store.ListSelfModelSyntheses(10, afterSeq)
}

func (e *Engine) pagedExperiences(afterSeq uint64) ([]store.Experience, error) {
	if afterSeq >= 1<<62 {
		return e.store.ListExperiences(20)
	}
	return e.store.ListExperiencesBefore(20, afterSeq)
}
