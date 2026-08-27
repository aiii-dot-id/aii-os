package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE OPERATOR-CREDENTIAL BOUNDARY. config.json and providers.json sit in
// the identity's own directory and hold the operator's API keys. The
// substrate floor must refuse them by every route the identity has.
//
// The hole this pins: the policy pattern was "config/", written when
// configuration lived in a config/ SUBDIRECTORY. Going entirely-local put
// config.json at the root, where "config/" no longer matches — and
// providers.json, which is where the keys moved, was never in the list at
// all.
func TestSubstrateFloorCoversOperatorCredentials(t *testing.T) {
	dir := t.TempDir()
	secret := "sk-operator-secret-value"
	for _, name := range []string{"config.json", "providers.json"} {
		if err := os.WriteFile(filepath.Join(dir, name),
			[]byte(`{"api_key":"`+secret+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A file the identity SHOULD be able to read, so we prove the floor is
	// a floor and not a blanket refusal.
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(dir, nil, Timeouts{})
	ctx := context.Background()

	for _, name := range []string{"config.json", "providers.json"} {
		res, err := r.Execute(ctx, "read", map[string]interface{}{"file_path": name})
		out := res.Text()
		if err != nil {
			out = err.Error()
		}
		if strings.Contains(out, secret) {
			t.Errorf("read %s handed the operator's credential to the identity", name)
		}
	}

	// The same content must not leak through a recursive search either:
	// refusing `read` while `grep` walks and opens every file is not a
	// boundary, it is a speed bump.
	res, err := r.Execute(ctx, "grep", map[string]interface{}{"pattern": "sk-operator", "path": "."})
	out := res.Text()
	if err != nil {
		out = err.Error()
	}
	if strings.Contains(out, secret) || strings.Contains(out, "providers.json") || strings.Contains(out, "config.json") {
		t.Errorf("recursive grep read protected files:\n%s", out)
	}

	// And the floor must not have become a wall.
	res, err = r.Execute(ctx, "read", map[string]interface{}{"file_path": "notes.md"})
	if err != nil || !strings.Contains(res.Text(), "hello") {
		t.Errorf("ordinary files must still be readable: %v %q", err, res.Text())
	}
}
