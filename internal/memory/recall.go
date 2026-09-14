package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/memory/carrd"
	"github.com/aiii-dot-id/aii-os/internal/memory/fuse"
	"github.com/aiii-dot-id/aii-os/internal/memory/trigram"
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
// .
// .
// .
// .
// .

// .
const (
	DefaultLimit = 7
	MaxLimit     = 50
	PoolFactor   = 3
	MaxPool      = 64
	SnippetWords = 64
	// .
	// .
	// .
	// .
	// .
	// .
	FuzzyFloor = 0.5
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	EditMinLetters = 4
	EditLongFrom   = 8
	EditsShort     = 1
	EditsLong      = 2
)

// .
const (
	StatusFound             = "found"
	StatusFoundNothing      = "found_nothing"
	StatusPartial           = "partial"
	StatusQueryFailed       = "query_failed"
	StatusSourceUnavailable = "source_unavailable"
)

// .
// .
// .
const (
	MatchExactWords = "exact_words"
	MatchFuzzy      = "fuzzy"
	MatchMeaning    = "meaning"
	MatchBoth       = "both"
)

// .
const (
	DecayDefault = "carrd"
	DecayNone    = "none"
	// .
	// .
	DecayACTR = "actr"
)

// .
type Query struct {
	Text string
	// .
	// .
	Exact bool
	// .
	Since  time.Time
	Before time.Time
	// .
	// .
	Limit int
	// .
	// .
	Stores []string
	// .
	// .
	PluginID string
	// .
	// .
	Decay string
	// .
	// .
	Reinforce bool
	// .
	Now time.Time
}

// .
type Hit struct {
	Store       string
	ID          string
	Seq         uint64
	Ring        int
	Time        time.Time
	Attribution string
	Text        string
	// .
	Snippet string
	Match   string
	// .
	// .
	Fused    float64
	Strength float64
	Score    float64
	Class    carrd.Class
	Accesses int64
	// .
	// .
	Fuzz float64
	// .
	// .
	Similarity float64
	// .
	// .
	// .
	Graph float64
	// .
	// .
	layerRank int
}

// .
type Source struct {
	Store   string
	Status  string
	Matched int
	Shown   int
	Detail  string
}

// .
// .
type Result struct {
	Hits      []Hit
	Sources   []Source
	Policy    string
	Limit     int
	Truncated bool
	// .
	// .
	// .
	Warnings []string
	// .
	// .
	// .
	Meaning MeaningStatus
}

// .
type Facility struct {
	st       *store.Store
	embedder Embedder
}

// .
func New(st *store.Store) *Facility { return &Facility{st: st} }

