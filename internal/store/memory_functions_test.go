package store

import (
	"math"
	"strings"
	"testing"
)

// .
// .
// .
func TestTheMemoryFunctionsAreReachableFromSQL(t *testing.T) {
	s := testStore(t)
	db := s.DB()

	var sim float64
	if err := db.QueryRow(`SELECT trigram_similarity('cat', 'cats')`).Scan(&sim); err != nil {
		t.Fatal(err)
	}
	if math.Abs(sim-0.5) > 1e-9 {
		t.Errorf("trigram_similarity('cat','cats') = %v, want 0.5", sim)
	}
	if err := db.QueryRow(`SELECT trigram_similarity('foo-bar', 'FOO BAR')`).Scan(&sim); err != nil {
		t.Fatal(err)
	}
	if math.Abs(sim-1) > 1e-9 {
		t.Errorf("trigram_similarity must fold case and punctuation like pg_trgm: %v", sim)
	}

	var strength float64
	if err := db.QueryRow(`SELECT carrd_strength('operational', 21.0, 0)`).Scan(&strength); err != nil {
		t.Fatal(err)
	}
	if math.Abs(strength-0.5) > 1e-9 {
		t.Errorf("carrd_strength at one half-life = %v, want 0.5", strength)
	}
	if err := db.QueryRow(`SELECT carrd_strength('ephemeral', 100000, 0)`).Scan(&strength); err != nil {
		t.Fatal(err)
	}
	if math.Abs(strength-0.02) > 1e-9 {
		t.Errorf("carrd_strength must hold the floor: %v", strength)
	}
	// .
	if err := db.QueryRow(`SELECT carrd_strength('core', 0, 3.0)`).Scan(&strength); err != nil {
		t.Fatal(err)
	}
	if math.Abs(strength-1) > 1e-9 {
		t.Errorf("carrd_strength at age 0 = %v, want 1", strength)
	}

	// .
	var nullable *float64
	if err := db.QueryRow(`SELECT carrd_strength(NULL, 1, 0)`).Scan(&nullable); err != nil {
		t.Fatal(err)
	}
	if nullable != nil {
		t.Errorf("carrd_strength(NULL, ...) = %v, want NULL", *nullable)
	}
	if err := db.QueryRow(`SELECT trigram_similarity('a', NULL)`).Scan(&nullable); err != nil {
		t.Fatal(err)
	}
	if nullable != nil {
		t.Errorf("trigram_similarity(..., NULL) = %v, want NULL", *nullable)
	}

	// .
	err := db.QueryRow(`SELECT carrd_strength('ring3', 1, 0)`).Scan(&strength)
	if err == nil || !strings.Contains(err.Error(), "ring3") {
		t.Errorf("an unknown class must fail the query by name: %v", err)
	}
}

// .
// .
func TestTrigramSimilarityRanksASidecarQuery(t *testing.T) {
	s := testStore(t)
	addPluginMemory(t, s, "m1", "the lighthouse keeper")
	addPluginMemory(t, s, "m2", "a lightweight housekeeper")
	addPluginMemory(t, s, "m3", "lighthouse")
	rows, err := s.DB().Query(`SELECT b.id FROM plugin_memories_tri f JOIN plugin_memories b ON b.rowid = f.rowid
		WHERE plugin_memories_tri MATCH ? ORDER BY trigram_similarity(b.text, ?) DESC`, "light", "lighthouse")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var order []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		order = append(order, id)
	}
	if strings.Join(order, " ") != "m3 m1 m2" {
		t.Fatalf("fuzzy order = %v, want the closest text first (m3 m1 m2)", order)
	}
}
