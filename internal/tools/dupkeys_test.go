package tools

import "testing"

// TestDuplicateArgKeys pins the corrupted-emission fingerprint that every
// other seam is blind to: valid JSON whose argument object repeats a key,
// so encoding/json dispatches the call on the LAST copy. The live shape
// (forensics 2026-08-24) is the third case — the same key restated many
// times with a truncated splice in the tail.
func TestDuplicateArgKeys(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"clean single key", `{"file_path":"/tmp/a.go"}`, nil},
		{"clean multi key", `{"file_path":"/tmp/a.go","offset":10}`, nil},
		{"repeated key", `{"file_path":"/tmp/a.go","file_path":"/tmp/b.go"}`, []string{"file_path"}},
		{
			"live shape: many repeats, corrupted tail",
			`{"file_path":"/w/sections_serve.go","file_path":"/w/sections_serve.go","file_path":"/w/sections_s-model"}`,
			[]string{"file_path"},
		},
		{"two distinct keys repeated", `{"a":1,"b":2,"a":3,"b":4}`, []string{"a", "b"}},
		{"empty args", ``, nil},
		{"unparseable is not ours", `{"file_path":`, nil},
		{"non-object", `["file_path","file_path"]`, nil},
		// Negative control: the repetition must be at the TOP level. A
		// nested object legitimately carrying the same key name is not a
		// corrupted call, and flagging it would make the counter lie.
		{"nested same-name key is not a duplicate", `{"payload":{"file_path":"a"},"file_path":"b"}`, nil},
		{"repeat across nesting still caught", `{"file_path":{"x":1},"file_path":"b"}`, []string{"file_path"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DuplicateArgKeys(tc.raw)
			if len(got) != len(tc.want) {
				t.Fatalf("DuplicateArgKeys(%s) = %v, want %v", tc.raw, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("DuplicateArgKeys(%s) = %v, want %v", tc.raw, got, tc.want)
				}
			}
		})
	}
}

// TestDuplicateArgKeyCounter pins the counter surface the panel reads.
func TestDuplicateArgKeyCounter(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	if got := r.DuplicateArgKeyCount(); got != 0 {
		t.Fatalf("fresh registry count = %d, want 0", got)
	}
	r.CountDuplicateArgKeys()
	r.CountDuplicateArgKeys()
	if got := r.DuplicateArgKeyCount(); got != 2 {
		t.Fatalf("after two counts = %d, want 2", got)
	}
}
