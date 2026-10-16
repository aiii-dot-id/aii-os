package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/memory/memoryeval"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

type scoreFixture struct {
	Query  string   `json:"query"`
	Expect []string `json:"expect"`
}

func runMemoryScore(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("memory-score", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "path to the identity's database (required; opened read-only)")
	fixturesPath := fs.String("fixtures", "", "JSON file: [{\"query\": \"...\", \"expect\": [\"experiences/e1\", ...]}, ...]")
	k := fs.Int("k", memory.DefaultLimit, "hits per query to judge")
	policies := fs.String("policies", strings.Join(memory.Policies, ","), "decay policies to score, comma-separated")
	evalDir := fs.String("eval", "", "a frozen corpus directory (manifest.json, corpus.jsonl, cues.jsonl): score recall by meaning over a throwaway store instead of an identity's")
	basisPath := fs.String("basis", "", "with -eval: a recorded basis (vectors a model made, keyed by text); absent, words alone")
	origins := fs.Bool("origins", false, "with -db: count the beliefs by the origins their sources rest on, and nothing else (counts only)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: aii memory-score -db <path to aii.db> [-fixtures <file>] [-k 7] [-policies carrd,actr,none]")
		fmt.Fprintln(stderr, "       aii memory-score -eval <corpus dir> [-basis <vectors.jsonl>] [-policies carrd]")
		fmt.Fprintln(stderr, "       aii memory-score -db <path to aii.db> -origins")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Scores each decay policy: against fixtures (hit@k and mean reciprocal")
		fmt.Fprintln(stderr, "rank of the expected memories), and against the identity's own record")
		fmt.Fprintln(stderr, "(mean strength by how often a row was actually recalled). Read-only;")
		fmt.Fprintln(stderr, "reinforces nothing; the meaning layer is not consulted here.")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "With -eval: builds a throwaway store from the frozen corpus, fills its")
		fmt.Fprintln(stderr, "vectors from the recorded basis through the backfill, recalls every cue")
		fmt.Fprintln(stderr, "through the facility, and holds the result to the manifest's thresholds.")
		fmt.Fprintln(stderr, "Exit 3 when a threshold is not met; an incomplete run is refused, not scored.")
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *evalDir != "" {
		return runMemoryEval(*evalDir, *basisPath, strings.Split(*policies, ",")[0], stdout, stderr)
	}
	if *dbPath == "" {
		fs.Usage()
		return 2
	}
	var fixtures []scoreFixture
	if *fixturesPath != "" {
		raw, err := os.ReadFile(*fixturesPath)
		if err != nil {
			fmt.Fprintf(stderr, "fixtures: %v\n", err)
			return 1
		}
		if err := json.Unmarshal(raw, &fixtures); err != nil {
			fmt.Fprintf(stderr, "fixtures: %v\n", err)
			return 1
		}
	}
	st, err := store.OpenReadOnly(*dbPath)
	if err != nil {
		fmt.Fprintf(stderr, "open: %v\n", err)
		return 1
	}
	defer st.Close()
	if *origins {
		return runOriginCensus(st, stdout, stderr)
	}
	f := memory.New(st)
	now := time.Now()
	ctx := context.Background()
	fmt.Fprintf(stdout, "memory-score — %s, read-only, %s; words and fuzzy matches only (no provider here)\n", *dbPath, now.UTC().Format(time.RFC3339))
	for _, policy := range strings.Split(*policies, ",") {
		policy = strings.TrimSpace(policy)
		if policy == "" {
			continue
		}
		if len(fixtures) > 0 {
			hits, mrr, failures := scoreFixtures(ctx, f, fixtures, policy, *k)
			fmt.Fprintf(stdout, "\npolicy %s — fixtures %d: hit@%d %.0f%%, MRR %.3f\n", policy, len(fixtures), *k, 100*hits, mrr)
			for _, line := range failures {
				fmt.Fprintln(stdout, "  "+line)
			}
		}
		rows, err := f.Calibration(ctx, policy, now)
		if err != nil {
			fmt.Fprintf(stderr, "policy %s: calibration: %v\n", policy, err)
			return 1
		}
		fmt.Fprint(stdout, "\n"+memory.RenderCalibration(policy, rows))
	}
	return 0
}