// .
func (f *Facility) Recall(ctx context.Context, q Query) (Result, error) {
	now := q.Now
	if now.IsZero() {
		now = time.Now()
	}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}
	pool := limit * PoolFactor
	if pool > MaxPool {
		pool = MaxPool
	}
	policy := q.Decay
	if policy == "" {
		policy = DecayDefault
	}
	if policy != DecayDefault && policy != DecayNone && policy != DecayACTR {
		return Result{}, fmt.Errorf("decay policy %q is not known; the policies are %s, %s and %s", q.Decay, DecayDefault, DecayACTR, DecayNone)
	}
	text := strings.TrimSpace(q.Text)
	if text == "" {
		return Result{}, errors.New("recall needs a query; a listing is Enumerate's read")
	}
	stores, err := selectStores(q)
	if err != nil {
		return Result{}, err
	}
	words := wordsOf(text)
	match := wordsQuery(words)
	if q.Exact {
		match = phraseQuery(words)
	}
	// .
	// .
	// .
	var qv *queryVector
	var meaning MeaningStatus
	if !q.Exact {
		qv, meaning = f.embedQuery(ctx, text)
	}

	res := Result{Policy: policy, Limit: limit, Meaning: meaning}
	var pools []fuse.Pool
	byKey := map[fuse.Key]*Hit{}
	exactKeys := map[fuse.Key]bool{}
	counted := map[fuse.Key]bool{}
	readErr := f.st.ReadWith(func(db *sql.DB) error {
		for _, d := range stores {
			src := Source{Store: d.Name}
			if match == "" {
				src.Status = StatusFoundNothing
				src.Detail = "no searchable words in the query"
				res.Sources = append(res.Sources, src)
				continue
			}
			exact, matched, err := layerExact(ctx, db, d, q, match, pool, true)
			if err != nil {
				src.Status = classify(err)
				src.Detail = err.Error()
				res.Sources = append(res.Sources, src)
				continue
			}
			src.Matched = matched
			p := fuse.Pool{Layer: MatchExactWords}
			for i := range exact {
				h := exact[i]
				k := fuse.Key{Store: h.Store, ID: h.ID}
				exactKeys[k] = true
				if _, seen := byKey[k]; !seen {
					byKey[k] = &h
				}
				p.Keys = append(p.Keys, k)
			}
			pools = append(pools, p)
			if !q.Exact {
				fuzzy, err := layerFuzzy(ctx, db, d, q, text, words, pool, true)
				if err != nil {
					res.Warnings = append(res.Warnings, fmt.Sprintf("%s: the fuzzy layer failed and only exact words answered — %v", d.Name, err))
				} else if len(fuzzy) > 0 {
					p := fuse.Pool{Layer: MatchFuzzy}
					for i := range fuzzy {
						h := fuzzy[i]
						k := fuse.Key{Store: h.Store, ID: h.ID}
						if _, seen := byKey[k]; !seen {
							byKey[k] = &h
						}
						// .
						// .
						// .
						if !exactKeys[k] && !counted[k] && !carriesAllWords(words, h.Text) {
							src.Matched++
							counted[k] = true
						}
						p.Keys = append(p.Keys, k)
					}
					pools = append(pools, p)
				}
			}
			// .
			// .
			// .
			if qv != nil {
				meaningHits, err := layerMeaning(ctx, db, d, q, qv, pool)
				if err != nil {
					res.Warnings = append(res.Warnings, fmt.Sprintf("%s: the meaning layer failed and the words answered alone — %v", d.Name, err))
				} else if len(meaningHits) > 0 {
					res.Meaning.Status = StatusFound
					p := fuse.Pool{Layer: MatchMeaning}
					for i := range meaningHits {
						h := meaningHits[i]
						k := fuse.Key{Store: h.Store, ID: h.ID}
						if held, seen := byKey[k]; !seen {
							byKey[k] = &h
						} else {
							held.Similarity = h.Similarity
						}
						if !exactKeys[k] && !counted[k] && !carriesAllWords(words, h.Text) {
							src.Matched++
							counted[k] = true
						}
						p.Keys = append(p.Keys, k)
					}
					pools = append(pools, p)
				}
			}
			res.Sources = append(res.Sources, src)
		}
		return nil
	})
	if readErr != nil {
		return res, readErr
	}

	fused := fuse.RRF(fuse.K, pools...)
	// .
	// .
	// .
	boosts := map[fuse.Key]float64{}
	if err := f.st.ReadWith(func(db *sql.DB) error {
		var err error
		boosts, err = graphBoost(ctx, db, fused)
		return err
	}); err != nil {
		res.Warnings = append(res.Warnings, "graph activation unavailable, hits ranked by the layers alone: "+err.Error())
		boosts = map[fuse.Key]float64{}
	}
	// .
	idsByStore := map[string][]string{}
	for _, fz := range fused {
		idsByStore[fz.Key.Store] = append(idsByStore[fz.Key.Store], fz.Key.ID)
	}
	accessByStore := map[string]map[string]store.MemoryAccess{}
	for name, ids := range idsByStore {
		acc, err := f.st.MemoryAccesses(name, ids)
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: access records unreadable, decay computed as never recalled — %v", name, err))
			acc = nil
		}
		accessByStore[name] = acc
	}
	hits := make([]Hit, 0, len(fused))
	for _, fz := range fused {
		h := byKey[fz.Key]
		if h == nil {
			continue
		}
		h.Match = matchOf(fz.Layers)
		h.Fused = fz.Score
		if a, ok := accessByStore[h.Store][h.ID]; ok {
			h.Accesses = a.Count
		}
		d, _ := Lookup(h.Store)
		h.Class = classOf(d, *h)
		var access store.MemoryAccess
		if a, ok := accessByStore[h.Store][h.ID]; ok {
			access = a
		}
		h.Strength = strengthUnder(policy, h.Class, h.Time, access, now)
		h.Graph = 1
		if b, ok := boosts[fz.Key]; ok && b > 0 {
			h.Graph = b
		}
		h.Score = carrd.Final(h.Fused, 1, h.Strength, 1) * h.Graph
		h.Snippet = Excerpt(h.Text, words, SnippetWords)
		hits = append(hits, *h)
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if hits[i].Store != hits[j].Store {
			return hits[i].Store < hits[j].Store
		}
		return hits[i].ID < hits[j].ID
	})
	if len(hits) > limit {
		res.Truncated = true
		hits = hits[:limit]
	}
	res.Hits = hits

	shown := map[string]int{}
	for _, h := range hits {
		shown[h.Store]++
	}
	for i := range res.Sources {
		s := &res.Sources[i]
		s.Shown = shown[s.Store]
		if s.Status != "" {
			continue
		}
		switch {
		case s.Matched == 0:
			s.Status = StatusFoundNothing
		case s.Shown < s.Matched:
			s.Status = StatusPartial
		default:
			s.Status = StatusFound
		}
	}

	if q.Reinforce && len(hits) > 0 {
		refs := make([]store.MemoryRef, 0, len(hits))
		for _, h := range hits {
			refs = append(refs, store.MemoryRef{Store: h.Store, ID: h.ID})
		}
		if err := f.st.RecordMemoryAccess(now, refs...); err != nil {
			res.Warnings = append(res.Warnings, "reinforcement not recorded: "+err.Error())
		}
	}
	return res, nil
}

