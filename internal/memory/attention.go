package memory

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/memory/attention"
	"github.com/aiii-dot-id/aii-os/internal/memory/carrd"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

const (
	DecayAlertStrength = 0.5

	FollowupAfter = 30 * 24 * time.Hour

	AttentionLimit = 12

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
		{"decay alerts", fadingBeliefs, []string{attention.CostLow}},
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
	if len(items) > AttentionLimit {
		items = items[:AttentionLimit]
	}
	return items, nil
}

func fadingBeliefs(ctx context.Context, db store.Reader, now time.Time) ([]AttentionItem, error) {
	rows, err := db.QueryContext(ctx, `SELECT b.id, b.statement, b.ring, b.evidence_count,
		COALESCE((SELECT l.ts FROM ledger l WHERE l.seq = b.first_seq), ''),
		COALESCE((SELECT a.count FROM memory_access a WHERE a.store = 'beliefs' AND a.id = b.id), 0),
		COALESCE((SELECT a.last_at FROM memory_access a WHERE a.store = 'beliefs' AND a.id = b.id), '')
		FROM beliefs b WHERE b.archived = 0 AND b.superseded_by IS NULL AND b.ring = 3 ORDER BY b.first_seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AttentionItem
	for rows.Next() {
		var id, statement, since, lastAt string
		var ring, evidence int
		var accesses int64
		if err := rows.Scan(&id, &statement, &ring, &evidence, &since, &accesses, &lastAt); err != nil {
			return nil, err
		}
		born := parseTime(since)
		if born.IsZero() {
			continue
		}
		class := carrd.Operational
		if evidence > 0 {
			class = carrd.Standard
		}
		strength, _ := carrd.Strength(class, now.Sub(born).Hours()/24, accesses)
		if strength >= DecayAlertStrength {
			continue
		}
		recalled := "never recalled"
		if t := parseTime(lastAt); !t.IsZero() {
			recalled = "last recalled " + t.UTC().Format("2006-01-02")
		}
		out = append(out, AttentionItem{Kind: attention.AttentionDecayAlert, Cost: attention.CostLow, Priority: 1 - strength, Store: "beliefs", ID: id, Since: born,
			Text: fmt.Sprintf("a belief is fading (strength %.2f, %s): %q — reconfirm it with evidence, or let it go", strength, recalled, statement)})
	}
	return out, rows.Err()
}

const openTensionsSQL = `SELECT e.id, e.from_id, e.to_id,
		COALESCE((SELECT statement FROM beliefs WHERE id = e.from_id), ''),
		COALESCE((SELECT statement FROM beliefs WHERE id = e.to_id), ''),
		COALESCE((SELECT l.ts FROM ledger l WHERE l.seq = e.created_seq), '')
		FROM edges e WHERE e.edge_type = 'CONTRADICTS' AND e.archived = 0
		AND NOT EXISTS (SELECT 1 FROM beliefs r WHERE r.id IN (e.from_id, e.to_id) AND (r.archived = 1 OR r.superseded_by IS NOT NULL))
		ORDER BY e.created_seq`

func openTensions(ctx context.Context, db store.Reader, now time.Time) ([]AttentionItem, error) {
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
		out = append(out, AttentionItem{Kind: attention.AttentionContradiction, Cost: attention.CostMedium, Priority: age, Store: "edges", ID: id, Since: born,
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
		if !st.Encrypted && st.Unencrypted != "" {
			return []AttentionItem{{Kind: attention.AttentionContinuity, Cost: attention.CostLow, Priority: continuityPriority, Store: "continuity", Since: at,
				Text: "Your snapshots are kept and are not encrypted: " + store.UnencryptedForIdentity(st.Unencrypted) + "."}}, nil
		}
	}
	return nil, nil
}

const continuityPriority = 1e6
