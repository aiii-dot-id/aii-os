package memoryeval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// memoryeval_test.go — the evaluator's own properties, each by name: a
// frozen corpus is scored through the facility's recall and the report
// is tied to its manifest; fixtures that moved are refused; a basis that
// cannot answer for every memory or cue is refused, not scored lower;
// the thresholds are read from the manifest and named when not met; and
// the corpus the tree ships is the one its manifest froze.

var corpus = []Memory{
	{ID: "m1", Lang: "en", Kind: "short", Text: "The vessel reached the harbour at dusk and the tide was turning."},
	{ID: "m2", Lang: "en", Kind: "short", Text: "A quiet day of routine checks on the backup schedule, nothing failed."},
	{ID: "m3", Lang: "en", Kind: "short", Text: "She was happy about the garden after the rain."},
	{ID: "m4", Lang: "de", Kind: "short", Text: "Der Zug kam zu spät und wir verpassten den Anschluss."},
}

var cues = []Cue{
	{ID: "c1", Lang: "en", Type: CueMeaning, Text: "when the ship made port as evening fell", Expect: []string{"m1"}},
	{ID: "c2", Lang: "en", Type: CueMeaning, Text: "that uneventful morning of looking over our safety copies", Expect: []string{"m2"}},
	{ID: "c3", Lang: "en", Type: CueTrap, Text: "the backup singers ran through their routine before the show, and nothing failed", Avoid: []string{"m2"}},
	{ID: "c4", Lang: "en", Type: "paraphrase_easy", Text: "the harbour at dusk", Expect: []string{"m1"}},
	{ID: "c5", Lang: "en", Type: CueMeaning, Text: "her happiness over the plants", Expect: []string{"m3"}},
	{ID: "c6", Lang: "en", Type: "cross_lingual", Text: "the train was late and we missed the connection", Expect: []string{"m4"}},
}

// concepts is the fake model: words that mean the same share a
// dimension; every other word gets one of its own, so two unrelated
// texts share nothing and the floor decides.
var concepts = map[string]int{
	"vessel": 0, "ship": 0, "harbour": 1, "port": 1, "dusk": 2, "evening": 2, "tide": 3,
	"routine": 4, "uneventful": 4, "backup": 5, "safety": 5, "copies": 5, "checks": 6, "looking": 6,
	"happy": 7, "happiness": 7, "garden": 8, "plants": 8,
	"zug": 9, "train": 9, "spät": 10, "late": 10, "anschluss": 11, "connection": 11, "verpassten": 12, "missed": 12,
}

func fakeVector(text string) []float32 {
	const dims = 64
	v := make([]float32, dims)
	for _, w := range strings.Fields(strings.ToLower(text)) {
		w = strings.Trim(w, ".,;:!?")
		if c, ok := concepts[w]; ok {
			v[c] += 1
			continue
		}
		h := sha256.Sum256([]byte(w))
		v[16+int(h[0])%(dims-16)] += 0.15
	}
	return v
}

// writeCorpus freezes corpus and cues in dir with the given thresholds
// and a basis file of fake vectors for every memory and cue, and
// returns the basis path.
func writeCorpus(t *testing.T, dir string, th Thresholds, memories []Memory, cs []Cue) string {
	t.Helper()
	write := func(name string, rows []any) string {
		var b strings.Builder
		for _, r := range rows {
			raw, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			b.Write(raw)
			b.WriteByte('\n')
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(b.String()))
		return hex.EncodeToString(sum[:])
	}
	var mrows, crows []any
	for _, m := range memories {
		mrows = append(mrows, m)
	}
	for _, c := range cs {
		crows = append(crows, c)
	}
	files := map[string]string{corpusFile: write(corpusFile, mrows), cuesFile: write(cuesFile, crows)}
	raw, err := json.Marshal(Manifest{Corpus: "test", Frozen: "2026-10-05", Files: files, Thresholds: th})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	var basis strings.Builder
	basis.WriteString(`{"basis":"fake/v1"}` + "\n")
	for _, text := range append(texts(memories), cueTexts(cs)...) {
		sum := sha256.Sum256([]byte(text))
		v, _ := json.Marshal(fakeVector(text))
		fmt.Fprintf(&basis, `{"sha256":%q,"v":%s}`+"\n", hex.EncodeToString(sum[:]), v)
	}
	path := filepath.Join(dir, "basis.jsonl")
	if err := os.WriteFile(path, []byte(basis.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func texts(ms []Memory) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.Text)
	}
	return out
}

