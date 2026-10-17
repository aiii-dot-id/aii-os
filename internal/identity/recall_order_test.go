package identity

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// orderEmbedder makes the vector a test names for a text, and for every
// other text one that no cue here comes near, under the floor the test
// names.
type orderEmbedder struct {
	floor   float64
	vectors map[string][]float32
}

const orderDims = 202

func (orderEmbedder) Basis() (string, error) { return "order/v1", nil }
func (e orderEmbedder) Floor() float64       { return e.floor }

func (e orderEmbedder) Embed(_ context.Context, _ memory.Kind, inputs []string) (string, [][]float32, error) {
	out := make([][]float32, len(inputs))
	for i, in := range inputs {
		if v, ok := e.vectors[in]; ok {
			out[i] = v
			continue
		}
		v := make([]float32, orderDims)
		v[orderDims-1] = 1
		out[i] = v
	}
	return "order/v1", out, nil
}

func nearTheFloor(cue, memoryText string) map[string][]float32 {
	c, m := make([]float32, orderDims), make([]float32, orderDims)
	c[0] = 1
	m[0], m[1] = 40.4, 127
	return map[string][]float32{cue: c, memoryText: m}
}

func TestTheSimilarityAndTheFloorAreShownToThreeDecimals(t *testing.T) {
	e, _, _, _, _ := setupEngine(t)
	vectors := nearTheFloor("lantern", "the keeper trimmed the wick")
	// The rig's seeded origin is recalled like any note (it was once left
	// out of recall, as a note marked private). It gets an axis of its
	// own, so the cue that is near nothing stays near nothing: this
	// stand-in gives every text it does not know the same vector.
	origin := make([]float32, orderDims)
	origin[2] = 1
	vectors["a prior observed fact"] = origin
	e.instruments.SetEmbedder(orderEmbedder{floor: 0.30, vectors: vectors})
	mustVerb(t, e, "note", map[string]any{"content": "the keeper trimmed the wick"})
	if _, err := e.instruments.Backfill(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	out := recallOut(t, e, map[string]any{"query": "lantern"})
	if head := strings.SplitN(out, "\n", 2)[0]; !strings.Contains(head, "meaning layer consulted, floor 0.300") {
		t.Fatalf("the first line must carry the floor to three decimals: %s", head)
	}
	if exp := sectionOf(out, "Experiences:"); !strings.Contains(exp, "similarity 0.300") {
		t.Fatalf("the hit must carry the similarity it was compared on, to three decimals:\n%s", out)
	}
	if !strings.Contains(out, "Meaning layer: consulted (basis order/v1, floor 0.300)") {
		t.Fatalf("the last line must carry the floor to three decimals:\n%s", out)
	}
	nothing := recallOut(t, e, map[string]any{"query": "zeppelin"})
	if !strings.Contains(nothing, "(basis order/v1, floor 0.300); no memory scored") {
		t.Fatalf("a recall the layer found nothing for carries the floor to three decimals:\n%s", nothing)
	}
}

// A HIT FOUND BY WORDS AND BY MEANING SHOWS THE MEANING PART. The mark
// named its layers ("both (words+meaning)") and the similarity stood
// apart from them; it is now the meaning layer's own number, beside its
// name and in the layers' own names: "both (exact_words + meaning 0.945)".
// Two word layers carry no similarity, and none is invented for them.
func TestABothMarkShowsTheMeaningPart(t *testing.T) {
	e, _, _, _, _ := setupEngine(t)
	e.instruments.SetEmbedder(currencyEmbedder{})
	mustVerb(t, e, "note", map[string]any{"content": "the ferry left the harbor late"})
	if _, err := e.instruments.Backfill(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	both := sectionOf(recallOut(t, e, map[string]any{"query": "ferry harbor"}), "Experiences:")
	if !regexp.MustCompile(`, both \(exact_words( \+ fuzzy)? \+ meaning 0\.\d{3}\), `).MatchString(both) {
		t.Fatalf("found by its words and by meaning, the mark must carry the meaning part with its similarity:\n%s", both)
	}
	if strings.Contains(both, "similarity") {
		t.Fatalf("the similarity is the meaning part's, shown once, inside the mark:\n%s", both)
	}
	plain, _, _, _, _ := setupEngine(t)
	mustVerb(t, plain, "note", map[string]any{"content": "the kingfisher struck the water at dusk"})
	words := sectionOf(recallOut(t, plain, map[string]any{"query": "kingfisher"}), "Experiences:")
	if !strings.Contains(words, ", both (exact_words + fuzzy), ") {
		t.Fatalf("two word layers are named in the layers' own words, with no similarity:\n%s", words)
	}
}

// THE ORDER'S KEY IS SHOWN. Hits were "ranked by match, decayed by
// policy", and a line showed its strength and nothing of the rank it was
// decayed from, so a resident could not see why one hit stood above
// another. The first line now names the key — score = fused rank (RRF,
// k=60, over each store's words and meaning) × strength × graph — and
// every hit carries its score and the three factors, in the order the
// answer gives.
func TestEveryHitShowsTheScoreItIsOrderedBy(t *testing.T) {
	e, st, _, _, _ := setupEngine(t)
	e.instruments.SetEmbedder(currencyEmbedder{})
	for _, note := range []string{"the ferry left the harbor late", "a ferry waited at the port", "the boat reached the harbor"} {
		mustVerb(t, e, "note", map[string]any{"content": note})
	}
	seedBelief(t, st, 1, "a ferry keeps to its harbor")
	if _, err := e.instruments.Backfill(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	out := recallOut(t, e, map[string]any{"query": "ferry harbor"})
	head := strings.SplitN(out, "\n", 2)[0]
	if !strings.Contains(head, "ordered by score: fused rank (RRF k=60 over each store's words and meaning) × strength (decay policy carrd) × graph") {
		t.Fatalf("the first line must name the order's key: %s", head)
	}
	line := regexp.MustCompile(`\| rank (\d+), .*score (\d\.\d{5}) \(fused (\d\.\d{5}) × strength (\d\.\d\d) × graph (\d\.\d\d)\)`)
	scores := map[int]float64{}
	for _, ln := range strings.Split(out, "\n") {
		if !strings.Contains(ln, "| rank ") {
			continue
		}
		m := line.FindStringSubmatch(ln)
		if m == nil {
			t.Fatalf("a hit does not carry its score and its factors:\n%s", ln)
		}
		var f [4]float64
		for i := range f {
			f[i], _ = strconv.ParseFloat(m[i+2], 64)
		}
		if math.Abs(f[0]-f[1]*f[2]*f[3]) > 0.006*f[0]+1e-5 {
			t.Fatalf("the score is not its factors' product:\n%s", ln)
		}
		rank, _ := strconv.Atoi(m[1])
		scores[rank] = f[0]
	}
	if len(scores) < 4 {
		t.Fatalf("rig: %d hits, want the three notes and the belief:\n%s", len(scores), out)
	}
	for r := 2; r <= len(scores); r++ {
		if scores[r] > scores[r-1] {
			t.Fatalf("rank %d scores %.5f above rank %d's %.5f: the order is not the score shown:\n%s", r, scores[r], r-1, scores[r-1], out)
		}
	}
}

// THE CUT ACROSS SOURCES IS SAID AT THE TOP. Every source pools its own
// candidates, and after fusion the hits of all of them are sorted together
// and cut to the limit in all: a source can match twelve and show two
// because the others scored higher, not because it held two. The answer
// said "beliefs (12 matched, 2 shown)" in its last lines and nothing of
// why. It now says, under its first line, that the cut is shared and by
// score, with what each source matched and shows; and nothing where
// nothing was cut.
func TestAMixedAnswerSaysTheCutIsSharedByScore(t *testing.T) {
	e, st, _, _, _ := setupEngine(t)
	mustVerb(t, e, "note", map[string]any{"content": "we passed a lighthouse before noon"})
	places := []string{"cape", "reef", "inlet", "shoal", "bar", "spit", "sound", "bight", "strait", "narrows", "roads", "bank"}
	for i, p := range places {
		seedBelief(t, st, i+1, "the lighthouse on the "+p+" is manned")
	}
	out := recallOut(t, e, map[string]any{"query": "lighthouse", "limit": 3})
	second := strings.SplitN(out, "\n", 3)[1]
	if !strings.HasPrefix(second, "Cut ") || !strings.Contains(second, "ranked together by score") || !strings.Contains(second, "the best 3 kept in all, not 3 per source") {
		t.Fatalf("under the first line the answer must say the limit was shared across sources by score:\n%s", out)
	}
	for _, want := range []string{`beliefs 12 matched, \d shown`, `experiences 1 matched, \d shown`} {
		if !regexp.MustCompile(want).MatchString(second) {
			t.Fatalf("the cut line must carry each matching source's counts (%s):\n%s", want, second)
		}
	}
	if whole := recallOut(t, e, map[string]any{"query": "lighthouse", "limit": 50}); strings.Contains(whole, "\nCut ") {
		t.Fatalf("nothing was cut, and the answer must not say it was:\n%s", whole)
	}
}

// A RECALL ANSWER IS NEVER CUT FROM ITS FRONT. The conversation loop keeps
// the tail of a tool result over its bound (32,000 characters by default),
// and a recall renders beliefs first, so a long answer lost its first line
// and its beliefs before anything else. The verb now bounds its own answer
// below that: the lowest-scored hits are left out, the answer says how
// many, and only what it shows is reinforced.
func TestALongRecallLeavesOutItsLowestScoredHitsAndSaysSo(t *testing.T) {
	e, st, _, _, _ := setupEngine(t)
	for i := 1; i <= 60; i++ {
		seedBelief(t, st, i, fmt.Sprintf("the lighthouse number %02d stands", i)+strings.Repeat(" weatherproofed", 60))
	}
	out := recallOut(t, e, map[string]any{"query": "lighthouse", "limit": 50})
	if n := utf8.RuneCountInString(out); n > recallAnswerChars {
		t.Fatalf("the answer is %d characters, over its own bound of %d", n, recallAnswerChars)
	}
	if !strings.HasPrefix(out, `Recall "lighthouse"`) {
		t.Fatalf("the answer must keep its first line:\n%.300s", out)
	}
	m := regexp.MustCompile(`the (\d+) lowest-scored of the 50 were left out to keep this answer within (\d+) characters`).FindStringSubmatch(strings.SplitN(out, "\n", 3)[1])
	if m == nil {
		t.Fatalf("the answer must say, under its first line, how many hits it left out and why:\n%.600s", out)
	}
	left, _ := strconv.Atoi(m[1])
	shownLines := regexp.MustCompile(`\n  \[(b\d\d), [^\n]*\| rank (\d+),`).FindAllStringSubmatch(out, -1)
	if left == 0 || len(shownLines)+left != 50 {
		t.Fatalf("%d hits shown and %d left out, of the 50 the limit kept", len(shownLines), left)
	}
	shown := map[string]bool{}
	for i, s := range shownLines {
		if rank, _ := strconv.Atoi(s[2]); rank != i+1 {
			t.Fatalf("the hits shown must be the best-scored, ranks 1 to %d; line %d is rank %d", len(shownLines), i+1, rank)
		}
		shown[s[1]] = true
	}
	if !regexp.MustCompile(`PAGED and possibly incomplete: [^\n]*beliefs \(60 matched, ` + strconv.Itoa(len(shownLines)) + ` shown\)`).MatchString(out) {
		t.Fatalf("the coverage must count what the answer shows:\n%s", out[strings.Index(out, "Coverage"):])
	}
	for i := 1; i <= 60; i++ {
		id := fmt.Sprintf("b%02d", i)
		a, ok, err := st.MemoryAccessOf(store.MemoryRef{Store: "beliefs", ID: id})
		if err != nil {
			t.Fatal(err)
		}
		if reinforced := ok && a.Count > 0; reinforced != shown[id] {
			t.Fatalf("belief %s: shown %v, reinforced %v — a recall reinforces what it shows, and only that", id, shown[id], reinforced)
		}
	}
}

// A NAMED SOURCE SAYS WHETHER THE MEANING LAYER READ THE CUE. A source
// named with a cue is listed by its words, and the meaning layer is not
// asked; a ranked answer said so in its first line, and a listing said
// nothing, so exact=true with source=experiences read as a search that
// had also been made by meaning. Every answer to a cue now says, in its
// first line, whether the layer was consulted; and a listing that finds
// nothing says so too, since a match by meaning alone is not in it.
func TestANamedSourceSaysWhetherTheMeaningLayerWasConsulted(t *testing.T) {
	e, st, _, _, _ := setupEngine(t)
	e.instruments.SetEmbedder(currencyEmbedder{})
	mustVerb(t, e, "note", map[string]any{"content": "the ferry left the harbor late"})
	seedBelief(t, st, 1, "a ferry keeps to its harbor")
	if _, err := e.instruments.Backfill(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	head := func(out string) string { return strings.SplitN(out, "\n", 2)[0] }
	for _, args := range []map[string]any{
		{"query": "ferry", "source": "experiences", "exact": true},
		{"query": "ferry", "source": "experiences"},
		{"query": "ferry", "source": "beliefs", "exact": true},
	} {
		if h := head(recallOut(t, e, args)); !strings.Contains(h, "meaning layer not consulted") {
			t.Fatalf("%v: the first line must say the meaning layer was not consulted: %s", args, h)
		}
	}
	if h := head(recallOut(t, e, map[string]any{"query": "ferry", "source": "beliefs"})); !strings.Contains(h, "meaning layer consulted") {
		t.Fatalf("a ranked source read without exact is read by meaning too, and says so: %s", h)
	}
	none := recallOut(t, e, map[string]any{"query": "boat port", "source": "experiences"})
	if !strings.HasPrefix(none, "No word match") || !strings.Contains(none, "meaning layer not consulted") {
		t.Fatalf("a listing that finds nothing by words must say the meaning layer was not asked:\n%s", none)
	}
}

// A RECALL FITS THE BOUND IT IS GIVEN. The loop's bound on a tool result
// is the operator's setting (prompt.max_tool_result_chars), and a bound
// below the answer's own 30,000 kept only an answer's tail: its first
// line and its beliefs went first. The engine is told the bound, and the
// answer fits under it, leaving out its lowest-scored hits and saying so.
func TestARecallFitsTheToolResultBoundItIsGiven(t *testing.T) {
	_, st, lg, kp, dir := setupEngine(t)
	const bound = 12000
	e := NewEngine(st, &testEventWriter{ledger: lg, store: st, key: kp}, ring.NewManager(),
		discovererAdapter{tools.NewRegistry(dir, nil, tools.Timeouts{})}, Ports{ToolResultChars: bound})
	for i := 1; i <= 60; i++ {
		seedBelief(t, st, i, fmt.Sprintf("the lighthouse number %02d stands", i)+strings.Repeat(" weatherproofed", 60))
	}
	out := recallOut(t, e, map[string]any{"query": "lighthouse", "limit": 50})
	if n := utf8.RuneCountInString(out); n > bound {
		t.Fatalf("the answer is %d characters, over the loop's bound of %d: the loop would keep its tail and drop its first line", n, bound)
	}
	if !strings.HasPrefix(out, `Recall "lighthouse"`) || !strings.Contains(out, fmt.Sprintf("to keep this answer within %d characters", bound)) {
		t.Fatalf("the answer must keep its first line and say the bound it was fitted to:\n%.600s", out)
	}
}

// THE TOOL'S DESCRIPTION SAYS WHAT THE ANSWER SAYS. It told a resident that
// hits "are ranked by match and decayed by age and use", and nothing of the
// shared cut, the score on every hit or the meaning part of a mark. It
// carries RecallOrder, the sentence the shipped SKILLS.md carries too.
func TestTheRecallDescriptionSaysHowAnAnswerIsOrderedAndCut(t *testing.T) {
	var recall string
	for _, v := range verbRegistry {
		if v.Name == "recall" {
			recall = v.Description
		}
	}
	if !strings.Contains(recall, RecallOrder) {
		t.Fatalf("the recall verb's description does not carry how an answer is ordered and cut: %q", recall)
	}
	if strings.Contains(recall, "ranked by match") {
		t.Fatalf("the description still says hits are ranked by match: %q", recall)
	}
	for _, said := range []string{"ordered by score", "fused rank", "strength", "graph", "not per source", "cut answer says so"} {
		if !strings.Contains(RecallOrder, said) {
			t.Errorf("RecallOrder does not say %q: %q", said, RecallOrder)
		}
	}
}
