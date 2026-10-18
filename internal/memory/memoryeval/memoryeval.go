package memoryeval

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

type Manifest struct {
	Corpus     string            `json:"corpus"`
	Frozen     string            `json:"frozen"`
	Files      map[string]string `json:"files"`
	Thresholds Thresholds        `json:"thresholds"`

	SHA256 string `json:"-"`

	Store string `json:"-"`
	dir   string
}

type Thresholds struct {
	K                          int      `json:"k"`
	MeaningHitMin              float64  `json:"meaning_hit_at_k_min,omitempty"`
	MeaningOverWordsMin        float64  `json:"meaning_hit_at_k_over_words_min,omitempty"`
	TrapByMeaningMax           *float64 `json:"trap_avoided_by_meaning_max,omitempty"`
	ParaphraseHitMin           float64  `json:"paraphrase_hit_at_k_min,omitempty"`
	ControlByMeaningMax        *float64 `json:"control_offered_by_meaning_max,omitempty"`
	CandidateParaphraseGainMin *float64 `json:"candidate_paraphrase_gain_min,omitempty"`
	CandidateOwnAndKeysLossMax *float64 `json:"candidate_own_and_keys_loss_max,omitempty"`
}

type Memory struct {
	ID   string `json:"id"`
	Lang string `json:"lang"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type Cue struct {
	ID     string   `json:"id"`
	Lang   string   `json:"lang"`
	Type   string   `json:"type"`
	Text   string   `json:"text"`
	Expect []string `json:"expect"`
	Avoid  []string `json:"avoid"`
}

const (
	CueMeaning   = "meaning"
	CueTrap      = "words_trap"
	CueParaShort = "para_short"
	CueParaLong  = "para_long"
	CueControl   = "control"

	KindBelief       = "belief"
	storeExperiences = "experiences"
	storeBeliefs     = "beliefs"
	corpusFile       = "corpus.jsonl"
	cuesFile         = "cues.jsonl"
)

var (
	ErrFrozenMismatch = errors.New("the fixtures are not the ones the manifest froze")

	ErrIncomplete = errors.New("the run is incomplete and is not scored")

	ErrStoreExists = errors.New("the evaluator builds only a store it creates, and this path exists")
)

func Load(dir string) (*Manifest, []Memory, []Cue, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, nil, nil, err
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, nil, nil, fmt.Errorf("manifest: %w", err)
	}
	if m.Thresholds.K <= 0 || m.Thresholds.MeaningHitMin <= 0 && m.Thresholds.ParaphraseHitMin <= 0 || len(m.Files) == 0 {
		return nil, nil, nil, errors.New("manifest: k, files and a hit figure (meaning_hit_at_k_min or paraphrase_hit_at_k_min) are required")
	}
	sum := sha256.Sum256(raw)
	m.SHA256, m.dir = hex.EncodeToString(sum[:]), dir
	for name, want := range m.Files {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, nil, nil, err
		}
		if s := sha256.Sum256(b); hex.EncodeToString(s[:]) != want {
			return nil, nil, nil, fmt.Errorf("%w: %s", ErrFrozenMismatch, name)
		}
	}
	if _, ok := m.Files[corpusFile]; !ok {
		return nil, nil, nil, fmt.Errorf("manifest: %s is not among the frozen files", corpusFile)
	}
	if _, ok := m.Files[cuesFile]; !ok {
		return nil, nil, nil, fmt.Errorf("manifest: %s is not among the frozen files", cuesFile)
	}
	var corpus []Memory
	if err := readJSONL(filepath.Join(dir, corpusFile), func(line []byte) error {
		var r Memory
		if err := json.Unmarshal(line, &r); err != nil {
			return err
		}
		corpus = append(corpus, r)
		return nil
	}); err != nil {
		return nil, nil, nil, fmt.Errorf("%s: %w", corpusFile, err)
	}
	ids := map[string]bool{}
	beliefs := 0
	for _, r := range corpus {
		if r.ID == "" || strings.TrimSpace(r.Text) == "" || ids[r.ID] {
			return nil, nil, nil, fmt.Errorf("%s: every memory needs a distinct id and text (%q)", corpusFile, r.ID)
		}
		ids[r.ID] = true
		if r.Kind == KindBelief {
			beliefs++
		}
	}
	switch beliefs {
	case 0:
		m.Store = storeExperiences
	case len(corpus):
		m.Store = storeBeliefs
	default:
		return nil, nil, nil, fmt.Errorf("%s: %d of %d rows are beliefs; a corpus is planted in one store and recalled from it", corpusFile, beliefs, len(corpus))
	}
	var cues []Cue
	if err := readJSONL(filepath.Join(dir, cuesFile), func(line []byte) error {
		var c Cue
		if err := json.Unmarshal(line, &c); err != nil {
			return err
		}
		for _, id := range append(append([]string{}, c.Expect...), c.Avoid...) {
			if !ids[id] {
				return fmt.Errorf("cue %s names %s, which is not in the corpus", c.ID, id)
			}
		}
		cues = append(cues, c)
		return nil
	}); err != nil {
		return nil, nil, nil, fmt.Errorf("%s: %w", cuesFile, err)
	}
	if len(corpus) == 0 || len(cues) == 0 {
		return nil, nil, nil, errors.New("a corpus and cues are required")
	}
	return &m, corpus, cues, nil
}

func readJSONL(path string, each func([]byte) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 1<<16), 16<<20)
	for s.Scan() {
		if len(strings.TrimSpace(s.Text())) == 0 {
			continue
		}
		if err := each(s.Bytes()); err != nil {
			return err
		}
	}
	return s.Err()
}

type RecordedBasis struct {
	Name    string
	vectors map[string][]float32
	dims    int
	floor   float64
}

func ReadBasis(path string) (*RecordedBasis, error) {
	rb := &RecordedBasis{vectors: map[string][]float32{}, floor: memory.ProviderFloor}
	err := readJSONL(path, func(line []byte) error {
		var row struct {
			Basis  string    `json:"basis"`
			Floor  *float64  `json:"floor"`
			SHA256 string    `json:"sha256"`
			V      []float32 `json:"v"`
		}
		if err := json.Unmarshal(line, &row); err != nil {
			return err
		}
		if row.Basis != "" {
			rb.Name = row.Basis
			if row.Floor != nil {
				if !(*row.Floor >= 0 && *row.Floor <= 1) {
					return fmt.Errorf("a floor of %v is not a cosine", *row.Floor)
				}
				rb.floor = *row.Floor
			}
			return nil
		}
		if len(row.SHA256) != 64 || len(row.V) == 0 {
			return errors.New("every row is a basis name or a sha256 with a vector")
		}
		if rb.dims == 0 {
			rb.dims = len(row.V)
		} else if rb.dims != len(row.V) {
			return fmt.Errorf("a vector of %d dimensions in a basis of %d", len(row.V), rb.dims)
		}
		rb.vectors[row.SHA256] = row.V
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("basis %s: %w", path, err)
	}
	if rb.Name == "" || len(rb.vectors) == 0 {
		return nil, fmt.Errorf("basis %s: a name and at least one vector are required", path)
	}
	return rb, nil
}

func (rb *RecordedBasis) Basis() (string, error) { return rb.Name, nil }

func (rb *RecordedBasis) Floor() float64 { return rb.floor }

func (rb *RecordedBasis) Embed(_ context.Context, _ memory.Kind, inputs []string) (string, [][]float32, error) {
	out := make([][]float32, len(inputs))
	for i, in := range inputs {
		sum := sha256.Sum256([]byte(in))
		v, ok := rb.vectors[hex.EncodeToString(sum[:])]
		if !ok {
			return rb.Name, nil, fmt.Errorf("%w: the basis %s holds no vector for a text of %d characters beginning %q", ErrIncomplete, rb.Name, len([]rune(in)), head(in))
		}
		out[i] = v
	}
	return rb.Name, out, nil
}

func head(s string) string {
	r := []rune(s)
	if len(r) > 24 {
		r = r[:24]
	}
	return string(r)
}

type Options struct {
	Basis memory.Embedder

	Policy string
}

func Build(path string, corpus []Memory) (*store.Store, error) {
	if _, err := os.Lstat(path); err == nil {
		return nil, fmt.Errorf("%w: %s", ErrStoreExists, path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	st, err := store.New(path)
	if err != nil {
		return nil, err
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, m := range corpus {
		typ, row := ledger.EventExperienceCreate, map[string]any{"id": m.ID, "content": m.Text, "category": "observation", "provenance": "self"}
		if m.Kind == KindBelief {
			typ, row = ledger.EventBeliefUpsert, map[string]any{"id": m.ID, "statement": m.Text, "ring": 3}
		}
		payload, err := json.Marshal(row)
		if err != nil {
			st.Close()
			return nil, err
		}
		sum := sha256.Sum256(payload)
		evt := &ledger.Event{Seq: uint64(i + 1), Timestamp: base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339Nano),
			Type: typ, Ring: 3, Content: hex.EncodeToString(sum[:]), Payload: payload}
		if err := st.Materialize(evt); err != nil {
			st.Close()
			return nil, fmt.Errorf("memory %s: %w", m.ID, err)
		}
	}
	return st, nil
}

type TypeScore struct {
	N            int     `json:"n"`
	Hit1         float64 `json:"hit_at_1"`
	HitK         float64 `json:"hit_at_k"`
	MRR          float64 `json:"mrr"`
	Siblings     int     `json:"siblings_named"`
	SiblingFirst float64 `json:"sibling_first"`
}

type TrapScore struct {
	N                 int     `json:"n"`
	AvoidedFirst      float64 `json:"avoided_ranked_first"`
	AvoidedByMeaning  float64 `json:"avoided_matched_by_meaning"`
	ExpectedWhenNamed float64 `json:"expected_hit_at_k_when_named"`
	named             int
}

type ControlScore struct {
	N                int      `json:"n"`
	OfferedByMeaning float64  `json:"offered_by_meaning"`
	Offered          []string `json:"offered"`
}

type Report struct {
	ManifestSHA256 string `json:"manifest_sha256"`
	Corpus         string `json:"corpus"`
	Frozen         string `json:"frozen"`

	Store string `json:"store"`
	Basis string `json:"basis"`

	Floor  float64 `json:"floor,omitempty"`
	Policy string  `json:"policy"`
	K      int     `json:"k"`

	Memories int                  `json:"memories"`
	Cues     int                  `json:"cues"`
	ByType   map[string]TypeScore `json:"by_type"`

	Paraphrase *TypeScore    `json:"paraphrase,omitempty"`
	Trap       *TrapScore    `json:"trap,omitempty"`
	Control    *ControlScore `json:"control,omitempty"`

	Words *Report `json:"words,omitempty"`

	Misses []string `json:"misses"`
}

type tally struct {
	n, sib          int
	h1, hk, rr, sf1 float64
}

func (t *tally) add(o *tally) {
	t.n, t.sib = t.n+o.n, t.sib+o.sib
	t.h1, t.hk, t.rr, t.sf1 = t.h1+o.h1, t.hk+o.hk, t.rr+o.rr, t.sf1+o.sf1
}

func (t *tally) score() TypeScore {
	s := TypeScore{N: t.n, Hit1: t.h1 / float64(t.n), HitK: t.hk / float64(t.n), MRR: t.rr / float64(t.n), Siblings: t.sib}
	if t.sib > 0 {
		s.SiblingFirst = t.sf1 / float64(t.sib)
	}
	return s
}

func Run(ctx context.Context, dir string, m *Manifest, corpus []Memory, cues []Cue, o Options) (*Report, error) {
	var words *Report
	if o.Basis != nil {
		var err error
		if words, err = Run(ctx, filepath.Join(dir, "words"), m, corpus, cues, Options{Policy: o.Policy}); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	st, err := Build(filepath.Join(dir, "eval.db"), corpus)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	rep := &Report{ManifestSHA256: m.SHA256, Corpus: m.Corpus, Frozen: m.Frozen, Store: m.Store, Basis: "words only",
		Policy: o.Policy, K: m.Thresholds.K, Memories: len(corpus), Cues: len(cues), ByType: map[string]TypeScore{}, Words: words}
	if rep.Policy == "" {
		rep.Policy = memory.DecayDefault
	}
	f := memory.New(st)
	if o.Basis != nil {
		f.SetEmbedder(o.Basis)
		bf, err := f.Backfill(ctx, len(corpus)+1)
		if err != nil {
			return nil, fmt.Errorf("%w: backfill: %v", ErrIncomplete, err)
		}
		if bf.Unavailable != "" || bf.Embedded != len(corpus) {
			return nil, fmt.Errorf("%w: the backfill embedded %d of %d memories (%s)", ErrIncomplete, bf.Embedded, len(corpus), bf.Unavailable)
		}
		rep.Basis, rep.Floor = bf.Basis, o.Basis.Floor()
	}
	tallies := map[string]*tally{}
	var trap TrapScore
	var control ControlScore
	for _, c := range cues {
		res, err := f.Recall(ctx, memory.Query{Text: c.Text, Limit: m.Thresholds.K, Decay: o.Policy, Stores: []string{m.Store}})
		if err != nil {
			return nil, fmt.Errorf("%w: cue %s: %v", ErrIncomplete, c.ID, err)
		}
		if o.Basis != nil && (res.Meaning.Status == memory.StatusSourceUnavailable || res.Meaning.Status == memory.StatusPartial) {
			return nil, fmt.Errorf("%w: cue %s: the meaning layer was unavailable or read short (%s)", ErrIncomplete, c.ID, res.Meaning.Detail)
		}
		expect, avoid := map[string]bool{}, map[string]bool{}
		for _, id := range c.Expect {
			expect[id] = true
		}
		for _, id := range c.Avoid {
			avoid[id] = true
		}
		rank := -1
		for i, h := range res.Hits {
			if expect[h.ID] {
				rank = i
				break
			}
		}
		if len(c.Expect) > 0 {
			t := tallies[c.Type]
			if t == nil {
				t = &tally{}
				tallies[c.Type] = t
			}
			t.n++
			if rank == 0 {
				t.h1++
			}
			if rank >= 0 {
				t.hk++
				t.rr += 1 / float64(rank+1)
			} else {
				rep.Misses = append(rep.Misses, c.ID)
			}
			if len(c.Avoid) > 0 {
				t.sib++
				for _, h := range res.Hits {
					if expect[h.ID] {
						break
					}
					if avoid[h.ID] {
						t.sf1++
						break
					}
				}
			}
		}
		if c.Type == CueTrap && len(c.Avoid) > 0 {
			trap.N++
			if len(res.Hits) > 0 && avoid[res.Hits[0].ID] {
				trap.AvoidedFirst++
			}
			for _, h := range res.Hits {
				if avoid[h.ID] && byMeaning(h) {
					trap.AvoidedByMeaning++
					break
				}
			}
			if len(c.Expect) > 0 {
				trap.named++
				if rank >= 0 {
					trap.ExpectedWhenNamed++
				}
			}
		}
		if c.Type == CueControl {
			control.N++
			for _, h := range res.Hits {
				if byMeaning(h) {
					control.Offered = append(control.Offered, c.ID)
					break
				}
			}
		}
	}
	var para tally
	for typ, t := range tallies {
		rep.ByType[typ] = t.score()
		if typ == CueParaShort || typ == CueParaLong {
			para.add(t)
		}
	}
	if para.n > 0 {
		s := para.score()
		rep.Paraphrase = &s
	}
	if trap.N > 0 {
		trap.AvoidedFirst /= float64(trap.N)
		trap.AvoidedByMeaning /= float64(trap.N)
		if trap.named > 0 {
			trap.ExpectedWhenNamed /= float64(trap.named)
		}
		rep.Trap = &trap
	}
	if control.N > 0 {
		control.OfferedByMeaning = float64(len(control.Offered)) / float64(control.N)
		rep.Control = &control
	}
	sort.Strings(rep.Misses)
	return rep, nil
}

func byMeaning(h memory.Hit) bool {
	for _, l := range h.Layers {
		if l == memory.MatchMeaning {
			return true
		}
	}
	return false
}

func (r *Report) row() (one, many string) {
	if r.Store == storeBeliefs {
		return "belief", "beliefs"
	}
	return "memory", "memories"
}

func (r *Report) Violations(t Thresholds) []string {
	if t.MeaningHitMin <= 0 && t.ParaphraseHitMin <= 0 {
		return []string{"the thresholds state no hit figure: nothing to hold the run to"}
	}
	var out []string
	if t.MeaningHitMin > 0 {
		if s, ok := r.ByType[CueMeaning]; !ok {
			out = append(out, "no meaning cue expects a memory: nothing to hold the run to")
		} else {
			if s.HitK < t.MeaningHitMin {
				out = append(out, fmt.Sprintf("meaning cues: hit@%d %.3f is below %.2f", r.K, s.HitK, t.MeaningHitMin))
			}
			if r.Words != nil && t.MeaningOverWordsMin > 0 {
				gain := s.HitK - r.Words.ByType[CueMeaning].HitK
				if gain < t.MeaningOverWordsMin {
					out = append(out, fmt.Sprintf("meaning cues: hit@%d gain over words alone %.3f is below %.2f", r.K, gain, t.MeaningOverWordsMin))
				}
			}
		}
	}
	if t.ParaphraseHitMin > 0 {
		switch {
		case r.Paraphrase == nil:
			out = append(out, "no paraphrase cue (para_short, para_long) expects a row: nothing to hold the paraphrase bound to")
		case r.Paraphrase.HitK < t.ParaphraseHitMin:
			out = append(out, fmt.Sprintf("paraphrase cues: hit@%d %.3f is below %.2f", r.K, r.Paraphrase.HitK, t.ParaphraseHitMin))
		}
	}
	switch {
	case t.TrapByMeaningMax == nil:
	case r.Trap == nil:
		out = append(out, "no trap cue names a memory to avoid: nothing to hold the trap bound to")
	case r.Trap.AvoidedByMeaning > *t.TrapByMeaningMax:
		out = append(out, fmt.Sprintf("trap cues: the avoided memory matched by meaning %.3f is above %.2f", r.Trap.AvoidedByMeaning, *t.TrapByMeaningMax))
	}
	switch one, _ := r.row(); {
	case t.ControlByMeaningMax == nil:
	case r.Control == nil:
		out = append(out, "no control cue: nothing to hold the control bound to")
	case r.Control.OfferedByMeaning > *t.ControlByMeaningMax:
		out = append(out, fmt.Sprintf("control cues: a %s offered by meaning %.3f is above %.2f", one, r.Control.OfferedByMeaning, *t.ControlByMeaningMax))
	}
	return out
}

func (r *Report) Render(t Thresholds) string {
	var b strings.Builder
	fmt.Fprintf(&b, "memory-eval — corpus %q frozen %s, manifest %s\n", r.Corpus, r.Frozen, r.ManifestSHA256)
	basis := r.Basis
	if r.Words != nil {
		basis += fmt.Sprintf(" (floor %.3f)", r.Floor)
	}
	one, many := r.row()
	fmt.Fprintf(&b, "basis %s · policy %s · %d %s, %d cues, hit@%d judged\n", basis, r.Policy, r.Memories, many, r.Cues, r.K)
	line := func(name string, s TypeScore) {
		fmt.Fprintf(&b, "  %-16s n=%-3d hit@1 %.3f  hit@%d %.3f  MRR %.3f", name, s.N, s.Hit1, r.K, s.HitK, s.MRR)
		if s.Siblings > 0 {
			fmt.Fprintf(&b, "  a sibling first %.3f of %d", s.SiblingFirst, s.Siblings)
		}
		b.WriteByte('\n')
	}
	types := make([]string, 0, len(r.ByType))
	for typ := range r.ByType {
		types = append(types, typ)
	}
	sort.Strings(types)
	for _, typ := range types {
		line(typ, r.ByType[typ])
	}
	if r.Paraphrase != nil {
		line("paraphrase", *r.Paraphrase)
	}
	if r.Trap != nil {
		fmt.Fprintf(&b, "  traps: n=%d  the avoided memory ranked first %.3f, matched by meaning %.3f\n", r.Trap.N, r.Trap.AvoidedFirst, r.Trap.AvoidedByMeaning)
	}
	if r.Control != nil {
		fmt.Fprintf(&b, "  controls: n=%d  a %s offered by meaning %.3f", r.Control.N, one, r.Control.OfferedByMeaning)
		if len(r.Control.Offered) > 0 {
			fmt.Fprintf(&b, " (%s)", strings.Join(r.Control.Offered, " "))
		}
		b.WriteByte('\n')
	}
	if r.Words != nil {
		if s, ok := r.ByType[CueMeaning]; ok {
			w := r.Words.ByType[CueMeaning]
			fmt.Fprintf(&b, "  words alone: meaning cues hit@%d %.3f (gain %.3f)\n", r.K, w.HitK, s.HitK-w.HitK)
		}
		if r.Paraphrase != nil && r.Words.Paraphrase != nil {
			fmt.Fprintf(&b, "  words alone: paraphrase cues hit@%d %.3f (gain %.3f)\n", r.K, r.Words.Paraphrase.HitK, r.Paraphrase.HitK-r.Words.Paraphrase.HitK)
		}
	}
	if v := r.Violations(t); len(v) > 0 {
		for _, line := range v {
			fmt.Fprintf(&b, "  NOT MET: %s\n", line)
		}
	} else {
		var held []string
		if t.MeaningHitMin > 0 {
			held = append(held, fmt.Sprintf("meaning hit@%d at least %.2f", r.K, t.MeaningHitMin))
		}
		if t.MeaningOverWordsMin > 0 {
			held = append(held, fmt.Sprintf("gain over words at least %.2f", t.MeaningOverWordsMin))
		}
		if t.ParaphraseHitMin > 0 {
			held = append(held, fmt.Sprintf("paraphrase hit@%d at least %.2f", r.K, t.ParaphraseHitMin))
		}
		if t.TrapByMeaningMax != nil {
			held = append(held, fmt.Sprintf("traps matched by meaning at most %.2f", *t.TrapByMeaningMax))
		}
		if t.ControlByMeaningMax != nil {
			held = append(held, fmt.Sprintf("controls offered by meaning at most %.2f", *t.ControlByMeaningMax))
		}
		fmt.Fprintf(&b, "  thresholds met: %s\n", strings.Join(held, ", "))
	}
	var compared []string
	if t.CandidateParaphraseGainMin != nil {
		compared = append(compared, fmt.Sprintf("a paraphrase gain of at least %.2f", *t.CandidateParaphraseGainMin))
	}
	if t.CandidateOwnAndKeysLossMax != nil {
		compared = append(compared, fmt.Sprintf("own and keys cues each losing at most %.2f", *t.CandidateOwnAndKeysLossMax))
	}
	if len(compared) > 0 {
		fmt.Fprintf(&b, "  not held by one run: a candidate source is adopted over the shipped one for %s, a comparison of two runs\n", strings.Join(compared, ", with "))
	}
	if len(r.Misses) > 0 {
		fmt.Fprintf(&b, "  misses: %s\n", strings.Join(r.Misses, " "))
	}
	return b.String()
}
