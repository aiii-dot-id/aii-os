package interaction

import (
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
