package memory

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

func tableColumns(t *testing.T, s *store.Store, table string) map[string]bool {
	t.Helper()
	rows, err := s.DB().Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("table_info(%s): %v", table, err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var cid, notNull, pk int
		var name, ctype string
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		out[strings.ToLower(name)] = true
	}
	return out
}

func compiles(t *testing.T, s *store.Store, what, q string) {
	t.Helper()
	rows, err := s.DB().Query(q + " LIMIT 0")
	if err != nil {
		t.Errorf("%s does not compile: %v\n%s", what, err, q)
		return
	}
	rows.Close()
}

// .
// .
// .
// .
// .
// .
// .
// .
func TestEveryDescriptorMatchesTheSchema(t *testing.T) {
	s, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if len(Stores()) == 0 {
		t.Fatal("no descriptors")
	}
	seen := map[string]bool{}
	for _, d := range Stores() {
		if seen[d.Name] {
			t.Errorf("%s is described twice", d.Name)
		}
		seen[d.Name] = true
		if _, ok := store.Catalog[d.Name]; !ok {
			t.Errorf("%s: not a catalogued table", d.Name)
			continue
		}
		wantArgs := strings.Join(d.Text, ", ") + ","
		for _, sc := range []string{d.FTS, d.Tri} {
			e, ok := store.Sidecars[sc]
			if !ok {
				t.Errorf("%s: sidecar %s is not declared", d.Name, sc)
				continue
			}
			if e.Base != d.Name || e.Content != d.Content {
				t.Errorf("%s: sidecar %s is declared over %s/%s, descriptor says %s/%s", d.Name, sc, e.Base, e.Content, d.Name, d.Content)
			}
			if !strings.HasPrefix(e.Args, wantArgs) {
				t.Errorf("%s: sidecar %s indexes %q, descriptor names %v", d.Name, sc, e.Args, d.Text)
			}
		}
		if !strings.HasSuffix(d.FTS, "_fts") || !strings.HasSuffix(d.Tri, "_tri") {
			t.Errorf("%s: sidecars must be the _fts and _tri pair: %s %s", d.Name, d.FTS, d.Tri)
		}
		contentCols := tableColumns(t, s, d.Content)
		for _, c := range d.Text {
			if !contentCols[c] {
				t.Errorf("%s: text column %s is not on %s", d.Name, c, d.Content)
			}
		}
		baseCols := tableColumns(t, s, d.Name)
		if !baseCols["id"] {
			t.Errorf("%s: the base has no id column", d.Name)
		}
		compiles(t, s, d.Name+": the time expression", "SELECT "+d.Time+" FROM "+d.Name+" b")
		if d.Filter != "" {
			compiles(t, s, d.Name+": the filter", "SELECT 1 FROM "+d.Name+" b WHERE "+d.Filter)
		}
		if d.Current != "" {
			compiles(t, s, d.Name+": the currency predicate", "SELECT 1 FROM "+d.Name+" b WHERE "+d.Current)
		}
		if d.Seq != "" {
			if !baseCols[d.Seq] {
				t.Errorf("%s: sequence column %s is not on the base", d.Name, d.Seq)
			}
			compiles(t, s, d.Name+": the sequence order", "SELECT 1 FROM "+d.Name+" b WHERE b."+d.Seq+" < 5 ORDER BY b."+d.Seq+" DESC")
		}
		compiles(t, s, d.Name+": the read's column list", "SELECT "+selectList(d)+" FROM "+d.Name+" b")
		if (d.Ring == 0) == (d.RingColumn == "") {
			t.Errorf("%s: a ring is given exactly one way (Ring=%d RingColumn=%q)", d.Name, d.Ring, d.RingColumn)
		}
		if d.RingColumn != "" && !baseCols[d.RingColumn] {
			t.Errorf("%s: ring column %s is not on the base", d.Name, d.RingColumn)
		}
		if (d.Attribution == "") == (d.AttributionColumn == "") {
			t.Errorf("%s: an attribution is given exactly one way", d.Name)
		}
		if d.AttributionColumn != "" && !baseCols[d.AttributionColumn] {
			t.Errorf("%s: attribution column %s is not on the base", d.Name, d.AttributionColumn)
		}
		if _, ok := d.Class.Params(); !ok {
			t.Errorf("%s: class %q is not in the CARRD table", d.Name, d.Class)
		}
		if !d.Reinforce {
			t.Errorf("%s: every store is reinforced by conscious recall; a store that is not needs a ruling", d.Name)
		}
	}
	for name, e := range store.Sidecars {
		if _, ok := Lookup(e.Base); !ok {
			t.Errorf("sidecar %s indexes %s, which no descriptor describes", name, e.Base)
		}
	}
	if len(store.Sidecars) != 2*len(Stores()) {
		t.Errorf("%d sidecars for %d stores; every store has exactly two", len(store.Sidecars), len(Stores()))
	}
	// .
	// .
	if d, _ := Lookup("experiences"); !strings.Contains(d.Filter, "private = 0") {
		t.Errorf("experiences must filter private rows in the descriptor: %q", d.Filter)
	}
}

func TestLookup(t *testing.T) {
	if d, ok := Lookup("conversations"); !ok || d.Content != "conversations_searchable" {
		t.Fatalf("Lookup(conversations) = %+v, %v", d, ok)
	}
	if _, ok := Lookup("nothing"); ok {
		t.Fatal("Lookup found a store that does not exist")
	}
	// .
	Stores()[0].Name = "edited"
	if d, _ := Lookup("experiences"); d.Name != "experiences" {
		t.Fatal("Stores() exposed the descriptors themselves")
	}
}