func cueTexts(cs []Cue) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Text)
	}
	return out
}

var thresholds = Thresholds{K: 3, MeaningHitMin: 0.8, MeaningOverWordsMin: 0.25}

var beliefs = []Memory{
	{ID: "b1", Lang: "en", Kind: KindBelief, Text: "The vessel reached the harbour at dusk."},
	{ID: "b2", Lang: "en", Kind: KindBelief, Text: "The tide was turning at the harbour."},
	{ID: "b3", Lang: "en", Kind: KindBelief, Text: "She was happy about the garden after the rain."},
	{ID: "b4", Lang: "en", Kind: KindBelief, Text: "A quiet day of routine checks on the backup schedule, nothing failed."},
}

var beliefCues = []Cue{
	{ID: "p1", Lang: "en", Type: CueParaShort, Text: "when the ship made port as evening fell", Expect: []string{"b1"}, Avoid: []string{"b2"}},
	{ID: "p2", Lang: "en", Type: CueParaShort, Text: "turning tide", Expect: []string{"b1"}, Avoid: []string{"b2"}},
	{ID: "p3", Lang: "en", Type: CueParaLong, Text: "her happiness over the plants", Expect: []string{"b3"}},
	{ID: "o1", Lang: "en", Type: "own", Text: "the vessel reached the harbour", Expect: []string{"b1"}, Avoid: []string{"b2"}},
	{ID: "k1", Lang: "en", Type: "keys", Text: "routine backup", Expect: []string{"b4"}},
	{ID: "x1", Lang: "en", Type: CueControl, Text: "happiness over plants"},
	{ID: "x2", Lang: "en", Type: CueControl, Text: "copper prices rose sharply"},
}

var (
	half, gain, loss = 0.5, 0.08, 0.03
	beliefThresholds = Thresholds{K: 3, ParaphraseHitMin: 0.6, ControlByMeaningMax: &half,
		CandidateParaphraseGainMin: &gain, CandidateOwnAndKeysLossMax: &loss}
)

func TestAFrozenCorpusIsScoredThroughTheFacilitysRecall(t *testing.T) {
	dir := t.TempDir()
	basisPath := writeCorpus(t, dir, thresholds, corpus, cues)
	m, ms, cs, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	basis, err := ReadBasis(basisPath)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Run(context.Background(), filepath.Join(dir, "run"), m, ms, cs, Options{Basis: basis})
	if err != nil {
		t.Fatal(err)
	}
	if rep.ManifestSHA256 != m.SHA256 || len(m.SHA256) != 64 {
		t.Fatalf("the report is not tied to the manifest: %q vs %q", rep.ManifestSHA256, m.SHA256)
	}
	if rep.Basis != "fake/v1" || rep.Policy != memory.DecayDefault || rep.K != 3 {
		t.Fatalf("report header: %+v", rep)
	}
	meaning := rep.ByType[CueMeaning]
	if meaning.N != 3 || meaning.HitK != 1 || meaning.Hit1 != 1 {
		t.Fatalf("meaning cues through the basis: %+v (misses %v)", meaning, rep.Misses)
	}
	if rep.ByType["cross_lingual"].HitK != 1 || rep.ByType["paraphrase_easy"].HitK != 1 {
		t.Fatalf("by type: %+v", rep.ByType)
	}
	if rep.Words == nil || rep.Words.ByType[CueMeaning].HitK != 0 || rep.Words.Basis != "words only" {
		t.Fatalf("the words-alone run must find no meaning cue and be carried: %+v", rep.Words)
	}
	if rep.Trap == nil || rep.Trap.N != 1 || rep.Trap.AvoidedFirst != 1 {
		t.Fatalf("the trap shares its words with m2, which words rank first: %+v", rep.Trap)
	}
	if v := rep.Violations(m.Thresholds); len(v) != 0 {
		t.Fatalf("thresholds met, yet: %v", v)
	}
	text := rep.Render(m.Thresholds)
	for _, want := range []string{"manifest " + m.SHA256, "basis fake/v1", "meaning          n=3", "words alone: meaning cues hit@3 0.000 (gain 1.000)", "thresholds met", "traps: n=1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the rendering lacks %q:\n%s", want, text)
		}
	}
	// Words alone, held to the same thresholds, is named for what it is.
	if v := rep.Words.Violations(m.Thresholds); len(v) != 1 || !strings.Contains(v[0], "hit@3 0.000 is below 0.80") {
		t.Fatalf("words alone must fail the meaning threshold by name: %v", v)
	}
}

