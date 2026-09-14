package tools

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
// .
// .
func TestPathArgumentsAreNormalisedOnceAndOnlyWhenStrings(t *testing.T) {
	r, sandbox := relRegistry(t)
	inside := filepath.Join(sandbox, "a.txt")
	for _, tool := range []struct{ name, key string }{
		{"read", "file_path"}, {"write", "file_path"}, {"edit", "file_path"},
		{"grep", "path"}, {"ls", "path"},
	} {
		for _, c := range []struct {
			label  string
			in     interface{}
			want   interface{}
			denied bool
		}{
			{"relative is rooted", "notes.txt", filepath.Join(sandbox, "notes.txt"), false},
			{"absolute inside is unchanged", inside, inside, false},
			{"non-string is unchanged", 42, 42, false},
			{"escape is denied", "/etc/passwd", "/etc/passwd", true},
		} {
			args := map[string]interface{}{tool.key: c.in, "content": "x", "old_string": "a", "new_string": "b", "pattern": "x"}
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
			args := map[string]interface{}{"path": "", "pattern": "x"}
			res, err := r.Execute(context.Background(), tool.name, args)
			if err != nil && !strings.Contains(err.Error(), "unknown tool") {
				t.Fatalf("%s empty: %v", tool.name, err)
			}
			if args["path"] != "" || strings.HasPrefix(res.Error, "access denied") {
				t.Errorf("%s: an empty path must be left alone and not denied: %#v %q", tool.name, args["path"], res.Error)
			}
		}
	}
}
