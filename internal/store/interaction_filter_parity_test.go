package store

import (
	"sort"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

// THE RECORDED VIEW AND THE TRANSIENT VIEW FILTER ALIKE. The store
// filters in SQL; the process-local source filters with
// interaction.Matches. A plugin that asks both the same question must get
// the same answer, so each filter field is asked of the store and of
// Matches over the very rows the store holds.
func TestTheStoresFilterIsMatches(t *testing.T) {
	s, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.AddConversationTurn("operator", "an undated legacy turn"); err != nil {
		t.Fatal(err)
	}
	for _, in := range []InteractionInput{
		{ID: "m1", Kind: interaction.Message, Role: interaction.Operator, Content: "a", SessionID: "s1", TurnID: "t1", ProjectID: "p1"},
		{ID: "m2", Kind: interaction.Message, Role: interaction.Resident, Content: "b", SessionID: "s2", TurnID: "t1",
			Details: interaction.Details{WorkSession: "w1"}},
		{ID: "n1", Kind: interaction.Notice, Role: interaction.System, Content: "c", ProjectID: "p1",
			Details: interaction.Details{WorkSession: "w1"}},
	} {
		if _, err = s.RecordInteraction(t.Context(), in); err != nil {
			t.Fatalf("%s: %v", in.ID, err)
		}
	}
	// Tool rows come through their owner operations: a call, and the
	// result that names it.
	if err = s.RecordToolStart("t2", 1, "resident", "m", "pc1", "read", `{}`); err != nil {
		t.Fatal(err)
	}
	if err = recordToolDone(s, "t2", 1, "read", `{}`, "ok", interaction.Succeeded, false); err != nil {
		t.Fatal(err)
	}
	all := queryRows(t, s, interaction.Filter{})
	if len(all) != 6 {
		t.Fatalf("the fixture holds %d rows, want 6", len(all))
	}
	call := ""
	for _, r := range all {
		if r.Kind == interaction.ToolCall {
			call = r.ID
		}
	}
	if call == "" {
		t.Fatal("the fixture holds no tool call")
	}
	for name, f := range map[string]interaction.Filter{
		"kinds":           {Kinds: []interaction.Kind{interaction.Notice, interaction.ToolResult}},
		"role":            {Role: interaction.System},
		"turn":            {TurnID: "t1"},
		"tool turn":       {TurnID: "t2"},
		"session":         {SessionID: "s1"},
		"project":         {ProjectID: "p1"},
		"related":         {RelatedID: call},
		"work session":    {WorkSession: "w1"},
		"undated":         {Undated: true},
		"from long ago":   {From: "2000-01-01T00:00:00Z"},
		"until long ago":  {Until: "2000-01-01T00:00:00Z"},
		"two fields":      {SessionID: "s1", Kinds: []interaction.Kind{interaction.Message}},
		"nothing matches": {TurnID: "t9"},
	} {
		store := queryIDs(t, s, f)
		var matched []string
		for _, r := range all {
			if interaction.Matches(r, normalized(t, f)) {
				matched = append(matched, r.ID)
			}
		}
		sort.Strings(matched)
		if strings.Join(store, " ") != strings.Join(matched, " ") {
			t.Errorf("%s: the store answers [%s], Matches answers [%s]", name, strings.Join(store, " "), strings.Join(matched, " "))
		}
	}
}

func normalized(t *testing.T, f interaction.Filter) interaction.Filter {
	t.Helper()
	q := interaction.Query{Version: 1, Filter: f}
	if err := q.Normalize(); err != nil {
		t.Fatal(err)
	}
	return q.Filter
}

// queryIDs is the store's whole answer to f, as sorted IDs.
func queryIDs(t *testing.T, s *Store, f interaction.Filter) []string {
	t.Helper()
	var ids []string
	for _, r := range queryRows(t, s, f) {
		ids = append(ids, r.ID)
	}
	sort.Strings(ids)
	return ids
}

// queryRows pages the store's answer to f to its end.
func queryRows(t *testing.T, s *Store, f interaction.Filter) []interaction.Record {
	t.Helper()
	var rows []interaction.Record
	q := interaction.Query{Version: 1, Filter: f}
	for {
		page, err := s.QueryInteractions(t.Context(), q)
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, page.Rows...)
		if page.NextCursor == "" {
			return rows
		}
		q.Cursor = page.NextCursor
	}
}
