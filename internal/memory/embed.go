package memory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/memory/vec"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

type Embedder interface {
	Basis() (string, error)
	Floor() float64

	Embed(ctx context.Context, kind Kind, inputs []string) (basis string, vectors [][]float32, err error)
}

type Kind string

const (
	KindMemory Kind = "memory"

	KindCue Kind = "cue"
)

type Chooser interface {
	Choose() Embedder
}

type Batcher interface {
	Batch() int
}

type Yielder interface {
	MayContinue() bool
}

type Worker interface {
	PassWork() time.Duration
}

const (
	ProviderFloor = 0.6

	EmbedBatch    = 64
	MaxEmbedChars = 8000

	backfillRound = 256

	BackfillBudget = 256

	ConversationVectorWindow = 2000
)

type MeaningStatus struct {
	Status string
	Detail string
	Basis  string

	Floor float64
}

const NoSource = "no source of vectors is set up (the dashboard's Memory card: a plugin that makes them on this machine, or a provider's embeddings model)"

func (f *Facility) SetEmbedder(e Embedder) { f.embedder = e }

func (f *Facility) source() Embedder {
	if c, ok := f.embedder.(Chooser); ok {
		return c.Choose()
	}
	return f.embedder
}

type queryVector struct {
	basis string
	q     []byte
	scale float64
	floor float64
}

type lengthSeen struct {
	mu    sync.Mutex
	basis string
	dims  int
}

func (l *lengthSeen) note(basis string, dims int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.basis, l.dims = basis, dims
}

func (l *lengthSeen) of(basis string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.basis != basis {
		return 0
	}
	return l.dims
}

func (f *Facility) embedQuery(ctx context.Context, text string) (*queryVector, MeaningStatus) {
	source := f.source()
	if source == nil {
		return nil, MeaningStatus{Status: StatusSourceUnavailable, Detail: NoSource + "; exact words and fuzzy matches answered"}
	}
	basis, vectors, err := source.Embed(ctx, KindCue, []string{clipForEmbedding(text)})
	if err != nil {
		return nil, MeaningStatus{Status: StatusSourceUnavailable, Basis: basis, Detail: err.Error() + "; exact words and fuzzy matches answered"}
	}
	if len(vectors) != 1 || len(vectors[0]) == 0 {
		return nil, MeaningStatus{Status: StatusSourceUnavailable, Basis: basis, Detail: "the source returned no vector for the query; exact words and fuzzy matches answered"}
	}

	floor := source.Floor()
	if !(floor >= 0 && floor <= 1) {
		return nil, MeaningStatus{Status: StatusSourceUnavailable, Basis: basis, Detail: fmt.Sprintf("the source of vectors declares no usable floor (%v); exact words and fuzzy matches answered", floor)}
	}
	f.length.note(basis, len(vectors[0]))
	q, scale := vec.Quantize(vectors[0])
	return &queryVector{basis: basis, q: q, scale: scale, floor: floor}, MeaningStatus{Status: StatusFoundNothing, Basis: basis, Floor: floor}
}

const staleSlack = 4

const MeaningReadBound = 1024

func layerMeaning(ctx context.Context, db store.Reader, d Store, q Query, qv *queryVector, pool int) (kept []Hit, passed int, err error) {
	where, args := predicates(d, q, true)
	from := " FROM memory_vectors v JOIN " + d.Name + " b ON b.id = v.id WHERE v.store = ? AND v.basis = ? AND v.dims = ?"
	if where != "" {
		from += " AND " + where
	}
	query := "SELECT " + selectList(d) + ", vec_cosine_q8(v.q, v.scale, ?, ?) AS sim, v.content_sha" + from + " ORDER BY sim DESC LIMIT ?"
	qargs := append([]any{qv.q, qv.scale, d.Name, qv.basis, len(qv.q)}, args...)
	for read := min(pool*staleSlack, MeaningReadBound); ; read = min(read*staleSlack, MeaningReadBound) {
		candidates, err := meaningCandidates(ctx, db, d, query, append(qargs, read))
		if err != nil {
			return nil, 0, err
		}
		kept, passed = nil, 0
		spent := len(candidates) < read
		for _, h := range candidates {
			if h.Similarity < qv.floor || len(kept) == pool {
				spent = true
				break
			}

			if h.vectorOf != textSHA(h.Text) {
				passed++
				continue
			}
			h.Match = MatchMeaning
			kept = append(kept, h)
		}
		switch {
		case spent || len(kept) == pool:
			return kept, 0, nil
		case read >= MeaningReadBound:
			return kept, passed, nil
		}
	}
}

