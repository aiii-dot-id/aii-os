package memory

import (
	"context"
	"database/sql"
	"math"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/memory/fuse"
)

// .
// .
// .
// .
// .
func TestGraphBoostFollowsTheEdgesAmongTheHits(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	seedLedger(t, s, 6, now.Add(-48*time.Hour))
	addExperience(t, s, "e1", "the harbour bell rang at dusk", 1, now.Add(-24*time.Hour), false)
	addBelief(t, s, "b1", "the harbour bell marks the tide", 3, 2)
	addBelief(t, s, "b2", "the tide turns twice a day", 3, 3)
	addBelief(t, s, "b3", "a bell alone", 3, 4)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.DB().Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// .
	// .
	exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq) VALUES ('ed1', 'e1', 'b1', 'SUPPORTS', 5)`)
	exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq) VALUES ('ed2', 'b1', 'b2', 'CONTRADICTS', 6)`)
	exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq, archived) VALUES ('ed3', 'b3', 'b2', 'SUPPORTS', 6, 1)`)

	fused := []fuse.Fused{
		{Key: fuse.Key{Store: "beliefs", ID: "b1"}, Score: 1.0},
		{Key: fuse.Key{Store: "beliefs", ID: "b2"}, Score: 0.5},
		{Key: fuse.Key{Store: "beliefs", ID: "b3"}, Score: 0.5},
	}
	var boosts map[fuse.Key]float64
	if err := s.ReadWith(func(db *sql.DB) error {
		var err error
		boosts, err = graphBoost(context.Background(), db, fused)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// .
	// .
	// .
	want := map[string]float64{"b1": 1.17, "b2": 1.19, "b3": 1.0}
	for id, w := range want {
		got := boosts[fuse.Key{Store: "beliefs", ID: id}]
		if math.Abs(got-w) > 1e-9 {
			t.Errorf("boost %s = %v, want %v", id, got, w)
		}
	}
	// .
	for i := 0; i < 30; i++ {
		exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq) VALUES (?, ?, 'b3', 'SUPPORTS', 6)`, "edx"+string(rune('a'+i)), "x"+string(rune('a'+i)))
	}
	many := []fuse.Fused{{Key: fuse.Key{Store: "beliefs", ID: "b3"}, Score: 1}}
	for i := 0; i < 30; i++ {
		many = append(many, fuse.Fused{Key: fuse.Key{Store: "experiences", ID: "x" + string(rune('a'+i))}, Score: 1})
	}
	if err := s.ReadWith(func(db *sql.DB) error {
		var err error
		boosts, err = graphBoost(context.Background(), db, many)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if b := boosts[fuse.Key{Store: "beliefs", ID: "b3"}]; b != GraphCap {
		t.Fatalf("thirty neighbours must hit the cap, got %v", b)
	}
	// .
	if len(boosts) != len(many) {
		t.Fatalf("the walk added rows: %d boosts for %d hits", len(boosts), len(many))
	}
}

// .
// .
func TestRecallRanksConnectedMemoriesHigher(t *testing.T) {
	s := newStore(t)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	seedLedger(t, s, 6, now.Add(-48*time.Hour))
	addBelief(t, s, "b_alone", "the lantern is lit at dusk", 3, 2)
	addBelief(t, s, "b_linked", "the lantern is lit at dawn", 3, 3)
	addExperience(t, s, "e1", "I lit the lantern", 1, now.Add(-24*time.Hour), false)
	if _, err := s.DB().Exec(`INSERT INTO edges (id, from_id, to_id, edge_type, created_seq) VALUES ('ed1', 'e1', 'b_linked', 'SUPPORTS', 5)`); err != nil {
		t.Fatal(err)
	}
	f := New(s)
	res, err := f.Recall(context.Background(), Query{Text: "lantern", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Hit{}
	for _, h := range res.Hits {
		byID[h.ID] = h
	}
	if byID["b_linked"].Graph <= 1 || byID["b_alone"].Graph != 1 {
		t.Fatalf("graph factors: linked %v alone %v", byID["b_linked"].Graph, byID["b_alone"].Graph)
	}
	if byID["e1"].Graph <= 1 {
		t.Fatalf("the experience the belief cites is connected too: %v", byID["e1"].Graph)
	}
	var linkedRank, aloneRank int
	for i, h := range res.Hits {
		switch h.ID {
		case "b_linked":
			linkedRank = i
		case "b_alone":
			aloneRank = i
		}
	}
	if linkedRank > aloneRank {
		t.Fatalf("the connected belief must outrank its unconnected twin: %v", hitIDs(res.Hits))
	}
}
