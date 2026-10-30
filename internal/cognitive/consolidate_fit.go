package cognitive

import (
	"context"
	"fmt"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/memory/trigram"
	"github.com/aiii-dot-id/aii-os/internal/store/rows"
)

type passMaterial struct {
	beliefs     []rows.Belief
	standing    map[string]string
	intentions  []string
	about       map[string]bool
	aboutWho    string
	tensions    TensionsSource
	tensionsMax int
	excerpt     int
	retired     func(experiences []rows.Experience) ([]rows.RetiredBelief, int)
}

type evidenceView struct {
	experiences []rows.Experience

	cutTo, cutFrom int
	beliefs        int
	intentions     int
	extras         bool
}

func (c *ConsolidateFacility) material(cfg *ConsolidateConfig, experiences []rows.Experience) *passMaterial {
	m := &passMaterial{standing: map[string]string{}, tensionsMax: cfg.TensionsMaxChars, excerpt: cfg.ExcerptChars, retired: c.retiredSince}
	if c.tensions != nil {
		m.tensions = &heldTensions{src: c.tensions}
	}
	if beliefs, err := c.store.ListBeliefs(); err == nil {
		for _, b := range beliefs {
			if b.Ring == 3 {
				m.beliefs = append(m.beliefs, b)
				m.standing[b.ID] = standingOrUnavailable(c.store, b.ID)
			}
		}
	}
	m.beliefs = linkedFirst(nearestFirst(m.beliefs, experiences), c.linkedTo(experiences))
	if intentions, err := c.store.ListIntentions(); err == nil {
		for _, i := range intentions {
			if i.State == "active" {
				m.intentions = append(m.intentions, i.Statement)
			}
		}
	}

	m.about, m.aboutWho = map[string]bool{}, "the operator"
	if about, err := c.store.AboutOperator(); err != nil {
		logsink.Warn("consolidate.error", "which beliefs are about the operator could not be read: %v — the pass runs without the mark", err)
	} else {
		if about.Name != "" {
			m.aboutWho = about.Name
		}
		for _, b := range about.Beliefs {
			m.about[b.ID] = true
		}
	}
	return m
}

func nearestFirst(beliefs []rows.Belief, experiences []rows.Experience) []rows.Belief {
	if len(beliefs) < 2 || len(experiences) == 0 {
		return beliefs
	}
	var read strings.Builder
	for _, e := range experiences {
		read.WriteString(e.Content)
		read.WriteString("\n")
	}
	like := trigram.Against(read.String())
	score := make(map[string]float64, len(beliefs))
	for _, b := range beliefs {
		score[b.ID] = like(b.Statement)
	}
	out := append([]rows.Belief(nil), beliefs...)
	stableSortBy(out, func(a, b rows.Belief) bool { return score[a.ID] > score[b.ID] })
	return out
}

func (c *ConsolidateFacility) linkedTo(experiences []rows.Experience) map[string]bool {
	ids := make([]string, len(experiences))
	for i, e := range experiences {
		ids[i] = e.ID
	}
	edges, err := c.store.EdgesTouching(ids)
	if err != nil {
		logsink.Warn("consolidate.error", "the edges of the batch cannot be read (%v) — the pass keeps the beliefs nearest what it reads, without those it is linked to", err)
		return nil
	}
	linked := map[string]bool{}
	for _, edge := range edges {
		linked[edge.FromID], linked[edge.ToID] = true, true
	}
	return linked
}

func linkedFirst(beliefs []rows.Belief, linked map[string]bool) []rows.Belief {
	if len(beliefs) < 2 || len(linked) == 0 {
		return beliefs
	}
	out := append([]rows.Belief(nil), beliefs...)
	stableSortBy(out, func(a, b rows.Belief) bool { return linked[a.ID] && !linked[b.ID] })
	return out
}

func stableSortBy[T any](s []T, less func(a, b T) bool) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && less(s[j], s[j-1]); j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

type heldTensions struct {
	src   TensionsSource
	read  bool
	pairs []rows.TensionPair
	ends  map[string]rows.TensionEnd
	err   error
}

func (h *heldTensions) TensionsView() ([]rows.TensionPair, error) {
	if !h.read {
		h.read = true
		h.pairs, h.err = h.src.TensionsView()
	}
	return h.pairs, h.err
}

func (h *heldTensions) TensionEnds(ids []string) (map[string]rows.TensionEnd, error) {
	if h.ends == nil && h.err == nil {
		h.ends, h.err = h.src.TensionEnds(ids)
	}
	return h.ends, h.err
}