func scoreFixtures(ctx context.Context, f *memory.Facility, fixtures []scoreFixture, policy string, k int) (hitRate, mrr float64, failures []string) {
	var hits int
	var rrSum float64
	for _, fx := range fixtures {
		res, err := f.Recall(ctx, memory.Query{Text: fx.Query, Limit: k, Decay: policy})
		if err != nil {
			failures = append(failures, fmt.Sprintf("%q: %v", fx.Query, err))
			continue
		}
		expected := map[string]bool{}
		for _, e := range fx.Expect {
			expected[e] = true
		}
		found := false
		for i, h := range res.Hits {
			if expected[h.Store+"/"+h.ID] {
				if !found {
					hits++
					rrSum += 1 / float64(i+1)
					found = true
				}
			}
		}
		if !found {
			var got []string
			for _, h := range res.Hits {
				got = append(got, h.Store+"/"+h.ID)
			}
			failures = append(failures, fmt.Sprintf("miss %q: expected %v, top %d were %v", fx.Query, fx.Expect, k, got))
		}
	}
	if len(fixtures) == 0 {
		return 0, 0, nil
	}
	return float64(hits) / float64(len(fixtures)), rrSum / float64(len(fixtures)), failures
}

func runMemoryEval(dir, basisPath, policy string, stdout, stderr io.Writer) int {
	m, corpus, cues, err := memoryeval.Load(dir)
	if err != nil {
		fmt.Fprintf(stderr, "eval: %v\n", err)
		return 1
	}
	o := memoryeval.Options{Policy: strings.TrimSpace(policy)}
	if basisPath != "" {
		basis, err := memoryeval.ReadBasis(basisPath)
		if err != nil {
			fmt.Fprintf(stderr, "eval: %v\n", err)
			return 1
		}
		o.Basis = basis
	}
	work, err := os.MkdirTemp("", "aii-memory-eval-")
	if err != nil {
		fmt.Fprintf(stderr, "eval: %v\n", err)
		return 1
	}
	defer os.RemoveAll(work)
	rep, err := memoryeval.Run(context.Background(), work, m, corpus, cues, o)
	if err != nil {
		fmt.Fprintf(stderr, "eval: %v\n", err)
		return 1
	}
	fmt.Fprint(stdout, rep.Render(m.Thresholds))
	if len(rep.Violations(m.Thresholds)) > 0 {
		return 3
	}
	return 0
}

func runOriginCensus(st *store.Store, stdout, stderr io.Writer) int {
	c, err := st.BeliefOriginCensus(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "origins: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "beliefs (live): %d, of which Ring 2: %d\n", c.Beliefs, c.Ring2)
	fmt.Fprintf(stdout, "with three or more distinct sources: %d\n", c.Confirmable)
	fmt.Fprintf(stdout, "  resting on 3 or more origins: %d\n", c.ByOrigins[3])
	fmt.Fprintf(stdout, "  resting on 2 origins: %d\n", c.ByOrigins[2])
	fmt.Fprintf(stdout, "  resting on 1 origin: %d\n", c.ByOrigins[1])
	fmt.Fprintf(stdout, "  resting on no origin (beliefs that rest only on each other): %d\n", c.ByOrigins[0])
	fmt.Fprintf(stdout, "Ring 2, with three or more sources, by origins (3+, 2, 1, 0): %d, %d, %d, %d\n",
		c.Ring2ByOrigins[3], c.Ring2ByOrigins[2], c.Ring2ByOrigins[1], c.Ring2ByOrigins[0])
	fmt.Fprintf(stdout, "Ring 2 holding fewer than three sources now: %d\n", c.Ring2UnderThreeSources)
	return 0
}
