package broker

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/memory"
)

// meaningStub is a source that knows four ideas. Words for one idea share
// a dimension; every text also carries a trace of a last dimension, so no
// vector is empty and unrelated texts stay far under the floor.
type meaningStub struct {
	cues, memories int
}

var stubIdeas = map[string]int{
	"harbor": 0, "harbour": 0, "port": 0,
	"ship": 1, "vessel": 1,
	"invoice": 2, "bill": 2,
	"roof": 3,
}

func (s *meaningStub) Basis() (string, error) { return "stub/ideas-1", nil }
func (s *meaningStub) Floor() float64         { return 0.5 }
func (s *meaningStub) Embed(_ context.Context, kind memory.Kind, inputs []string) (string, [][]float32, error) {
	out := make([][]float32, 0, len(inputs))
	for _, text := range inputs {
		if kind == memory.KindCue {
			s.cues++
		} else {
			s.memories++
		}
		v := make([]float32, 8)
		v[7] = 0.01
		for _, w := range strings.Fields(strings.ToLower(text)) {
			if d, ok := stubIdeas[strings.Trim(w, ".,")]; ok {
				v[d] = 1
			}
		}
		var n float64
		for _, x := range v {
			n += float64(x) * float64(x)
		}
		for i := range v {
			v[i] = float32(float64(v[i]) / math.Sqrt(n))
		}
		out = append(out, v)
	}
	return "stub/ideas-1", out, nil
}

type meaningRecall struct {
	Hits []struct {
		ID         string  `json:"id"`
		Text       string  `json:"text"`
		Match      string  `json:"match"`
		Similarity float64 `json:"similarity"`
	} `json:"hits"`
	Meaning struct {
		Status string  `json:"status"`
		Detail string  `json:"detail"`
		Basis  string  `json:"basis"`
		Floor  float64 `json:"floor"`
	} `json:"meaning"`
}

func recallByMeaning(t *testing.T, b *Binding, args string) meaningRecall {
	t.Helper()
	m := dispatch(t, b, memParams("memory.recall", args))
	wantResult(t, m, statusSucceeded, "")
	var r meaningRecall
	if err := json.Unmarshal(m["operation_result"], &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestAPluginsRecallIsReadByMeaningWhenTheIdentityHasASource(t *testing.T) {
	src := &meaningStub{}
	h, b := memHost(t, Config{Meaning: src})
	harbor := remember(t, b, `{"text":"the ship reached the harbor at dusk"}`)
	remember(t, b, `{"text":"an invoice for the roof repair"}`)
	// Storing a memory compares letters and asks no source for anything.
	if src.cues != 0 || src.memories != 0 {
		t.Fatalf("storing two memories asked the source for %d cues and %d memories; storing asks it for nothing", src.cues, src.memories)
	}
	// The pass that keeps the vectors filled is the application's, over
	// this same record with this same source.
	rep, err := h.instruments.Backfill(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Embedded != 2 || src.memories != 2 {
		t.Fatalf("the pass embedded %d memories (%d asked of the source), want the plugin's 2: %s", rep.Embedded, src.memories, rep.Line())
	}

	// "port" shares no word and no near-word with either memory.
	got := recallByMeaning(t, b, `{"query":"port"}`)
	if src.cues != 1 {
		t.Fatalf("the recall asked the source for %d cues, want 1", src.cues)
	}
	if got.Meaning.Status != memory.StatusFound || got.Meaning.Basis != "stub/ideas-1" || got.Meaning.Floor != 0.5 {
		t.Fatalf("the layer's account is %+v, want found under the source's basis and floor", got.Meaning)
	}
	if len(got.Hits) != 1 || got.Hits[0].ID != harbor.ID || got.Hits[0].Match != memory.MatchMeaning || got.Hits[0].Similarity < 0.5 {
		t.Fatalf("the recall brought %+v, want the harbor memory alone, marked meaning, with its similarity", got.Hits)
	}

	// The exact override never consults the layer, for a plugin as for
	// the identity.
	exact := recallByMeaning(t, b, `{"query":"port","exact":true}`)
	if src.cues != 1 || len(exact.Hits) != 0 || exact.Meaning.Status != "" {
		t.Fatalf("an exact recall asked the source (%d cues) or answered by meaning: %+v", src.cues, exact)
	}
}

func TestAPluginsRecallSaysTheLayerIsOffWhenNothingMakesVectors(t *testing.T) {
	_, b := memHost(t, Config{})
	remember(t, b, `{"text":"the ship reached the harbor at dusk"}`)
	got := recallByMeaning(t, b, `{"query":"port"}`)
	if len(got.Hits) != 0 {
		t.Fatalf("with no source the recall brought %+v", got.Hits)
	}
	if got.Meaning.Status != memory.StatusSourceUnavailable || !strings.Contains(got.Meaning.Detail, "no source of vectors") {
		t.Fatalf("with no source the layer's account is %+v; it must say that nothing makes vectors", got.Meaning)
	}
}

// readingStub is meaningStub reading each text to its first window
// characters, and saying so, as a package of aii.embeddings@2 does.
type readingStub struct {
	meaningStub
	window int
}

func (s *readingStub) Tells() bool { return true }

func (s *readingStub) EmbedRead(ctx context.Context, kind memory.Kind, inputs []string) (string, [][]float32, []int, error) {
	heads, read := make([]string, len(inputs)), make([]int, len(inputs))
	for i, in := range inputs {
		r := []rune(in)
		read[i] = min(len(r), s.window)
		heads[i] = string(r[:read[i]])
	}
	basis, vectors, err := s.meaningStub.Embed(ctx, kind, heads)
	return basis, vectors, read, err
}

func (s *readingStub) Embed(ctx context.Context, kind memory.Kind, inputs []string) (string, [][]float32, error) {
	basis, vectors, _, err := s.EmbedRead(ctx, kind, inputs)
	return basis, vectors, err
}

// A PLUGIN'S RECALL SAYS HOW MUCH OF A TEXT MEANING READ, as the
// identity's does: each hit how many of its characters its vector was made
// from, and the layer how many of the cue's. Zero where the source does
// not say (the stub above).
func TestAPluginsRecallSaysHowMuchOfATextMeaningRead(t *testing.T) {
	src := &readingStub{window: 24}
	h, b := memHost(t, Config{Meaning: src})
	harbor := remember(t, b, `{"text":"the harbor at dusk, and a long account of the evening after it"}`)
	if _, err := h.instruments.Backfill(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	m := dispatch(t, b, memParams("memory.recall", `{"query":"port"}`))
	wantResult(t, m, statusSucceeded, "")
	var got struct {
		Hits []struct {
			ID          string `json:"id"`
			MeaningRead int    `json:"meaning_read"`
		} `json:"hits"`
		Meaning struct {
			CueChars int `json:"cue_chars"`
			CueRead  int `json:"cue_read"`
		} `json:"meaning"`
	}
	if err := json.Unmarshal(m["operation_result"], &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Hits) != 1 || got.Hits[0].ID != harbor.ID || got.Hits[0].MeaningRead != 24 || got.Meaning.CueChars != 4 || got.Meaning.CueRead != 4 {
		t.Fatalf("the recall must say the hit's vector was made from its first 24 characters and the cue read whole: %+v", got)
	}
}
