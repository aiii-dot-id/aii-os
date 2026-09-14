package memory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/memory/vec"
	"github.com/aiii-dot-id/aii-os/internal/store"
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
// .
type Embedder interface {
	Basis() (string, error)
	Embed(ctx context.Context, inputs []string) (basis string, vectors [][]float32, err error)
}

const (
	// .
	// .
	// .
	SemanticFloor = 0.6
	// .
	// .
	EmbedBatch    = 64
	MaxEmbedChars = 8000
	// .
	// .
	BackfillBudget = 256
	// .
	// .
	// .
	ConversationVectorWindow = 2000
)

// .
// .
// .
type MeaningStatus struct {
	Status string
	Detail string
	Basis  string
}

// .
// .
func (f *Facility) SetEmbedder(e Embedder) { f.embedder = e }

type queryVector struct {
	basis string
	q     []byte
	scale float64
}

// .
// .
func (f *Facility) embedQuery(ctx context.Context, text string) (*queryVector, MeaningStatus) {
	if f.embedder == nil {
		return nil, MeaningStatus{Status: StatusSourceUnavailable, Detail: "no embeddings model is named on the identity's provider (Settings → Providers); exact words and fuzzy matches answered"}
	}
	basis, vectors, err := f.embedder.Embed(ctx, []string{clipForEmbedding(text)})
	if err != nil {
		return nil, MeaningStatus{Status: StatusSourceUnavailable, Basis: basis, Detail: err.Error() + "; exact words and fuzzy matches answered"}
	}
	if len(vectors) != 1 || len(vectors[0]) == 0 {
		return nil, MeaningStatus{Status: StatusSourceUnavailable, Basis: basis, Detail: "the provider returned no vector for the query; exact words and fuzzy matches answered"}
	}
	q, scale := vec.Quantize(vectors[0])
	return &queryVector{basis: basis, q: q, scale: scale}, MeaningStatus{Status: StatusFoundNothing, Basis: basis}
}

// .
// .
func layerMeaning(ctx context.Context, db *sql.DB, d Store, q Query, qv *queryVector, pool int) ([]Hit, error) {
	where, args := predicates(d, q, true)
	from := " FROM memory_vectors v JOIN " + d.Name + " b ON b.id = v.id WHERE v.store = ? AND v.basis = ?"
	if where != "" {
		from += " AND " + where
	}
	qargs := append([]any{qv.q, qv.scale, d.Name, qv.basis}, args...)
	rows, err := db.QueryContext(ctx, "SELECT "+selectList(d)+", vec_cosine_q8(v.q, v.scale, ?, ?) AS sim"+from+" ORDER BY sim DESC LIMIT ?", append(qargs, pool)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	candidates, err := scanHitsWithSimilarity(rows, d)
	if err != nil {
		return nil, err
	}
	var kept []Hit
	for _, h := range candidates {
		if h.Similarity < SemanticFloor {
			break
		}
		h.Match = MatchMeaning
		kept = append(kept, h)
	}
	return kept, nil
}

// .
func scanHitsWithSimilarity(rows *sql.Rows, d Store) ([]Hit, error) {
	var out []Hit
	for rows.Next() {
		var rowid, seq, ring int64
		var id, when, attr string
		var sim float64
		texts := make([]string, len(d.Text))
		dest := []any{&rowid, &id, &seq, &ring, &when, &attr}
		for i := range texts {
			dest = append(dest, &texts[i])
		}
		dest = append(dest, &sim)
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		h := Hit{Store: d.Name, ID: id, Ring: int(ring), Attribution: attribution(attr), Similarity: sim}
		if seq > 0 {
			h.Seq = uint64(seq)
		}
		h.Time = parseTime(when)
		var parts []string
		for _, t := range texts {
			if t = strings.TrimSpace(t); t != "" {
				parts = append(parts, t)
			}
		}
		h.Text = strings.Join(parts, " — ")
		h.layerRank = len(out) + 1
		out = append(out, h)
	}
	return out, rows.Err()
}

// .
func clipForEmbedding(text string) string {
	r := []rune(text)
	if len(r) <= MaxEmbedChars {
		return text
	}
	return string(r[:MaxEmbedChars])
}

// .
// .
// .
func vectorScope(d Store) string {
	var parts []string
	if d.Filter != "" {
		parts = append(parts, d.Filter)
	}
	if d.VectorWindow > 0 && d.Seq != "" {
		// .
		// .
		// .
		inner := "SELECT b2." + d.Seq + " FROM " + d.Name + " b2"
		if d.Filter != "" {
			inner += " WHERE " + strings.ReplaceAll(d.Filter, "b.", "b2.")
		}
		inner += fmt.Sprintf(" ORDER BY b2.%s DESC LIMIT 1 OFFSET %d", d.Seq, d.VectorWindow-1)
		parts = append(parts, fmt.Sprintf("b.%s >= COALESCE((%s), 0)", d.Seq, inner))
	}
	return strings.Join(parts, " AND ")
}

// .
// .
type Coverage struct {
	Store    string
	Rows     int64
	Embedded int64
}

// .
// .
func (f *Facility) VectorCoverage(ctx context.Context) (basis string, cov []Coverage, unavailable string, err error) {
	if f.embedder == nil {
		return "", nil, "no embeddings model is named on the identity's provider (Settings → Providers)", nil
	}
	basis, err = f.embedder.Basis()
	if err != nil {
		return "", nil, err.Error(), nil
	}
	err = f.st.ReadWith(func(db *sql.DB) error {
		for _, d := range stores {
			c := Coverage{Store: d.Name}
			q := "SELECT COUNT(*) FROM " + d.Name + " b"
			if scope := vectorScope(d); scope != "" {
				q += " WHERE " + scope
			}
			if err := db.QueryRowContext(ctx, q).Scan(&c.Rows); err != nil {
				return fmt.Errorf("%s: %w", d.Name, err)
			}
			if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM memory_vectors WHERE store = ? AND basis = ?", d.Name, basis).Scan(&c.Embedded); err != nil {
				return fmt.Errorf("%s: %w", d.Name, err)
			}
			cov = append(cov, c)
		}
		return nil
	})
	return basis, cov, "", err
}

