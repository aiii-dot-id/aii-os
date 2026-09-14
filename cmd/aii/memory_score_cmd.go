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
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
// .
// .
// .

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
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: aii memory-score -db <path to aii.db> [-fixtures <file>] [-k 7] [-policies carrd,actr,none]")
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "Scores each decay policy: against fixtures (hit@k and mean reciprocal")
		fmt.Fprintln(stderr, "rank of the expected memories), and against the identity's own record")
		fmt.Fprintln(stderr, "(mean strength by how often a row was actually recalled). Read-only;")
		fmt.Fprintln(stderr, "reinforces nothing; the meaning layer is not consulted here.")
	}
	if err := fs.Parse(args); err != nil {
		return 2
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

// .
// .
// .
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