// A corpus of beliefs is planted in the beliefs store and recalled from
// it, and scored by cue type: a sibling that comes in place of the
// belief asked for is counted as a sibling and is a miss, a control the
// meaning layer offers a belief for is counted, the paraphrase types
// pool into the figure the manifest holds, and the candidate figures are
// reported and held to nothing.
func TestABeliefCorpusIsRecalledFromTheBeliefsAndScoredByCueType(t *testing.T) {
	dir := t.TempDir()
	basisPath := writeCorpus(t, dir, beliefThresholds, beliefs, beliefCues)
	m, ms, cs, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Store != "beliefs" {
		t.Fatalf("a corpus whose rows are beliefs is recalled from the beliefs, got %q", m.Store)
	}
	basis, err := ReadBasis(basisPath)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Run(context.Background(), filepath.Join(dir, "run"), m, ms, cs, Options{Basis: basis})
	if err != nil {
		t.Fatal(err)
	}
	if rep.ManifestSHA256 != m.SHA256 || len(m.SHA256) != 64 || rep.Store != "beliefs" || rep.Memories != 4 {
		t.Fatalf("report header: %+v", rep)
	}
	for typ, want := range map[string]TypeScore{
		CueParaShort: {N: 2, Hit1: 0.5, HitK: 0.5, MRR: 0.5, Siblings: 2, SiblingFirst: 0.5},
		CueParaLong:  {N: 1, Hit1: 1, HitK: 1, MRR: 1},
		"own":        {N: 1, Hit1: 1, HitK: 1, MRR: 1, Siblings: 1},
		"keys":       {N: 1, Hit1: 1, HitK: 1, MRR: 1},
	} {
		if got := rep.ByType[typ]; got != want {
			t.Fatalf("%s: %+v, want %+v (misses %v)", typ, got, want, rep.Misses)
		}
	}
	if _, ok := rep.ByType[CueControl]; ok {
		t.Fatalf("a control expects nothing and has no hit figure: %+v", rep.ByType)
	}
	if p := rep.Paraphrase; p == nil || p.N != 3 || p.HitK != 2.0/3 || p.Siblings != 2 || p.SiblingFirst != 0.5 {
		t.Fatalf("the paraphrase types pooled: %+v", p)
	}
	if strings.Join(rep.Misses, " ") != "p2" {
		t.Fatalf("the sibling that came in place of b1 is a miss, never a hit: %v", rep.Misses)
	}
	if c := rep.Control; c == nil || c.N != 2 || c.OfferedByMeaning != 0.5 || strings.Join(c.Offered, " ") != "x1" {
		t.Fatalf("controls: %+v", c)
	}
	if c := rep.Words.Control; c == nil || c.OfferedByMeaning != 0 {
		t.Fatalf("words alone offers nothing by meaning: %+v", c)
	}
	if v := rep.Violations(m.Thresholds); len(v) != 0 {
		t.Fatalf("thresholds met, yet: %v", v)
	}
	text := rep.Render(m.Thresholds)
	for _, want := range []string{"manifest " + m.SHA256, "4 beliefs, 7 cues, hit@3 judged",
		"a sibling first 0.500 of 2", "controls: n=2  a belief offered by meaning 0.500 (x1)",
		"thresholds met: paraphrase hit@3 at least 0.60, controls offered by meaning at most 0.50",
		"not held by one run: a candidate source is adopted over the shipped one for a paraphrase gain of at least 0.08, with own and keys cues each losing at most 0.03",
		"misses: p2"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the rendering lacks %q:\n%s", want, text)
		}
	}
	// The thresholds decide: the same run under a higher paraphrase bound
	// and a lower control bound fails both, by name.
	tighter, quarter := m.Thresholds, 0.25
	tighter.ParaphraseHitMin, tighter.ControlByMeaningMax = 0.7, &quarter
	v := rep.Violations(tighter)
	if len(v) != 2 || v[0] != "paraphrase cues: hit@3 0.667 is below 0.70" || v[1] != "control cues: a belief offered by meaning 0.500 is above 0.25" {
		t.Fatalf("the tighter bounds: %v", v)
	}
}

