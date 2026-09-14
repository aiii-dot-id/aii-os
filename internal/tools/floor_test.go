package tools

import (
	"context"
	"os"
	"path/filepath"
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
// .
// .
func TestSubstrateFloorCoversOperatorCredentials(t *testing.T) {
	dir := t.TempDir()
	secret := "sk-operator-secret-value"
	for _, name := range []string{"config.json", "providers.json"} {
		if err := os.WriteFile(filepath.Join(dir, name),
			[]byte(`{"api_key":"`+secret+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// .
	// .
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

	// .
	// .
	// .
	res, err := r.Execute(ctx, "grep", map[string]interface{}{"pattern": "sk-operator", "path": "."})
	out := res.Text()
	if err != nil {
		out = err.Error()
	}
	if strings.Contains(out, secret) || strings.Contains(out, "providers.json") || strings.Contains(out, "config.json") {
		t.Errorf("recursive grep read protected files:\n%s", out)
	}

	// .
	res, err = r.Execute(ctx, "read", map[string]interface{}{"file_path": "notes.md"})
	if err != nil || !strings.Contains(res.Text(), "hello") {
		t.Errorf("ordinary files must still be readable: %v %q", err, res.Text())
	}
}