// .
// .
// .
// .
// .
func (f *Facility) Enumerate(ctx context.Context, storeName, text string, exact bool, beforeSeq uint64, limit int) ([]Hit, int, error) {
	d, ok := Lookup(storeName)
	if !ok {
		return nil, 0, fmt.Errorf("no store named %q", storeName)
	}
	if d.Seq == "" {
		return nil, 0, fmt.Errorf("store %s has no sequence to page by", storeName)
	}
	if d.Name == "plugin_memories" {
		return nil, 0, errors.New("a plugin's record is read through its own verbs, not enumerated")
	}
	if beforeSeq == 0 {
		beforeSeq = 1 << 62
	}
	if limit <= 0 {
		limit = DefaultLimit
	}
	text = strings.TrimSpace(text)
	match := ""
	if text != "" {
		words := wordsOf(text)
		match = wordsQuery(words)
		if exact {
			match = phraseQuery(words)
		}
		if match == "" {
			return nil, 0, errors.New("no searchable words in the query")
		}
	}
	var hits []Hit
	var total int
	err := f.st.ReadWith(func(db *sql.DB) error {
		where, args := predicates(d, Query{}, false)
		countSQL := "SELECT COUNT(*) FROM " + d.Name + " b"
		if where != "" {
			countSQL += " WHERE " + where
		}
		if err := db.QueryRowContext(ctx, countSQL, args...).Scan(&total); err != nil {
			return err
		}
		var sqlText string
		var qargs []any
		if match == "" {
			sqlText = "SELECT " + selectList(d) + " FROM " + d.Name + " b WHERE b." + d.Seq + " < ?"
			qargs = append(qargs, int64(beforeSeq))
		} else {
			sqlText = "SELECT " + selectList(d) + " FROM " + d.FTS + " f JOIN " + d.Name + " b ON b.rowid = f.rowid WHERE " + d.FTS + " MATCH ? AND b." + d.Seq + " < ?"
			qargs = append(qargs, match, int64(beforeSeq))
		}
		if where != "" {
			sqlText += " AND " + where
			qargs = append(qargs, args...)
		}
		sqlText += " ORDER BY b." + d.Seq + " DESC LIMIT ?"
		qargs = append(qargs, limit)
		rows, err := db.QueryContext(ctx, sqlText, qargs...)
		if err != nil {
			return err
		}
		defer rows.Close()
		hits, err = scanHits(rows, d)
		return err
	})
	if err != nil {
		return nil, 0, err
	}
	for i := range hits {
		if match != "" {
			hits[i].Match = MatchExactWords
		}
		hits[i].Strength = 1
	}
	return hits, total, nil
}

// .
// .
func selectStores(q Query) ([]Store, error) {
	var out []Store
	if len(q.Stores) == 0 {
		for _, d := range Stores() {
			if d.Name == "plugin_memories" {
				continue
			}
			out = append(out, d)
		}
		return out, nil
	}
	for _, name := range q.Stores {
		d, ok := Lookup(name)
		if !ok {
			return nil, fmt.Errorf("no store named %q", name)
		}
		if d.Name == "plugin_memories" && q.PluginID == "" {
			return nil, errors.New("plugin_memories is searched per plugin: a plugin id is required")
		}
		out = append(out, d)
	}
	return out, nil
}