func TestABasisCarriesItsModelsFloor(t *testing.T) {
	dir := t.TempDir()
	basisPath := writeCorpus(t, dir, thresholds, corpus, cues)
	m, ms, cs, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	basis, err := ReadBasis(basisPath)
	if err != nil {
		t.Fatal(err)
	}
	if basis.Floor() != memory.ProviderFloor {
		t.Fatalf("a basis that names no floor is held to the provider's %.2f, got %.2f", memory.ProviderFloor, basis.Floor())
	}
	withFloor := func(floor string) string {
		t.Helper()
		raw, err := os.ReadFile(basisPath)
		if err != nil {
			t.Fatal(err)
		}
		_, rest, _ := strings.Cut(string(raw), "\n")
		path := filepath.Join(dir, "basis-"+floor+".jsonl")
		if err := os.WriteFile(path, []byte(`{"basis":"fake/v1","floor":`+floor+`}`+"\n"+rest), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	// The fake's true matches are exact (cosine 1), so a floor of 1 still
	// passes them and the report is the same run, read under that floor.
	basis, err = ReadBasis(withFloor("1"))
	if err != nil || basis.Floor() != 1 {
		t.Fatalf("a named floor is the basis's: %v %v", basis, err)
	}
	rep, err := Run(context.Background(), filepath.Join(dir, "run"), m, ms, cs, Options{Basis: basis})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Floor != 1 || rep.Words.Floor != 0 {
		t.Fatalf("the report carries the floor it read under, and none for words alone: %v %v", rep.Floor, rep.Words.Floor)
	}

	if text := rep.Render(m.Thresholds); !strings.Contains(text, "basis fake/v1 (floor 1.000) · policy") {
		t.Fatalf("the rendering does not say the floor to three decimals:\n%s", text)
	}
	if text := rep.Words.Render(m.Thresholds); strings.Contains(text, "floor") {
		t.Fatalf("words alone read under no floor, yet its rendering names one:\n%s", text)
	}
	for _, bad := range []string{"1.5", "-0.1"} {
		if _, err := ReadBasis(withFloor(bad)); err == nil || !strings.Contains(err.Error(), "is not a cosine") {
			t.Fatalf("a floor of %s must be refused with the file: %v", bad, err)
		}
	}
}

func TestFixturesThatMovedAreRefused(t *testing.T) {
	for _, moved := range []struct {
		name, file, row string
		memories        []Memory
		cues            []Cue
		th              Thresholds
	}{
		{"a cue added to a memory corpus", cuesFile, `{"id":"c9","lang":"en","type":"meaning","text":"an easier cue","expect":["m1"]}`, corpus, cues, thresholds},
		{"a belief added to a belief corpus", corpusFile, `{"id":"b9","lang":"en","kind":"belief","text":"an easier belief"}`, beliefs, beliefCues, beliefThresholds},
	} {
		t.Run(moved.name, func(t *testing.T) {
			dir := t.TempDir()
			writeCorpus(t, dir, moved.th, moved.memories, moved.cues)
			f, err := os.OpenFile(filepath.Join(dir, moved.file), os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.WriteString(moved.row + "\n"); err != nil {
				t.Fatal(err)
			}
			f.Close()
			if _, _, _, err := Load(dir); !errors.Is(err, ErrFrozenMismatch) {
				t.Fatalf("a row added after the freeze must refuse the load, got %v", err)
			}
		})
	}
}

func TestABasisMissingAnyVectorIsRefusedNotScored(t *testing.T) {
	for name, c := range map[string]struct {
		memories []Memory
		cues     []Cue
		th       Thresholds
		// The file is the basis line, then the rows, then the cues.
		drop func([]string) []string
	}{
		"a memory's":  {corpus, cues, thresholds, func(l []string) []string { return append(l[:1:1], l[2:]...) }},
		"a cue's":     {corpus, cues, thresholds, func(l []string) []string { return l[:len(l)-1] }},
		"a belief's":  {beliefs, beliefCues, beliefThresholds, func(l []string) []string { return append(l[:1:1], l[2:]...) }},
		"a control's": {beliefs, beliefCues, beliefThresholds, func(l []string) []string { return l[:len(l)-1] }},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			basisPath := writeCorpus(t, dir, c.th, c.memories, c.cues)
			raw, err := os.ReadFile(basisPath)
			if err != nil {
				t.Fatal(err)
			}
			lines := c.drop(strings.Split(strings.TrimSpace(string(raw)), "\n"))
			if err := os.WriteFile(basisPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			m, ms, cs, err := Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			basis, err := ReadBasis(basisPath)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Run(context.Background(), filepath.Join(dir, "run"), m, ms, cs, Options{Basis: basis})
			if !errors.Is(err, ErrIncomplete) {
				t.Fatalf("one vector short must refuse the run, got %v", err)
			}
		})
	}
}

func TestTheManifestIsHeldToItsShape(t *testing.T) {
	dir := t.TempDir()
	writeCorpus(t, dir, Thresholds{K: 0}, corpus, cues)
	if _, _, _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("a manifest without thresholds must be refused, got %v", err)
	}
	dir = t.TempDir()
	bound := 0.05
	writeCorpus(t, dir, Thresholds{K: 7, ControlByMeaningMax: &bound}, beliefs, beliefCues)
	if _, _, _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "a hit figure") {
		t.Fatalf("a manifest that holds no hit figure must be refused, got %v", err)
	}
	dir = t.TempDir()
	writeCorpus(t, dir, beliefThresholds, append(append([]Memory{}, beliefs...), corpus[0]), beliefCues)
	if _, _, _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "4 of 5 rows are beliefs") {
		t.Fatalf("a corpus of beliefs and memories must be refused, got %v", err)
	}
	dir = t.TempDir()
	writeCorpus(t, dir, thresholds, corpus, []Cue{{ID: "c1", Type: CueMeaning, Text: "x", Expect: []string{"m9"}}})
	if _, _, _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "m9") {
		t.Fatalf("a cue naming a memory the corpus lacks must be refused by name, got %v", err)
	}
}