func meaningCandidates(ctx context.Context, db store.Reader, d Store, query string, args []any) ([]Hit, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHitsWithSimilarity(rows, d)
}

func textSHA(text string) string {
	sum := sha256.Sum256([]byte(clipForEmbedding(text)))
	return hex.EncodeToString(sum[:])
}

func scanHitsWithSimilarity(rows *sql.Rows, d Store) ([]Hit, error) {
	var out []Hit
	for rows.Next() {
		var rowid, seq, ring int64
		var id, when, attr, vectorOf string
		var sim float64
		texts := make([]string, len(d.Text))
		dest := []any{&rowid, &id, &seq, &ring, &when, &attr}
		for i := range texts {
			dest = append(dest, &texts[i])
		}
		dest = append(dest, &sim, &vectorOf)
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		h := Hit{Store: d.Name, ID: id, Ring: int(ring), Attribution: attribution(attr), Similarity: sim, vectorOf: vectorOf}
		if seq > 0 {
			h.Seq = uint64(seq)
		}
		h.Time = parseTime(when)
		h.Text = joinText(texts)
		h.layerRank = len(out) + 1
		out = append(out, h)
	}
	return out, rows.Err()
}

func clipForEmbedding(text string) string {
	r := []rune(text)
	if len(r) <= MaxEmbedChars {
		return text
	}
	return string(r[:MaxEmbedChars])
}

func vectorScope(d Store, window int) string {
	var parts []string
	if d.Filter != "" {
		parts = append(parts, d.Filter)
	}
	if window > 0 && d.Seq != "" {

		inner := "SELECT b2." + d.Seq + " FROM " + d.Name + " b2"
		if d.Filter != "" {
			inner += " WHERE " + strings.ReplaceAll(d.Filter, "b.", "b2.")
		}
		inner += fmt.Sprintf(" ORDER BY b2.%s DESC LIMIT 1 OFFSET %d", d.Seq, window-1)
		parts = append(parts, fmt.Sprintf("b.%s >= COALESCE((%s), 0)", d.Seq, inner))
	}
	return strings.Join(parts, " AND ")
}

func (f *Facility) SetConversationWindow(turns int) { f.convWindow = turns }

func (f *Facility) windowFor(d Store, source Embedder) int {
	if d.VectorWindow <= 0 {
		return 0
	}
	if w, ok := source.(Worker); ok && w.PassWork() > 0 {
		return 0
	}
	if f.convWindow > 0 {
		return f.convWindow
	}
	return d.VectorWindow
}

type Coverage struct {
	Store string

	Rows     int64
	Embedded int64

	Beyond         int64
	BeyondEmbedded int64
}

func (f *Facility) HeldCoverage(ctx context.Context) (basis string, cov []Coverage, unavailable string, err error) {
	return f.coverage(ctx, vectorsHeld)
}

func (f *Facility) UsableCoverage(ctx context.Context) (basis string, cov []Coverage, unavailable string, err error) {
	return f.coverage(ctx, vectorsUsable)
}

