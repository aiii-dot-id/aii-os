package memory

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/memory/carrd"
)

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
const (
	AttentionDecayAlert    = "decay_alert"
	AttentionContradiction = "contradiction"
	AttentionFollowup      = "followup"
	AttentionConsolidation = "consolidation"
)

// .
const (
	CostSilent = "silent"
	CostLow    = "low"
	CostMedium = "medium"
	CostHigh   = "high"
)

const (
	// .
	// .
	// .
	DecayAlertStrength = 0.5
	// .
	// .
	FollowupAfter = 30 * 24 * time.Hour
	// .
	// .
	ConsolidationBacklog = 3
	// .
	AttentionLimit = 12
)

// .
type AttentionItem struct {
	Kind     string
	Cost     string
	Priority float64
	Store    string
	ID       string
	Text     string
	Since    time.Time
}

// .
func (f *Facility) Attention(ctx context.Context, now time.Time) ([]AttentionItem, error) {
	if now.IsZero() {
		now = time.Now()
	}
	var items []AttentionItem
	err := f.st.ReadWith(func(db *sql.DB) error {
		fading, err := fadingBeliefs(ctx, db, now)
		if err != nil {
			return fmt.Errorf("decay alerts: %w", err)
		}
		items = append(items, fading...)
		tensions, err := openTensions(ctx, db, now)
		if err != nil {
			return fmt.Errorf("contradictions: %w", err)
		}
		items = append(items, tensions...)
		followups, err := untouchedIntentions(ctx, db, now)
		if err != nil {
			return fmt.Errorf("follow-ups: %w", err)
		}
		items = append(items, followups...)
		var raw int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM experiences WHERE raw = 1 AND private = 0`).Scan(&raw); err != nil {
			return fmt.Errorf("consolidation: %w", err)
		}
		if raw >= ConsolidationBacklog {
			items = append(items, AttentionItem{Kind: AttentionConsolidation, Cost: CostSilent, Priority: float64(raw),
				Text: fmt.Sprintf("%d experiences await the unconscious (consolidation runs capacity-gated)", raw)})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(items, func(i, j int) bool {
		if ci, cj := costRank(items[i].Cost), costRank(items[j].Cost); ci != cj {
			return ci > cj
		}
		return items[i].Priority > items[j].Priority
	})
	if len(items) > AttentionLimit {
		items = items[:AttentionLimit]
	}
	return items, nil
}

func costRank(c string) int {
	switch c {
	case CostHigh:
		return 3
	case CostMedium:
		return 2
	case CostLow:
		return 1
	}
	return 0
}

// .
func AtMost(items []AttentionItem, cost string) []AttentionItem {
	var out []AttentionItem
	for _, it := range items {
		if costRank(it.Cost) <= costRank(cost) {
			out = append(out, it)
		}
	}
	return out
}

// .
func OfCost(items []AttentionItem, cost string) []AttentionItem {
	var out []AttentionItem
	for _, it := range items {
		if it.Cost == cost {
			out = append(out, it)
		}
	}
	return out
}

// .
// .
func RenderAttention(items []AttentionItem) string {
	var lines []string
	for _, it := range items {
		lines = append(lines, "- "+it.Text)
	}
	return strings.Join(lines, "\n")
}

// .
// .
func fadingBeliefs(ctx context.Context, db *sql.DB, now time.Time) ([]AttentionItem, error) {
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
		out = append(out, AttentionItem{Kind: AttentionDecayAlert, Cost: CostLow, Priority: 1 - strength, Store: "beliefs", ID: id, Since: born,
			Text: fmt.Sprintf("a belief is fading (strength %.2f, %s): %q — reconfirm it with evidence, or let it go", strength, recalled, statement)})
	}
	return out, rows.Err()
}

// .
// .
func openTensions(ctx context.Context, db *sql.DB, now time.Time) ([]AttentionItem, error) {
	rows, err := db.QueryContext(ctx, `SELECT e.id, e.from_id, e.to_id,
		COALESCE((SELECT statement FROM beliefs WHERE id = e.from_id), ''),
		COALESCE((SELECT statement FROM beliefs WHERE id = e.to_id), ''),
		COALESCE((SELECT l.ts FROM ledger l WHERE l.seq = e.created_seq), '')
		FROM edges e WHERE e.edge_type = 'CONTRADICTS' AND e.archived = 0 ORDER BY e.created_seq`)
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
		out = append(out, AttentionItem{Kind: AttentionContradiction, Cost: CostMedium, Priority: age, Store: "edges", ID: id, Since: born,
			Text: fmt.Sprintf("an open tension: %q contradicts %q — resolve it by superseding or archiving one, or hold it knowingly", fromText, toText)})
	}
	return out, rows.Err()
}

// .
// .
func untouchedIntentions(ctx context.Context, db *sql.DB, now time.Time) ([]AttentionItem, error) {
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
		out = append(out, AttentionItem{Kind: AttentionFollowup, Cost: CostLow, Priority: float64(days), Store: "intentions", ID: id, Since: touched,
			Text: fmt.Sprintf("an intention untouched for %d days: %q — advance it, or complete or abandon it with its outcome", days, statement)})
	}
	return out, rows.Err()
}