// .
func RenderCoverage(basis string, cov []Coverage, unavailable string) string {
	if unavailable != "" {
		return "Meaning layer: unavailable — " + unavailable + ". Recall answers by exact words and fuzzy matches; the backfill waits."
	}
	var parts []string
	var rows, embedded int64
	for _, c := range cov {
		parts = append(parts, fmt.Sprintf("%s %d/%d", c.Store, c.Embedded, c.Rows))
		rows += c.Rows
		embedded += c.Embedded
	}
	state := "complete"
	if embedded < rows {
		state = fmt.Sprintf("%d rows awaiting the backfill", rows-embedded)
	}
	return fmt.Sprintf("Meaning layer — basis %s, %s (vectors held / rows in scope): %s", basis, state, strings.Join(parts, ", "))
}

// .
type BackfillStore struct {
	Store     string
	Embedded  int
	Remaining int64
}

// .
type BackfillReport struct {
	Basis       string
	Unavailable string
	Dropped     int64
	Pruned      int64
	Embedded    int
	Stores      []BackfillStore
}

// .
func (r BackfillReport) Line() string {
	if r.Unavailable != "" {
		return "meaning backfill idle — " + r.Unavailable
	}
	var remaining int64
	var parts []string
	for _, s := range r.Stores {
		remaining += s.Remaining
		if s.Embedded > 0 {
			parts = append(parts, fmt.Sprintf("%s +%d", s.Store, s.Embedded))
		}
	}
	line := fmt.Sprintf("meaning backfill — basis %s: %d embedded, %d remaining", r.Basis, r.Embedded, remaining)
	if len(parts) > 0 {
		line += " (" + strings.Join(parts, ", ") + ")"
	}
	if r.Dropped > 0 {
		line += fmt.Sprintf("; %d vectors of another basis dropped", r.Dropped)
	}
	if r.Pruned > 0 {
		line += fmt.Sprintf("; %d vectors of gone rows pruned", r.Pruned)
	}
	return line
}