func TestATrapBoundIsHeldWhenTheManifestStatesOne(t *testing.T) {
	var th Thresholds
	if err := json.Unmarshal([]byte(`{"k":7,"meaning_hit_at_k_min":0.8,"trap_avoided_by_meaning_max":0.05}`), &th); err != nil {
		t.Fatal(err)
	}
	if th.TrapByMeaningMax == nil || *th.TrapByMeaningMax != 0.05 {
		t.Fatalf("the bound as the manifest states it: %+v", th)
	}
	rep := &Report{K: 7, ByType: map[string]TypeScore{CueMeaning: {N: 10, HitK: 0.9}}, Trap: &TrapScore{N: 20, AvoidedByMeaning: 0.25}}
	if v := rep.Violations(th); len(v) != 1 || !strings.Contains(v[0], "matched by meaning 0.250 is above 0.05") {
		t.Fatalf("a quarter of the traps matched by meaning must fail the bound by name: %v", v)
	}
	if text := rep.Render(th); !strings.Contains(text, "NOT MET: trap cues") {
		t.Fatalf("the rendering must say so:\n%s", text)
	}
	rep.Trap.AvoidedByMeaning = 0.05
	if v := rep.Violations(th); len(v) != 0 {
		t.Fatalf("at the bound is within it: %v", v)
	}
	if text := rep.Render(th); !strings.Contains(text, "traps matched by meaning at most 0.05") {
		t.Fatalf("a bound that is met is named with the others:\n%s", text)
	}
	rep.Trap = nil
	if v := rep.Violations(th); len(v) != 1 || !strings.Contains(v[0], "nothing to hold the trap bound to") {
		t.Fatalf("a bound over a corpus with no trap is not met by silence: %v", v)
	}
	// A manifest that states no bound holds a run to none.
	th.TrapByMeaningMax = nil
	rep.Trap = &TrapScore{N: 20, AvoidedByMeaning: 1}
	if v := rep.Violations(th); len(v) != 0 {
		t.Fatalf("no bound stated, yet: %v", v)
	}
}