// .
// .
func predicates(d Store, q Query, ranked bool) (string, []any) {
	var parts []string
	var args []any
	if d.Filter != "" {
		parts = append(parts, "("+d.Filter+")")
	}
	if ranked && d.Current != "" {
		parts = append(parts, "("+d.Current+")")
	}
	if !q.Since.IsZero() {
		parts = append(parts, "datetime("+d.Time+") >= datetime(?)")
		args = append(args, q.Since.UTC().Format(time.RFC3339))
	}
	if !q.Before.IsZero() {
		parts = append(parts, "datetime("+d.Time+") < datetime(?)")
		args = append(args, q.Before.UTC().Format(time.RFC3339))
	}
	if d.Name == "plugin_memories" {
		parts = append(parts, "b.plugin_id = ?")
		args = append(args, q.PluginID)
	}
	return strings.Join(parts, " AND "), args
}

// .
func selectList(d Store) string {
	seq := "0"
	if d.Seq != "" {
		seq = "COALESCE(b." + d.Seq + ", 0)"
	}
	ring := strconv.Itoa(d.Ring)
	if d.RingColumn != "" {
		ring = "COALESCE(b." + d.RingColumn + ", 0)"
	}
	attr := "'" + d.Attribution + "'"
	if d.AttributionColumn != "" {
		attr = "COALESCE(b." + d.AttributionColumn + ", '')"
	}
	cols := []string{"b.rowid", "b.id", seq, ring, "COALESCE(" + d.Time + ", '')", attr}
	for _, c := range d.Text {
		cols = append(cols, "COALESCE(b."+c+", '')")
	}
	return strings.Join(cols, ", ")
}