func (f *Facility) coverage(ctx context.Context, vectors func(ctx context.Context, db store.Reader, d Store, basis, where string) (int64, error)) (basis string, cov []Coverage, unavailable string, err error) {
	source := f.source()
	if source == nil {
		return "", nil, NoSource, nil
	}
	basis, err = source.Basis()
	if err != nil {
		return "", nil, err.Error(), nil
	}

	ofLength := ""
	if n := f.length.of(basis); n > 0 {
		ofLength = fmt.Sprintf(" AND v.dims = %d", n)
	}
	err = f.st.ReadWith(ctx, func(db store.Reader) error {
		for _, d := range stores {
			c := Coverage{Store: d.Name}
			count := func(window int) (rows, embedded int64, err error) {
				where := hasText(d)
				if scope := vectorScope(d, window); scope != "" {
					where += " AND " + scope
				}
				if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+d.Name+" b WHERE "+where).Scan(&rows); err != nil {
					return 0, 0, fmt.Errorf("%s: %w", d.Name, err)
				}
				if embedded, err = vectors(ctx, db, d, basis, where+ofLength); err != nil {
					return 0, 0, fmt.Errorf("%s: %w", d.Name, err)
				}
				return rows, embedded, nil
			}
			window := f.windowFor(d, source)
			var err error
			if c.Rows, c.Embedded, err = count(window); err != nil {
				return err
			}
			if window > 0 {
				whole, wholeEmbedded, err := count(0)
				if err != nil {
					return err
				}
				c.Beyond, c.BeyondEmbedded = whole-c.Rows, wholeEmbedded-c.Embedded
			}
			cov = append(cov, c)
		}
		return nil
	})
	return basis, cov, "", err
}

func vectorsHeld(ctx context.Context, db store.Reader, d Store, basis, where string) (n int64, err error) {
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM memory_vectors v JOIN "+d.Name+" b ON b.id = v.id WHERE v.store = ? AND v.basis = ? AND "+where, d.Name, basis).Scan(&n)
	return n, err
}

func vectorsUsable(ctx context.Context, db store.Reader, d Store, basis, where string) (n int64, err error) {
	err = vectorsByText(ctx, db, d, basis, where, func(_ string, speaks bool) {
		if speaks {
			n++
		}
	})
	return n, err
}

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

type BackfillStore struct {
	Store     string
	Embedded  int
	Remaining int64
}

type BackfillReport struct {
	Basis       string
	Unavailable string
	Dropped     int64
	Pruned      int64
	Embedded    int
	Stores      []BackfillStore

	Yielded bool

	SetAside int

	Stale int64

	Worked time.Duration
	TimeUp bool
}

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
	if r.Stale > 0 {
		line += fmt.Sprintf("; %d vectors of rows whose text changed dropped, to be made again", r.Stale)
	}
	if r.SetAside > 0 {
		line += fmt.Sprintf("; %d set aside, which the source could not embed", r.SetAside)
	}
	if r.Yielded {
		line += "; the pass stopped early at the source's word"
	}
	if r.TimeUp {
		line += fmt.Sprintf("; the source worked %s, which is its pass, and the next goes on", r.Worked.Round(time.Second))
	}
	return line
}