// "Matched by meaning" is the meaning layer's finding and nothing else. A
// hit's Match says "both" for ANY two layers, so a trap that both word
// layers find says "both" in a run with no meaning layer at all; the
// figure reads the hit's layers.
func TestOnlyTheMeaningLayerMatchesByMeaning(t *testing.T) {
	dir := t.TempDir()
	trap := []Cue{{ID: "t1", Lang: "en", Type: CueTrap, Text: "routine backup", Avoid: []string{"m2"}},
		{ID: "c1", Lang: "en", Type: CueMeaning, Text: "the harbour at dusk", Expect: []string{"m1"}}}
	writeCorpus(t, dir, thresholds, corpus, trap)
	m, ms, cs, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Run(context.Background(), filepath.Join(dir, "run"), m, ms, cs, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Trap == nil || rep.Trap.N != 1 || rep.Trap.AvoidedFirst != 1 {
		t.Fatalf("both word layers find m2 for the trap: %+v", rep.Trap)
	}
	if rep.Trap.AvoidedByMeaning != 0 {
		t.Fatalf("words alone matched the trap's memory by meaning %.3f of the time", rep.Trap.AvoidedByMeaning)
	}
}

// A belief corpus's bounds, as its manifest states them: the paraphrase
// figure and the control figure are held, each at its bound within it;
// the candidate figures are read, rendered, and never held.
func TestTheBeliefBoundsAreHeldAsTheManifestStatesThem(t *testing.T) {
	var th Thresholds
	if err := json.Unmarshal([]byte(`{"k":7,"paraphrase_hit_at_k_min":0.52,"control_offered_by_meaning_max":0.05,"candidate_paraphrase_gain_min":0.08,"candidate_own_and_keys_loss_max":0.03}`), &th); err != nil {
		t.Fatal(err)
	}
	if th.K != 7 || th.ParaphraseHitMin != 0.52 || th.ControlByMeaningMax == nil || *th.ControlByMeaningMax != 0.05 ||
		th.CandidateParaphraseGainMin == nil || *th.CandidateParaphraseGainMin != 0.08 || th.CandidateOwnAndKeysLossMax == nil || *th.CandidateOwnAndKeysLossMax != 0.03 {
		t.Fatalf("the bounds as the manifest states them: %+v", th)
	}
	// One control of twenty, as Run computes it, is at the bound.
	rep := &Report{K: 7, Store: "beliefs", ByType: map[string]TypeScore{"own": {N: 78, HitK: 1}},
		Paraphrase: &TypeScore{N: 156, HitK: 0.52}, Control: &ControlScore{N: 20, OfferedByMeaning: float64(1) / float64(20), Offered: []string{"q319"}}}
	if v := rep.Violations(th); len(v) != 0 {
		t.Fatalf("at both bounds is within them, and no meaning cue is asked of a corpus that holds none: %v", v)
	}
	text := rep.Render(th)
	for _, want := range []string{"thresholds met: paraphrase hit@7 at least 0.52, controls offered by meaning at most 0.05", "not held by one run", "(q319)"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the rendering lacks %q:\n%s", want, text)
		}
	}
	rep.Paraphrase.HitK = 0.51
	rep.Control.OfferedByMeaning = 0.10
	if v := rep.Violations(th); len(v) != 2 || v[0] != "paraphrase cues: hit@7 0.510 is below 0.52" || v[1] != "control cues: a belief offered by meaning 0.100 is above 0.05" {
		t.Fatalf("both bounds missed, by name: %v", v)
	}
	if text := rep.Render(th); !strings.Contains(text, "NOT MET: paraphrase cues") || !strings.Contains(text, "NOT MET: control cues") {
		t.Fatalf("the rendering must say so:\n%s", text)
	}
	rep.Paraphrase, rep.Control = nil, nil
	if v := rep.Violations(th); len(v) != 2 || !strings.Contains(v[0], "nothing to hold the paraphrase bound to") || !strings.Contains(v[1], "nothing to hold the control bound to") {
		t.Fatalf("bounds over a corpus with no such cue are not met by silence: %v", v)
	}
	if v := (&Report{}).Violations(Thresholds{K: 7}); len(v) != 1 || !strings.Contains(v[0], "no hit figure") {
		t.Fatalf("thresholds that hold no hit figure hold nothing, and say so: %v", v)
	}
}