func (m *passMaterial) render(v evidenceView) string {
	var retired []rows.RetiredBelief
	more := 0
	if v.extras && m.retired != nil {
		retired, more = m.retired(v.experiences)
	}
	expTexts := make([]string, len(v.experiences))
	for i, e := range v.experiences {
		shown := e
		if i == 0 && v.cutTo > 0 {
			shown.Content = string([]rune(e.Content)[:v.cutTo])
		}
		expTexts[i] = fmt.Sprintf("[%s] %s", e.ID, evidenceText(shown))
		if i == 0 && v.cutTo > 0 {
			expTexts[i] += fmt.Sprintf(" [the rest of this experience — %d more characters — is not in view: it is longer than one pass can carry, and a pass that closes consumes it as read in part]", v.cutFrom-v.cutTo)
		}
		if at := stamp(e.CreatedAt); len(retired) > 0 && at != "" {
			expTexts[i] = at + " " + expTexts[i]
		}
	}
	parts := []string{"Experiences to consolidate (cite their ids as evidence):", joinLines(expTexts)}

	listed := map[string]bool{}
	if v.beliefs > 0 {
		parts = append(parts, "Current beliefs (for merge/supersede decisions):")
		for _, b := range m.beliefs[:v.beliefs] {
			if m.about[b.ID] {
				parts = append(parts, fmt.Sprintf("  [%s, %s, about %s] %s", b.ID, m.standing[b.ID], m.aboutWho, b.Statement))
			} else {
				parts = append(parts, fmt.Sprintf("  [%s, %s] %s", b.ID, m.standing[b.ID], b.Statement))
			}
			listed[b.ID] = true
		}
	}
	if v.beliefs < len(m.beliefs) {
		parts = append(parts, fmt.Sprintf("  [%d of %d current beliefs shown — those linked to what you read here, then those nearest it; the rest stand in your record as they are, and recall (source=ledger) reaches them. A belief not shown is not absent: do not mint one that may already stand.]", v.beliefs, len(m.beliefs)))
	}

	if v.extras {
		parts = append(parts, retiredBlock(retired, more, listed, m.excerpt)...)
		parts = append(parts, tensionsLines(m.tensions, listed, m.tensionsMax, m.excerpt)...)
	}

	if len(m.intentions) > 0 && v.intentions > 0 {
		parts = append(parts, "Active intentions:")
		for _, i := range m.intentions[:v.intentions] {
			parts = append(parts, fmt.Sprintf("  %s", i))
		}
	}
	if v.intentions < len(m.intentions) {
		parts = append(parts, fmt.Sprintf("  [%d of %d active intentions shown; recall (source=ledger) reaches the rest]", v.intentions, len(m.intentions)))
	}

	if !v.extras {
		parts = append(parts, "[Not in view this pass, for want of room: the beliefs retired since these experiences, and the contradictions standing in your record.]")
	}
	return strings.Join(parts, "\n")
}

