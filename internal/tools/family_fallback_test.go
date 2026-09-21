package tools

import "testing"

// .
// .
// .
// .
// .
func TestUndottedOperationsGroupUnderTheirPlugin(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	registerPlugin(t, r, "com.example.memory", "evict", "get", "health", "recall", "recent", "search", "stats", "store", "update")
	registerPlugin(t, r, "id.aiii.voice", "speaker.enroll", "speaker.forget", "speech.session.open")
	b := r.Brief()
	got := map[string]int{}
	for _, f := range b.Families {
		got[f.Name] = f.Count
	}
	want := map[string]int{"memory": 9, "speaker": 2, "speech": 1}
	if len(got) != len(want) {
		t.Fatalf("families = %v, want %v", got, want)
	}
	for name, n := range want {
		if got[name] != n {
			t.Fatalf("families = %v, want %v", got, want)
		}
	}
	for _, f := range b.Families {
		if f.Name == "memory" && (len(f.Names) != MaxFamilyNames || f.Names[0] != "evict" || f.More != 9-MaxFamilyNames) {
			t.Fatalf("memory family names = %v (+%d)", f.Names, f.More)
		}
	}
	// .
	if _, err := r.Offer("recall"); err != nil {
		t.Fatalf("offer by the bare operation id: %v", err)
	}
}

func TestFamilyFallbackEdges(t *testing.T) {
	for _, tc := range []struct{ plugin, op, want string }{
		{"com.example.memory", "recall", "memory"},
		{"com.example.memory", "memory.recall", "memory"},
		{"id.aiii.voice", "speech.session.open", "speech"},
		{"solo", "recall", "solo"},
		{"", "recall", "recall"},
		{"trailing.", "recall", "trailing."},
	} {
		if got := familyOf(tc.plugin, tc.op); got != tc.want {
			t.Fatalf("familyOf(%q, %q) = %q, want %q", tc.plugin, tc.op, got, tc.want)
		}
	}
}