// The belief set is planted as replay plants a record (Build): no origin,
// nothing between its beliefs. Through live admission instead, each
// belief citing one origin of its own, 121 of its 150 would be admitted
// and 29 refused as near-duplicates of a belief before them: all nine
// twins and all twenty revisions of the five chains. Its README states
// that as a property of the set; this holds the README to the store's
// admission as it stands.
func TestLiveAdmissionWouldRefuseTheBeliefSetsTwinsAndRevisions(t *testing.T) {
	_, rows, _, err := Load(filepath.Join("testdata", "recall-beliefs-2026-10-08"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.New(filepath.Join(t.TempDir(), "admission.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	seq := 0
	materialize := func(typ ledger.EventType, payload []byte) {
		t.Helper()
		seq++
		sum := sha256.Sum256(payload)
		if err := st.Materialize(&ledger.Event{Seq: uint64(seq), Timestamp: fmt.Sprintf("2026-01-01T00:%02d:%02dZ", seq/60%60, seq%60),
			Type: typ, Ring: 3, Content: hex.EncodeToString(sum[:]), Payload: payload}); err != nil {
			t.Fatal(err)
		}
	}
	var refused []string
	for _, r := range rows {
		origin := "origin_" + r.ID
		raw, err := json.Marshal(map[string]any{"id": origin, "content": "the origin of " + r.ID, "category": "observation", "provenance": "self"})
		if err != nil {
			t.Fatal(err)
		}
		materialize(ledger.EventExperienceCreate, raw)
		if raw, err = json.Marshal(map[string]any{"id": r.ID, "statement": r.Text, "ring": 3,
			"origin_refs": []map[string]string{{"kind": "experience", "id": origin}}}); err != nil {
			t.Fatal(err)
		}
		if err := st.ValidateEvent(ledger.EventBeliefUpsert, 3, raw); err != nil {
			if !strings.Contains(err.Error(), "may already express this claim") {
				t.Fatalf("%s is refused for another reason than a near-duplicate: %v", r.ID, err)
			}
			refused = append(refused, r.ID)
			continue
		}
		materialize(ledger.EventBeliefUpsert, raw)
	}
	want := "b019 b021 b023 b042 b044 b046 b056 b058 b060 " + // the twins
		"b131 b132 b133 b134 b135 b136 b137 b138 b139 b140 b141 b142 b143 b144 b145 b146 b147 b148 b149 b150" // the revisions
	if len(rows)-len(refused) != 121 || strings.Join(refused, " ") != want {
		t.Fatalf("admitted %d of %d; refused %v", len(rows)-len(refused), len(rows), refused)
	}
}

// The corpora the tree ships are the ones their manifests froze, each with
// the thresholds written before its first run. Scoring them is the
// command's work, not a test's: a run over the first's 150 memories takes
// most of a minute under the race detector, and the second holds 5,150.
func TestTheShippedCorporaAreTheOnesTheirManifestsFroze(t *testing.T) {
	for _, want := range []struct {
		dir, store     string
		memories, cues int
		thresholds     string
		// manifest, where named, pins the sanitized public fixture
		// byte for byte; corpus hashes and thresholds remain frozen.
		manifest string
	}{
		{"recall-2026-10-05", "experiences", 150, 180, `{"k":7,"meaning_hit_at_k_min":0.8,"meaning_hit_at_k_over_words_min":0.25,"trap_avoided_by_meaning_max":0.05}`, ""},
		{"recall-2026-10-07", "experiences", 5150, 370, `{"k":7,"meaning_hit_at_k_min":0.52,"meaning_hit_at_k_over_words_min":0.06}`, ""},
		{"recall-beliefs-2026-10-08", "beliefs", 150, 332, `{"k":7,"paraphrase_hit_at_k_min":0.52,"control_offered_by_meaning_max":0.05,"candidate_paraphrase_gain_min":0.08,"candidate_own_and_keys_loss_max":0.03}`,
			"4a9bb1bd98b2bf2921c5ae27039723cd9801386469f9915e5146edc5cbb49182"},
	} {
		m, ms, cs, err := Load(filepath.Join("testdata", want.dir))
		if err != nil {
			t.Fatalf("%s: %v", want.dir, err)
		}
		th, err := json.Marshal(m.Thresholds)
		if err != nil {
			t.Fatal(err)
		}
		if m.Corpus != want.dir || m.Store != want.store || len(ms) != want.memories || len(cs) != want.cues || string(th) != want.thresholds {
			t.Fatalf("%s is not as frozen: named %q, store %s, %d rows, %d cues, %s", want.dir, m.Corpus, m.Store, len(ms), len(cs), th)
		}
		if want.manifest != "" && m.SHA256 != want.manifest {
			t.Fatalf("%s: the manifest is not the exact sanitized fixture: %s", want.dir, m.SHA256)
		}
	}
}

// Build projects unsigned events, so it writes only into a store it
// creates: an empty file, and a store an earlier build left, are both
// refused and left as they were.
func TestBuildRefusesAPathThatExists(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.db")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(empty, corpus); !errors.Is(err, ErrStoreExists) {
		t.Fatalf("an existing empty file must be refused, got %v", err)
	}
	if info, err := os.Stat(empty); err != nil || info.Size() != 0 {
		t.Fatalf("the refused path was written: %v %v", info, err)
	}
	built := filepath.Join(dir, "built.db")
	st, err := Build(built, corpus)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(built)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Build(built, corpus); !errors.Is(err, ErrStoreExists) {
		t.Fatalf("a store that exists must be refused, got %v", err)
	}
	if after, err := os.Stat(built); err != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("the refused store was touched: %v -> %v (%v)", before, after, err)
	}
}