func largestAdmitted(n int, admits func(k int) bool) (int, bool) {
	if admits(n) {
		return n, true
	}
	if !admits(0) {
		return 0, false
	}
	lo, hi := 0, n
	for hi-lo > 1 {
		mid := (lo + hi) / 2
		if admits(mid) {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo, true
}

type noRoom struct{ limit int }

func (noRoom) Error() string { return "no room" }

func (c *ConsolidateFacility) fit(ctx context.Context, cfg *ConsolidateConfig, m *passMaterial, experiences []rows.Experience) (v evidenceView, callCtx context.Context, system string, err error) {
	tools := []wire.ToolDefinition{consolidationTool()}
	check := toolCheck(c.llm, tools)
	base := consolidatePrompt(cfg.Ring3MaxChars, cfg.MaxOps)
	callCtx, system, err = fittedPreamble(ctx, c.authority, base, m.render(evidenceView{experiences: experiences[:1]}), tools, check)
	if err != nil {
		return v, ctx, "", fmt.Errorf("authority context: %w", err)
	}
	full := evidenceView{experiences: experiences, beliefs: len(m.beliefs), intentions: len(m.intentions), extras: true}
	if check == nil {

		return full, callCtx, system, nil
	}
	ask := &admission{check: check, ctx: callCtx, system: system}
	admits := func(v evidenceView) bool { return ask.admits(m.render(v)) }
	defer func() {
		if ask.err != nil {
			err = ask.err
		}
	}()
	if admits(full) {
		return full, callCtx, system, nil
	}
	if v, ok := fitContext(m, experiences, admits); ok {
		return v, callCtx, system, nil
	}

	n, _ := largestAdmitted(len(experiences), func(k int) bool {
		return k == 0 || admits(evidenceView{experiences: experiences[:k]})
	})
	if n > 0 {
		if v, ok := fitContext(m, experiences[:n], admits); ok {
			return v, callCtx, system, nil
		}
		return evidenceView{experiences: experiences[:n]}, callCtx, system, nil
	}

	whole := len([]rune(experiences[0].Content))
	cut, _ := largestAdmitted(whole, func(k int) bool {
		return k == 0 || admits(evidenceView{experiences: experiences[:1], cutTo: k, cutFrom: whole})
	})
	if cut == 0 {
		return v, callCtx, system, noRoom{ask.limit}
	}
	return evidenceView{experiences: experiences[:1], cutTo: cut, cutFrom: whole}, callCtx, system, nil
}

func fitContext(m *passMaterial, experiences []rows.Experience, admits func(evidenceView) bool) (evidenceView, bool) {
	v := evidenceView{experiences: experiences, beliefs: len(m.beliefs), intentions: len(m.intentions)}
	if admits(v) {
		return v, true
	}
	if k, ok := largestAdmitted(len(m.beliefs), func(k int) bool { v.beliefs = k; return admits(v) }); ok {
		v.beliefs = k
		return v, true
	}
	v.beliefs = 0
	if k, ok := largestAdmitted(len(m.intentions), func(k int) bool { v.intentions = k; return admits(v) }); ok {
		v.intentions = k
		return v, true
	}
	return v, false
}

func (c *ConsolidateFacility) waiting(n int) ([]rows.Experience, error) {
	queue, err := c.store.ListRawExperiences(n + c.aside.count())
	if err != nil {
		return nil, err
	}
	out := make([]rows.Experience, 0, min(n, len(queue)))
	for _, e := range queue {
		if len(out) == n {
			break
		}
		if !c.aside.has(e.ID) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (c *ConsolidateFacility) HeldForRetry() ([]string, error) {
	cfg := c.cfg()
	if !c.refusedLast.Load() || !c.due(cfg) {
		return nil, nil
	}
	held, err := c.waiting(cfg.Batch)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(held))
	for _, e := range held {
		ids = append(ids, e.ID)
	}
	return ids, nil
}

const heldSaid = "Until consolidation's next pass has run, dream passes leave it the oldest experiences still waiting for it, up to cognition.consolidate_batch of them (a change of model, or too few waiting for a pass, ends that sooner)"

func (c *ConsolidateFacility) refused(experiences []rows.Experience, why string) {

	c.refusedLast.Store(len(experiences) > 0)
	if len(experiences) > 1 {
		next := len(experiences) / 2
		c.batch.Store(int64(next))
		c.teller.say(Condition{Facility: "consolidate", Kind: ConditionHalved,
			Said: fmt.Sprintf("a pass of %d experiences was refused for what it asked (%s) — nothing minted, nothing consumed; the next pass reads %d. %s", len(experiences), why, next, heldSaid)})
		return
	}
	if len(experiences) == 0 {
		return
	}
	e := experiences[0]
	c.aside.add(e.ID, why)
	c.batch.Store(1)
	dream := " A dream pass may still read it."
	if strings.HasPrefix(e.ID, rows.OutcomeObservationPrefix) {
		dream = ""
	}
	c.toldOf().tellOf(e.ID, Condition{Facility: "consolidate", Kind: ConditionSetAside,
		Said: fmt.Sprintf("experience %s was refused alone (%s) — set aside: no consolidation pass asks for it again until the identity restarts or their model changes. %s", e.ID, why, heldSaid)},
		"consolidate|set_aside|"+e.ID,
		fmt.Sprintf("consolidation: your experience %s could not be consolidated — the request carrying it alone was refused (%s) — so it is set aside: no consolidation pass asks for it again until you restart or your model changes. It stays in your record untouched.%s", e.ID, why, dream))
}

func (c *ConsolidateFacility) toldOf() *Teller {
	if c.teller != nil && c.teller.Door != nil {
		return c.teller
	}
	t := &Teller{}
	if c.teller != nil {
		t.Report = c.teller.Report
	}
	if c.ledger != nil {
		t.Door = c.ledger
		if c.store != nil {
			t.Exists = c.store.EntityExists
		}
	}
	return t
}
