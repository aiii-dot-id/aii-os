package tools

import "testing"

// The shapes here are taken from the field scan over 2010 production tool
// calls, not invented: identical restatements must pass (they were never
// ambiguous), drifted tails must be caught (they dispatch on a value
// nobody chose).
func TestConflictingArgKeys(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		// --- must NOT conflict: the call is unambiguous ---
		{"clean call", `{"file_path":"/a/b.go"}`, nil},
		{"no args", `{}`, nil},
		{"empty string", ``, nil},
		{"unparseable is the parse seam's business", `{"file_path":`, nil},
		{"not an object", `["a","a"]`, nil},
		{"identical restatement (8 of 21 in the field)",
			`{"file_path":"/a/b.go","file_path":"/a/b.go"}`, nil},
		{"identical restatement four times",
			`{"file_path":"/x.go","file_path":"/x.go","file_path":"/x.go","file_path":"/x.go"}`, nil},
		{"whitespace-only difference is not disagreement",
			`{"o":{"a":1},"o":{"a": 1}}`, nil},
		{"nested repetition is not a top-level conflict",
			`{"payload":{"k":1,"k":2}}`, nil},
		{"different keys, no repetition",
			`{"file_path":"/a","old_string":"/a"}`, nil},

		// --- must conflict: the executing value is not the stated one ---
		{"drifted tail: sibling filename",
			`{"file_path":"/d/sections_serve.go","file_path":"/d/sections_save.go"}`,
			[]string{"file_path"}},
		{"drifted tail: truncated path",
			`{"file_path":"/work/aiii/identity/aeon/x.py","file_path":"/work//identity/aeon/x.py"}`,
			[]string{"file_path"}},
		{"drifted tail: emptied value",
			`{"old_string":"// a real anchor","old_string":""}`,
			[]string{"old_string"}},
		{"drift after many identical copies",
			`{"p":"/a.go","p":"/a.go","p":"/a.go","p":"/rc.txt"}`,
			[]string{"p"}},
		{"two keys both drifted",
			`{"a":"1","b":"2","a":"9","b":"8"}`,
			[]string{"a", "b"}},
		{"reported once however many drifts",
			`{"a":"1","a":"2","a":"3","a":"4"}`,
			[]string{"a"}},
		{"type change counts as disagreement",
			`{"a":"1","a":1}`,
			[]string{"a"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ConflictingArgKeys(tc.raw)
			if len(got) != len(tc.want) {
				t.Fatalf("ConflictingArgKeys(%s) = %v, want %v", tc.raw, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("ConflictingArgKeys(%s) = %v, want %v", tc.raw, got, tc.want)
				}
			}
		})
	}
}

// The two functions answer different questions and the seam depends on the
// difference: telemetry counts every repetition, refusal fires only on
// disagreement. If these ever collapse into each other, 8 unambiguous
// calls in 21 start being refused for nothing.
func TestDuplicateIsWiderThanConflicting(t *testing.T) {
	identical := `{"file_path":"/a/b.go","file_path":"/a/b.go"}`
	if got := DuplicateArgKeys(identical); len(got) != 1 {
		t.Fatalf("DuplicateArgKeys should still count the repetition, got %v", got)
	}
	if got := ConflictingArgKeys(identical); len(got) != 0 {
		t.Fatalf("ConflictingArgKeys must stay silent on agreement, got %v", got)
	}

	drifted := `{"file_path":"/a/b.go","file_path":"/a/c.go"}`
	if got := DuplicateArgKeys(drifted); len(got) != 1 {
		t.Fatalf("DuplicateArgKeys should count the repetition, got %v", got)
	}
	if got := ConflictingArgKeys(drifted); len(got) != 1 {
		t.Fatalf("ConflictingArgKeys should catch the drift, got %v", got)
	}
}
