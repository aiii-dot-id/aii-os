package tools

import (
	"context"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestPathArgumentsAreNormalisedOnceAndOnlyWhenStrings(t *testing.T) {
	r, sandbox := relRegistry(t)
	inside := filepath.Join(sandbox, "a.txt")
	// An escape is an absolute path outside the home on every system: on
	// Windows that takes the home's own volume, because a path that starts
	// at the root with no volume is not absolute there.
	escape := filepath.Join(filepath.VolumeName(sandbox)+string(filepath.Separator), "etc", "passwd")
	for _, tool := range []struct{ name, key string }{
		{"read", "file_path"}, {"write", "file_path"}, {"edit", "file_path"},
		{"grep", "path"}, {"ls", "path"},
	} {
		cases := []struct {
			label  string
			in     any
			want   any
			denied bool
		}{
			{"relative is rooted", "notes.txt", filepath.Join(sandbox, "notes.txt"), false},
			{"absolute inside is unchanged", inside, inside, false},
			{"non-string is unchanged", 42, 42, false},
			{"escape is denied", escape, escape, true},
		}
		if runtime.GOOS == "windows" {
			// A path from the root with no volume is the current volume's:
			// it is written back in its clean form, and outside the home it
			// is denied all the same.
			cases = append(cases, struct {
				label  string
				in     any
				want   any
				denied bool
			}{"a root with no volume is denied", "/etc/passwd", `\etc\passwd`, true})
		}
		for _, c := range cases {
			args := map[string]any{tool.key: c.in, "content": "x", "old_string": "a", "new_string": "b", "pattern": "x"}
			res, err := r.Execute(context.Background(), tool.name, args)
			if err != nil && !strings.Contains(err.Error(), "unknown tool") {
				t.Fatalf("%s %s: %v", tool.name, c.label, err)
			}
			if got := args[tool.key]; !reflect.DeepEqual(got, c.want) {
				t.Errorf("%s %s: argument after dispatch = %#v, want %#v", tool.name, c.label, got, c.want)
			}
			if denied := strings.HasPrefix(res.Error, "access denied"); denied != c.denied {
				t.Errorf("%s %s: denied=%v (%q), want %v", tool.name, c.label, denied, res.Error, c.denied)
			}
		}
		if tool.key == "path" {
			args := map[string]any{"path": "", "pattern": "x"}
			res, err := r.Execute(context.Background(), tool.name, args)
			if err != nil && !strings.Contains(err.Error(), "unknown tool") {
				t.Fatalf("%s empty: %v", tool.name, err)
			}
			if args["path"] != sandbox || strings.HasPrefix(res.Error, "access denied") {
				t.Errorf("%s: an empty path must be the home and not denied: %#v %q", tool.name, args["path"], res.Error)
			}
		}
	}
}