func scanHits(rows *sql.Rows, d Store) ([]Hit, error) {
	var out []Hit
	for rows.Next() {
		var rowid, seq, ring int64
		var id, when, attr string
		texts := make([]string, len(d.Text))
		dest := []any{&rowid, &id, &seq, &ring, &when, &attr}
		for i := range texts {
			dest = append(dest, &texts[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		h := Hit{Store: d.Name, ID: id, Ring: int(ring), Attribution: attribution(attr)}
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
// .
func layerExact(ctx context.Context, db *sql.DB, d Store, q Query, match string, pool int, ranked bool) ([]Hit, int, error) {
	where, args := predicates(d, q, ranked)
	from := " FROM " + d.FTS + " f JOIN " + d.Name + " b ON b.rowid = f.rowid WHERE " + d.FTS + " MATCH ?"
	if where != "" {
		from += " AND " + where
	}
	qargs := append([]any{match}, args...)
	var matched int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*)"+from, qargs...).Scan(&matched); err != nil {
		return nil, 0, err
	}
	rows, err := db.QueryContext(ctx, "SELECT "+selectList(d)+from+" ORDER BY f.rank LIMIT ?", append(qargs, pool)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	hits, err := scanHits(rows, d)
	if err != nil {
		return nil, 0, err
	}
	for i := range hits {
		hits[i].Match = MatchExactWords
	}
	return hits, matched, nil
}

// .
// .
// .
func layerFuzzy(ctx context.Context, db *sql.DB, d Store, q Query, text string, words []string, pool int, ranked bool) ([]Hit, error) {
	match := trigramQuery(words)
	if match == "" {
		return nil, nil
	}
	where, args := predicates(d, q, ranked)
	from := " FROM " + d.Tri + " f JOIN " + d.Name + " b ON b.rowid = f.rowid WHERE " + d.Tri + " MATCH ?"
	if where != "" {
		from += " AND " + where
	}
	qargs := append([]any{match}, args...)
	rows, err := db.QueryContext(ctx, "SELECT "+selectList(d)+from+" ORDER BY f.rank LIMIT ?", append(qargs, pool*2)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	candidates, err := scanHits(rows, d)
	if err != nil {
		return nil, err
	}
	var kept []Hit
	for _, h := range candidates {
		cov := trigram.Coverage(text, h.Text)
		if cov < FuzzyFloor {
			// .
			// .
			// .
			near, ok := nearByEdits(words, h.Text)
			if !ok {
				continue
			}
			cov = near
		}
		h.Fuzz = cov
		h.Match = MatchFuzzy
		kept = append(kept, h)
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].Fuzz != kept[j].Fuzz {
			return kept[i].Fuzz > kept[j].Fuzz
		}
		return kept[i].layerRank < kept[j].layerRank
	})
	if len(kept) > pool {
		kept = kept[:pool]
	}
	return kept, nil
}

// .
// .
func classify(err error) string {
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "no such table") || strings.Contains(msg, "database is closed") || strings.Contains(msg, "no such view") {
		return StatusSourceUnavailable
	}
	return StatusQueryFailed
}

func matchOf(layers []string) string {
	exact, fuzzy, meaning := false, false, false
	for _, l := range layers {
		switch l {
		case MatchExactWords:
			exact = true
		case MatchFuzzy:
			fuzzy = true
		case MatchMeaning:
			meaning = true
		}
	}
	switch {
	case meaning && (exact || fuzzy), exact && fuzzy:
		return MatchBoth
	case meaning:
		return MatchMeaning
	case fuzzy:
		return MatchFuzzy
	default:
		return MatchExactWords
	}
}

// .
// .
func classOf(d Store, h Hit) carrd.Class {
	if d.RingColumn != "" && h.Ring > 0 && h.Ring <= 2 {
		return carrd.Constitutional
	}
	return d.Class
}

// .
// .
func attribution(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "resident":
		return "self"
	case "":
		return "unknown"
	}
	return strings.ToLower(strings.TrimSpace(v))
}

var timeLayouts = []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000Z", "2006-01-02 15:04:05"}

func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// .
// .
func wordsOf(text string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return out
}

// .
// .
// .
// .
// .
// .
func nearByEdits(words []string, text string) (float64, bool) {
	if len(words) == 0 {
		return 0, false
	}
	textWords := wordsOf(text)
	lowered := make([]string, len(textWords))
	present := map[string]bool{}
	for i, w := range textWords {
		lowered[i] = strings.ToLower(w)
		present[lowered[i]] = true
	}
	total := 0.0
	for _, w := range words {
		w = strings.ToLower(w)
		if present[w] {
			total++
			continue
		}
		letters := utf8.RuneCountInString(w)
		if letters < EditMinLetters {
			return 0, false
		}
		allowed := EditsShort
		if letters >= EditLongFrom {
			allowed = EditsLong
		}
		best := allowed + 1
		for _, tw := range lowered {
			if diff := utf8.RuneCountInString(tw) - letters; diff > allowed || diff < -allowed {
				continue
			}
			if d := trigram.Edits(w, tw); d < best {
				best = d
				if d == 0 {
					break
				}
			}
		}
		if best > allowed {
			return 0, false
		}
		total += 1 - float64(best)/float64(letters)
	}
	return total / float64(len(words)), true
}

// .
// .
// .
func carriesAllWords(words []string, text string) bool {
	if len(words) == 0 {
		return false
	}
	have := map[string]bool{}
	for _, w := range wordsOf(text) {
		have[strings.ToLower(w)] = true
	}
	for _, w := range words {
		if !have[strings.ToLower(w)] {
			return false
		}
	}
	return true
}

// .
// .
func wordsQuery(words []string) string {
	if len(words) == 0 {
		return ""
	}
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = `"` + w + `"`
	}
	return strings.Join(quoted, " ")
}

// .
func phraseQuery(words []string) string {
	if len(words) == 0 {
		return ""
	}
	return `"` + strings.Join(words, " ") + `"`
}

// .
// .
// .
func trigramQuery(words []string) string {
	seen := map[string]bool{}
	var terms []string
	for _, w := range words {
		for _, t := range trigram.Windows(w) {
			if seen[t] {
				continue
			}
			seen[t] = true
			terms = append(terms, `"`+t+`"`)
		}
	}
	return strings.Join(terms, " OR ")
}

// .
// .
func Excerpt(text string, words []string, max int) string {
	tokens := strings.Fields(text)
	if len(tokens) <= max {
		return strings.Join(tokens, " ")
	}
	at := 0
	lower := make([]string, len(words))
	for i, w := range words {
		lower[i] = strings.ToLower(w)
	}
	for i, tok := range tokens {
		t := strings.ToLower(tok)
		hit := false
		for _, w := range lower {
			if w != "" && strings.Contains(t, w) {
				hit = true
				break
			}
		}
		if hit {
			at = i
			break
		}
	}
	start := at - max/3
	if start < 0 {
		start = 0
	}
	end := start + max
	if end > len(tokens) {
		end = len(tokens)
		start = end - max
		if start < 0 {
			start = 0
		}
	}
	out := strings.Join(tokens[start:end], " ")
	if start > 0 {
		out = "…" + out
	}
	if end < len(tokens) {
		out += "…"
	}
	return out
}
