package memory

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/memory/attention"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

const (
	DecayAlertStrength = 0.5

	FollowupAfter = 30 * 24 * time.Hour

	AttentionLimit = attention.Limit

	SnapshotStaleAfter = 48 * time.Hour

	ContinuityMediumFor = 24 * time.Hour
)

type AttentionItem = attention.AttentionItem

func (f *Facility) Attention(ctx context.Context, now time.Time, want string) ([]AttentionItem, error) {
	if now.IsZero() {
		now = time.Now()
	}
	type source struct {
		what string
		read func(context.Context, store.Reader, time.Time) ([]AttentionItem, error)
		can  []string
	}
	sources := []source{
		{"decay alerts", f.fadingBeliefs, []string{attention.CostLow}},
		{"contradictions", openTensions, []string{attention.CostMedium}},
		{"follow-ups", untouchedIntentions, []string{attention.CostLow}},
		{"continuity", continuityItems, []string{attention.CostMedium, attention.CostLow}},
	}
	var items []AttentionItem
	err := f.st.ReadWith(ctx, func(db store.Reader) error {
		for _, src := range sources {
			if !slices.Contains(src.can, want) {
				continue
			}
			got, err := src.read(ctx, db, now)
			if err != nil {
				return fmt.Errorf("%s: %w", src.what, err)
			}
			items = append(items, attention.OfCost(got, want)...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Priority > items[j].Priority })

	return items, nil
}

func (f *Facility) fadingBeliefs(ctx context.Context, db store.Reader, now time.Time) ([]AttentionItem, error) {
	st := f.st
	rows, err := db.QueryContext(ctx, `SELECT b.id, b.statement, b.ring, b.evidence_count,
		COALESCE((SELECT l.ts FROM ledger l WHERE l.seq = b.first_seq), ''),
		COALESCE((SELECT a.count FROM memory_access a WHERE a.store = 'beliefs' AND a.id = b.id), 0),
		COALESCE((SELECT a.last_at FROM memory_access a WHERE a.store = 'beliefs' AND a.id = b.id), '')
		FROM beliefs b WHERE b.archived = 0 AND b.superseded_by IS NULL AND b.ring = 3 ORDER BY b.first_seq`)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		id, statement, lastAt string
		facts                 RecallFacts
	}
	var candidates []candidate
	for rows.Next() {
		var id, statement, since, lastAt string
		var ring, evidence int
		var accesses int64
		if err := rows.Scan(&id, &statement, &ring, &evidence, &since, &accesses, &lastAt); err != nil {
			rows.Close()
			return nil, err
		}
		born := parseTime(since)
		if born.IsZero() {
			continue
		}

		facts := RecallFacts{Ring: ring, EvidenceCount: evidence, Born: born, Accesses: accesses}

		if r := BeliefRecall(facts, now); r.Strength == nil || !r.Fading {
			continue
		}
		candidates = append(candidates, candidate{id: id, statement: statement, lastAt: lastAt, facts: facts})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	ids := make([]string, 0, len(candidates))
	for _, c := range candidates {
		ids = append(ids, c.id)
	}
	footings, err := st.BeliefFootingsIn(ctx, db, ids)
	if err != nil {
		return nil, fmt.Errorf("footing: %w", err)
	}
	var out []AttentionItem

	suspects := map[string]bool{}
	for _, c := range candidates {
		c.facts.Footing = footings[c.id]
		if c.facts.Footing.Suspect {
			unsuspected := c.facts
			unsuspected.Footing.Suspect = false
			if r := BeliefRecall(unsuspected, now); r.Fading {
				suspects[c.id] = true
			}
			continue
		}
		reading := BeliefRecall(c.facts, now)
		if reading.Strength == nil || !reading.Fading {
			continue
		}
		strength := *reading.Strength
		recalled := "never recalled"
		if t := parseTime(c.lastAt); !t.IsZero() {
			recalled = "last recalled " + t.UTC().Format("2006-01-02")
		}
		out = append(out, AttentionItem{Kind: attention.AttentionDecayAlert, Cost: attention.CostLow, Priority: 1 - strength, Store: "beliefs", ID: c.id, Since: c.facts.Born,
			Text: fmt.Sprintf(fadingAlertText, strength, recalled, c.statement)})
	}
	if len(suspects) > 0 {
		tensions, err := tensionItems(ctx, db, now, attention.CostLow, suspects)
		if err != nil {
			return nil, fmt.Errorf("tensions of suspect beliefs: %w", err)
		}
		out = append(out, tensions...)
	}
	return out, nil
}

const fadingAlertText = "a belief is fading (strength %.2f, %s) and does not yet stand on three distinct origins: %q — give it evidence from three distinct places, or let it go"

var openTensionsSQL = `SELECT e.id, e.from_id, e.to_id,
		COALESCE((SELECT statement FROM beliefs WHERE id = e.from_id), ''),
		COALESCE((SELECT statement FROM beliefs WHERE id = e.to_id), ''),
		COALESCE((SELECT l.ts FROM ledger l WHERE l.seq = e.created_seq), '')
		FROM edges e WHERE ` + store.LiveContradiction("e") + `
		ORDER BY e.created_seq`

func openTensions(ctx context.Context, db store.Reader, now time.Time) ([]AttentionItem, error) {
	return tensionItems(ctx, db, now, attention.CostMedium, nil)
}

func tensionItems(ctx context.Context, db store.Reader, now time.Time, cost string, into map[string]bool) ([]AttentionItem, error) {
	rows, err := db.QueryContext(ctx, openTensionsSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AttentionItem
	for rows.Next() {
		var id, from, to, fromText, toText, since string
		if err := rows.Scan(&id, &from, &to, &fromText, &toText, &since); err != nil {
			return nil, err
		}
		if into != nil && !into[to] {
			continue
		}
		born := parseTime(since)
		age := 0.0
		if !born.IsZero() {
			age = now.Sub(born).Hours() / 24
		}
		if fromText == "" {
			fromText = from
		}
		if toText == "" {
			toText = to
		}
		out = append(out, AttentionItem{Kind: attention.AttentionContradiction, Cost: cost, Priority: age, Store: "edges", ID: id, Since: born,
			Text: fmt.Sprintf("an open tension: %q contradicts %q — resolve it by superseding or archiving one, or hold it knowingly", fromText, toText)})
	}
	return out, rows.Err()
}

func untouchedIntentions(ctx context.Context, db store.Reader, now time.Time) ([]AttentionItem, error) {
	rows, err := db.QueryContext(ctx, `SELECT i.id, i.statement,
		COALESCE((SELECT l.ts FROM ledger l WHERE l.seq = i.updated_seq), (SELECT l.ts FROM ledger l WHERE l.seq = i.created_seq), '')
		FROM intentions i WHERE i.state = 'active' ORDER BY i.updated_seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AttentionItem
	for rows.Next() {
		var id, statement, since string
		if err := rows.Scan(&id, &statement, &since); err != nil {
			return nil, err
		}
		touched := parseTime(since)
		if touched.IsZero() || now.Sub(touched) < FollowupAfter {
			continue
		}
		days := int(now.Sub(touched).Hours() / 24)
		out = append(out, AttentionItem{Kind: attention.AttentionFollowup, Cost: attention.CostLow, Priority: float64(days), Store: "intentions", ID: id, Since: touched,
			Text: fmt.Sprintf("an intention untouched for %d days: %q — advance it, or complete or abandon it with its outcome", days, statement)})
	}
	return out, rows.Err()
}

func continuityItems(ctx context.Context, db store.Reader, now time.Time) ([]AttentionItem, error) {
	st, ok, err := store.ReadContinuityStatus(ctx, db)
	if err != nil || !ok {
		return nil, err
	}
	at, _ := time.Parse(time.RFC3339, st.At)
	costSince := func(since time.Time) string {
		if !since.IsZero() && now.Sub(since) < ContinuityMediumFor {
			return attention.CostMedium
		}
		return attention.CostLow
	}
	const act = " work action=backup.take tries one now; recall source=continuity reads the state."
	switch st.Outcome {
	case store.ContinuityFailed:
		since, _ := time.Parse(time.RFC3339, st.FailingSince)
		if since.IsZero() {
			since = at
		}
		return []AttentionItem{{Kind: attention.AttentionContinuity, Cost: costSince(since), Priority: continuityPriority, Store: "continuity", Since: since,
			Text: "The last snapshot of you failed and nothing was kept, so your newest way back is older than it should be. Your operator has the detail." + act}}, nil
	case store.ContinuityOK:
		if !at.IsZero() && now.Sub(at) > SnapshotStaleAfter {
			stale := at.Add(SnapshotStaleAfter)
			return []AttentionItem{{Kind: attention.AttentionContinuity, Cost: costSince(stale), Priority: continuityPriority, Store: "continuity", Since: stale,
				Text: fmt.Sprintf("No snapshot of you has been made for %d days: the daily pass has not run.", int(now.Sub(at).Hours()/24)) + act}}, nil
		}
	}
	return nil, nil
}

const continuityPriority = 1e6