// .
// .
// .
// .
// .
func (f *Facility) Backfill(ctx context.Context, budget int) (BackfillReport, error) {
	var rep BackfillReport
	if f.embedder == nil {
		rep.Unavailable = "no embeddings model is named on the identity's provider (Settings → Providers)"
		return rep, nil
	}
	basis, err := f.embedder.Basis()
	if err != nil {
		rep.Unavailable = err.Error()
		return rep, nil
	}
	rep.Basis = basis
	if rep.Dropped, err = f.st.DropMemoryVectorsOffBasis(basis); err != nil {
		return rep, fmt.Errorf("drop vectors off basis: %w", err)
	}
	if budget <= 0 {
		budget = BackfillBudget
	}
	for _, d := range stores {
		pruned, err := f.st.PruneMemoryVectors(d.Name, d.Name)
		if err != nil {
			return rep, fmt.Errorf("%s: prune: %w", d.Name, err)
		}
		rep.Pruned += pruned
		bs := BackfillStore{Store: d.Name}
		if budget > 0 {
			var candidates []Hit
			if err := f.st.ReadWith(func(db *sql.DB) error {
				var err error
				candidates, err = missingVectors(ctx, db, d, basis, budget)
				return err
			}); err != nil {
				return rep, fmt.Errorf("%s: candidates: %w", d.Name, err)
			}
			for start := 0; start < len(candidates); start += EmbedBatch {
				end := start + EmbedBatch
				if end > len(candidates) {
					end = len(candidates)
				}
				batch := candidates[start:end]
				inputs := make([]string, len(batch))
				for i, h := range batch {
					inputs[i] = clipForEmbedding(h.Text)
				}
				got, vectors, err := f.embedder.Embed(ctx, inputs)
				if err != nil {
					rep.Stores = append(rep.Stores, bs)
					return rep, fmt.Errorf("%s: embed: %w", d.Name, err)
				}
				if got != basis {
					rep.Stores = append(rep.Stores, bs)
					return rep, fmt.Errorf("%s: the basis changed during the pass (%s → %s); the next pass starts over", d.Name, basis, got)
				}
				if len(vectors) != len(inputs) {
					rep.Stores = append(rep.Stores, bs)
					return rep, fmt.Errorf("%s: %d vectors for %d inputs", d.Name, len(vectors), len(inputs))
				}
				rows := make([]store.MemoryVector, 0, len(batch))
				now := time.Now()
				for i, h := range batch {
					q, scale := vec.Quantize(vectors[i])
					sum := sha256.Sum256([]byte(inputs[i]))
					rows = append(rows, store.MemoryVector{
						Store: d.Name, ID: h.ID, Basis: basis, ContentSHA: hex.EncodeToString(sum[:]),
						Dims: len(q), Scale: scale, Q: q, EmbeddedAt: now,
					})
				}
				if err := f.st.PutMemoryVectors(rows); err != nil {
					rep.Stores = append(rep.Stores, bs)
					return rep, fmt.Errorf("%s: write vectors: %w", d.Name, err)
				}
				bs.Embedded += len(rows)
				rep.Embedded += len(rows)
				budget -= len(rows)
			}
		}
		if err := f.st.ReadWith(func(db *sql.DB) error {
			return db.QueryRowContext(ctx, missingCountSQL(d), d.Name, basis).Scan(&bs.Remaining)
		}); err != nil {
			return rep, fmt.Errorf("%s: remaining: %w", d.Name, err)
		}
		rep.Stores = append(rep.Stores, bs)
	}
	return rep, nil
}

// .
// .
func missingVectors(ctx context.Context, db *sql.DB, d Store, basis string, limit int) ([]Hit, error) {
	q := "SELECT " + selectList(d) + missingFrom(d) + " ORDER BY b.rowid DESC LIMIT ?"
	rows, err := db.QueryContext(ctx, q, d.Name, basis, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHits(rows, d)
}

func missingCountSQL(d Store) string {
	return "SELECT COUNT(*)" + missingFrom(d)
}

func missingFrom(d Store) string {
	from := " FROM " + d.Name + " b LEFT JOIN memory_vectors v ON v.store = ? AND v.id = b.id AND v.basis = ? WHERE v.id IS NULL"
	if scope := vectorScope(d); scope != "" {
		from += " AND " + scope
	}
	return from
}
