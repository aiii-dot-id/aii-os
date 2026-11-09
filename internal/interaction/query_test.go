package interaction

import (
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestQueryNormalizationDoesNotMutateSharedInput(t *testing.T) {
	q := Query{Version: 1, Filter: Filter{Kinds: []Kind{ToolResult, Message}}}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			copy := q
			if err := copy.Normalize(); err != nil {
				t.Error(err)
			}
			if copy.Filter.Kinds[0] != Message {
				t.Error("normalization did not sort the private copy")
			}
		}()
	}
	wg.Wait()
	if q.Filter.Kinds[0] != ToolResult {
		t.Fatal("reader mutated the caller's query")
	}
	q.Filter.ProjectID = string([]byte{0xff})
	if err := q.Normalize(); err == nil {
		t.Fatal("malformed reference would be silently rewritten during JSON encoding")
	}
}

func TestDescendantFilterRequiresRecordedRoot(t *testing.T) {
	for _, q := range []Query{
		{Version: 1, Filter: Filter{IncludeDescendants: true}},
		{Version: 1, Source: "transient", Filter: Filter{WorkSession: "root", IncludeDescendants: true}},
	} {
		if err := q.Normalize(); err == nil {
			t.Fatalf("unrooted ancestry accepted: %+v", q)
		}
	}
	q := Query{Version: 1, Filter: Filter{WorkSession: "root", IncludeDescendants: true}}
	if err := q.Normalize(); err != nil {
		t.Fatal(err)
	}
	if _, err := MemoryPage(q, "source", nil, 0); err == nil {
		t.Fatal("memory rows invented ancestry")
	}
}

// The host's outcome filter keeps only the records whose outcome it names,
// from the transient source as from the recorded one; none named keeps all.
func TestTheOutcomeFilterKeepsOnlyTheOutcomesItNames(t *testing.T) {
	rows := []Record{
		{ID: "a", Sequence: 1, Kind: ToolResult, Role: System, Outcome: Succeeded},
		{ID: "b", Sequence: 2, Kind: ToolResult, Role: System, Outcome: Failed},
		{ID: "c", Sequence: 3, Kind: Notice, Role: System, Outcome: Refused},
	}
	ids := func(outcomes ...Outcome) string {
		t.Helper()
		p, err := MemoryPage(Query{Version: 1, Source: "transient", Limit: 10, Outcomes: outcomes}, "inc", append([]Record(nil), rows...), 0)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, r := range p.Rows {
			got = append(got, r.ID)
		}
		slices.Sort(got)
		return strings.Join(got, ",")
	}
	if got := ids(Failed, Refused); got != "b,c" {
		t.Fatalf("failed and refused kept %q; want b,c", got)
	}
	if got := ids(); got != "a,b,c" {
		t.Fatalf("no outcome named kept %q; want every record", got)
	}
}
