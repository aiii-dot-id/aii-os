package memory

import (
	"context"
	"database/sql"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/memory/fuse"
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

const (
	GraphDecay            = 0.7
	GraphHops             = 3
	GraphEdgesPerNode     = 20
	GraphCrossWeight      = 0.2
	GraphCentralityWeight = 0.1
	GraphCap              = 2.0
	// .
	// .
	graphNodeBound = 2000
)

// .
// .
// .
func graphBoost(ctx context.Context, db *sql.DB, fused []fuse.Fused) (map[fuse.Key]float64, error) {
	boosts := make(map[fuse.Key]float64, len(fused))
	for _, fz := range fused {
		boosts[fz.Key] = 1
	}
	if len(fused) == 0 {
		return boosts, nil
	}
	seeds := map[string]float64{}
	var best float64
	for _, fz := range fused {
		if fz.Score > best {
			best = fz.Score
		}
	}
	for _, fz := range fused {
		if best > 0 && fz.Score/best > seeds[fz.Key.ID] {
			seeds[fz.Key.ID] = fz.Score / best
		}
	}
	adjacency, err := walkEdges(ctx, db, seeds)
	if err != nil {
		return boosts, err
	}
	if len(adjacency) == 0 {
		return boosts, nil
	}
	// .
	// .
	cross := map[string]float64{}
	for seed, a := range seeds {
		if len(adjacency[seed]) == 0 {
			continue
		}
		dist := map[string]int{seed: 0}
		frontier := []string{seed}
		for hop := 1; hop <= GraphHops && len(frontier) > 0; hop++ {
			var next []string
			for _, n := range frontier {
				for _, m := range adjacency[n] {
					if _, seen := dist[m]; seen {
						continue
					}
					dist[m] = hop
					next = append(next, m)
				}
			}
			frontier = next
		}
		decay := 1.0
		for hop := 1; hop <= GraphHops; hop++ {
			decay *= GraphDecay
			for n, d := range dist {
				if d == hop {
					cross[n] += a * decay
				}
			}
		}
	}
	var maxDegree int
	for id := range seeds {
		if d := len(adjacency[id]); d > maxDegree {
			maxDegree = d
		}
	}
	for _, fz := range fused {
		id := fz.Key.ID
		centrality := 0.0
		if maxDegree > 0 {
			centrality = float64(len(adjacency[id])) / float64(maxDegree)
		}
		b := 1 + GraphCrossWeight*cross[id] + GraphCentralityWeight*centrality
		if b > GraphCap {
			b = GraphCap
		}
		boosts[fz.Key] = b
	}
	return boosts, nil
}

// .
// .
func walkEdges(ctx context.Context, db *sql.DB, seeds map[string]float64) (map[string][]string, error) {
	adjacency := map[string][]string{}
	visited := map[string]bool{}
	frontier := make([]string, 0, len(seeds))
	for id := range seeds {
		frontier = append(frontier, id)
		visited[id] = true
	}
	add := func(a, b string) {
		for _, held := range adjacency[a] {
			if held == b {
				return
			}
		}
		if len(adjacency[a]) < GraphEdgesPerNode {
			adjacency[a] = append(adjacency[a], b)
		}
	}
	for hop := 1; hop <= GraphHops && len(frontier) > 0 && len(visited) < graphNodeBound; hop++ {
		var next []string
		for start := 0; start < len(frontier); start += 100 {
			end := start + 100
			if end > len(frontier) {
				end = len(frontier)
			}
			batch := frontier[start:end]
			marks := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
			args := make([]any, 0, 2*len(batch))
			for _, id := range batch {
				args = append(args, id)
			}
			args = append(args, args...)
			rows, err := db.QueryContext(ctx, "SELECT from_id, to_id FROM edges WHERE archived = 0 AND (from_id IN ("+marks+") OR to_id IN ("+marks+")) ORDER BY created_seq DESC", args...)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var from, to string
				if err := rows.Scan(&from, &to); err != nil {
					rows.Close()
					return nil, err
				}
				add(from, to)
				add(to, from)
				for _, n := range []string{from, to} {
					if !visited[n] {
						visited[n] = true
						next = append(next, n)
					}
				}
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return nil, err
			}
			rows.Close()
		}
		frontier = next
	}
	return adjacency, nil
}