func (f *Facility) Backfill(ctx context.Context, budget int) (BackfillReport, error) {
	var rep BackfillReport
	source := f.source()
	if source == nil {
		rep.Unavailable = NoSource
		return rep, nil
	}
	basis, err := source.Basis()
	if err != nil {
		rep.Unavailable = err.Error()
		return rep, nil
	}
	rep.Basis = basis
	if rep.Dropped, err = f.st.DropMemoryVectorsOffBasis(basis); err != nil {
		return rep, fmt.Errorf("drop vectors off basis: %w", err)
	}

	began := f.length.of(basis)
	held := began
	oneLength := func(vectors [][]float32) error {
		n := len(vectors[0])
		for _, v := range vectors {
			if len(v) != n {
				return fmt.Errorf("vectors of %d and %d dimensions in one answer", n, len(v))
			}
		}
		if n == 0 {
			return nil
		}
		f.length.note(basis, n)
		if held == 0 {
			held = n
		}
		if n != held {
			return fmt.Errorf("the length of the source's vectors changed (%d → %d dimensions); nothing of this answer was kept, and the next pass starts over", held, n)
		}
		return nil
	}
	if budget <= 0 {
		budget = BackfillBudget
	}
	batchSize := EmbedBatch
	if b, ok := source.(Batcher); ok && b.Batch() > 0 {
		batchSize = b.Batch()
	}
	yielder, _ := source.(Yielder)
	var epoch any
	if e, ok := source.(Epocher); ok {
		epoch = e.Epoch()
	}
	f.aside.begin(basis, epoch)

	var work, worked time.Duration
	if w, ok := source.(Worker); ok {
		work = w.PassWork()
	}
	timed := work > 0
	clock := f.clock
	if clock == nil {
		clock = time.Now
	}
	for _, d := range stores {
		if rep.Yielded || (timed && worked >= work) {
			break
		}
		pruned, err := f.st.PruneMemoryVectors(d.Name, d.Name)
		if err != nil {
			return rep, fmt.Errorf("%s: prune: %w", d.Name, err)
		}
		rep.Pruned += pruned
		stale, err := f.dropStaleVectors(ctx, d, basis)
		if err != nil {
			return rep, fmt.Errorf("%s: vectors of changed text: %w", d.Name, err)
		}
		rep.Stale += stale
		bs := BackfillStore{Store: d.Name}
	rounds:
		for (timed && worked < work) || (!timed && budget > 0) {

			want := backfillRound
			if !timed && budget < want {
				want = budget
			}
			var candidates []Hit
			if err := f.st.ReadWith(ctx, func(db store.Reader) error {
				var err error

				candidates, err = missingVectors(ctx, db, d, f.windowFor(d, source), began, basis, want+f.aside.in(d.Name))
				return err
			}); err != nil {
				return rep, fmt.Errorf("%s: candidates: %w", d.Name, err)
			}
			if candidates = f.aside.without(d.Name, candidates); len(candidates) > want {
				candidates = candidates[:want]
			}
			if len(candidates) == 0 {
				break
			}
			for start := 0; start < len(candidates); start += batchSize {

				if yielder != nil && !yielder.MayContinue() {
					rep.Yielded = true
					break rounds
				}

				if timed && worked >= work {
					break rounds
				}
				end := start + batchSize
				if end > len(candidates) {
					end = len(candidates)
				}
				batch := candidates[start:end]
				inputs := make([]string, len(batch))
				for i, h := range batch {
					inputs[i] = clipForEmbedding(h.Text)
				}
				began := clock()
				got, vectors, err := source.Embed(ctx, KindMemory, inputs)
				worked += clock().Sub(began)
				rep.Worked = worked
				if err != nil {

					var fault TextFault

					if len(batch) > 1 && errors.As(err, &fault) && fault.OfTheText() && !fault.EndsThePass() {
						var refused []Hit
						var last error
						for i, h := range batch {
							began := clock()
							got, one, err := source.Embed(ctx, KindMemory, inputs[i:i+1])
							worked += clock().Sub(began)
							rep.Worked = worked
							var own TextFault
							switch {
							case err != nil && errors.As(err, &own) && own.OfTheText() && !own.EndsThePass():
								refused, last = append(refused, h), err
								continue
							case err != nil:
								rep.Stores = append(rep.Stores, bs)
								return rep, fmt.Errorf("%s: embed: %w", d.Name, err)
							case got != basis:
								rep.Stores = append(rep.Stores, bs)
								return rep, fmt.Errorf("%s: the basis changed during the pass (%s → %s); the next pass starts over", d.Name, basis, got)
							case len(one) != 1:
								rep.Stores = append(rep.Stores, bs)
								return rep, fmt.Errorf("%s: %d vectors for 1 input", d.Name, len(one))
							}
							if err := oneLength(one); err != nil {
								rep.Stores = append(rep.Stores, bs)
								return rep, fmt.Errorf("%s: %w", d.Name, err)
							}
							q, scale := vec.Quantize(one[0])
							if err := f.st.PutMemoryVectors([]store.MemoryVector{{
								Store: d.Name, ID: h.ID, Basis: basis, ContentSHA: textSHA(h.Text),
								Dims: len(q), Scale: scale, Q: q, EmbeddedAt: time.Now(),
							}}); err != nil {
								rep.Stores = append(rep.Stores, bs)
								return rep, fmt.Errorf("%s: write vectors: %w", d.Name, err)
							}
							bs.Embedded++
							rep.Embedded++
						}

						if len(refused) == len(batch) && rep.Embedded == 0 {
							rep.Stores = append(rep.Stores, bs)
							return rep, fmt.Errorf("%s: embed: the source refused every text of a batch when asked for each alone: %w", d.Name, last)
						}
						for _, h := range refused {
							f.aside.add(d.Name, h.ID, textSHA(h.Text), last.Error())
							rep.SetAside++
						}
						budget -= len(batch)
						continue
					}
					if len(batch) == 1 && errors.As(err, &fault) && fault.OfTheText() {
						f.aside.add(d.Name, batch[0].ID, textSHA(batch[0].Text), err.Error())
						rep.SetAside++
						budget--
						if !fault.EndsThePass() {
							continue
						}
					}
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
				if err := oneLength(vectors); err != nil {
					rep.Stores = append(rep.Stores, bs)
					return rep, fmt.Errorf("%s: %w", d.Name, err)
				}
				rows := make([]store.MemoryVector, 0, len(batch))
				now := time.Now()
				for i, h := range batch {
					q, scale := vec.Quantize(vectors[i])
					rows = append(rows, store.MemoryVector{
						Store: d.Name, ID: h.ID, Basis: basis, ContentSHA: textSHA(h.Text),
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
		if err := f.st.ReadWith(ctx, func(db store.Reader) error {
			return db.QueryRowContext(ctx, missingCountSQL(d, f.windowFor(d, source), began), d.Name, basis).Scan(&bs.Remaining)
		}); err != nil {
			return rep, fmt.Errorf("%s: remaining: %w", d.Name, err)
		}
		rep.Stores = append(rep.Stores, bs)
	}
	rep.TimeUp = timed && worked >= work
	return rep, nil
}

func (f *Facility) dropStaleVectors(ctx context.Context, d Store, basis string) (int64, error) {
	var stale []string
	err := f.st.ReadWith(ctx, func(db store.Reader) error {
		return vectorsByText(ctx, db, d, basis, "", func(id string, speaks bool) {
			if !speaks {
				stale = append(stale, id)
			}
		})
	})
	if err != nil || len(stale) == 0 {
		return 0, err
	}
	return f.st.DeleteMemoryVectors(d.Name, stale)
}

func vectorsByText(ctx context.Context, db store.Reader, d Store, basis, where string, see func(id string, speaks bool)) error {
	q := "SELECT b.id, v.content_sha" + textColumns(d) + " FROM memory_vectors v JOIN " + d.Name + " b ON b.id = v.id WHERE v.store = ? AND v.basis = ?"
	if where != "" {
		q += " AND " + where
	}
	rows, err := db.QueryContext(ctx, q, d.Name, basis)
	if err != nil {
		return err
	}
	defer rows.Close()

	texts := make([]string, len(d.Text))
	var id, vectorOf string
	dest := []any{&id, &vectorOf}
	for i := range texts {
		dest = append(dest, &texts[i])
	}
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return err
		}
		see(id, vectorOf == textSHA(joinText(texts)))
	}
	return rows.Err()
}

func missingVectors(ctx context.Context, db store.Reader, d Store, window, dims int, basis string, limit int) ([]Hit, error) {
	q := "SELECT " + selectList(d) + missingFrom(d, window, dims) + " ORDER BY b.rowid DESC LIMIT ?"
	rows, err := db.QueryContext(ctx, q, d.Name, basis, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHits(rows, d)
}

func missingCountSQL(d Store, window, dims int) string {
	return "SELECT COUNT(*)" + missingFrom(d, window, dims)
}

func missingFrom(d Store, window, dims int) string {
	ofLength := ""
	if dims > 0 {
		ofLength = fmt.Sprintf(" AND v.dims = %d", dims)
	}
	from := " FROM " + d.Name + " b LEFT JOIN memory_vectors v ON v.store = ? AND v.id = b.id AND v.basis = ?" + ofLength + " WHERE v.id IS NULL AND " + hasText(d)
	if scope := vectorScope(d, window); scope != "" {
		from += " AND " + scope
	}
	return from
}
