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
package fuse

import "sort"

// .
// .
const K = 60.0

// .
type Key struct {
	Store string
	ID    string
}

// .
type Pool struct {
	Layer string
	Keys  []Key
}

// .
type Fused struct {
	Key      Key
	Score    float64
	Layers   []string
	BestRank int
}

// .
// .
// .
func RRF(k float64, pools ...Pool) []Fused {
	if k <= 0 {
		k = K
	}
	merged := map[Key]*Fused{}
	for _, p := range pools {
		seen := map[Key]bool{}
		for i, key := range p.Keys {
			if seen[key] {
				continue
			}
			seen[key] = true
			rank := i + 1
			f, ok := merged[key]
			if !ok {
				f = &Fused{Key: key, BestRank: rank}
				merged[key] = f
			}
			f.Score += 1 / (k + float64(rank))
			if rank < f.BestRank {
				f.BestRank = rank
			}
			f.Layers = addLayer(f.Layers, p.Layer)
		}
	}
	out := make([]Fused, 0, len(merged))
	for _, f := range merged {
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Key.Store != out[j].Key.Store {
			return out[i].Key.Store < out[j].Key.Store
		}
		return out[i].Key.ID < out[j].Key.ID
	})
	return out
}

func addLayer(layers []string, layer string) []string {
	for _, l := range layers {
		if l == layer {
			return layers
		}
	}
	layers = append(layers, layer)
	sort.Strings(layers)
	return layers
}
