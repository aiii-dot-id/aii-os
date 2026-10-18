package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadValidatesVocabulary pins the seam the writers guard. Each
// case takes a good manifest, applies one hand-typo (the repair-path
// shape: one field wrong, the rest intact), and demands a refusal that
// names the field, what was found, and the valid values.
func TestLoadValidatesVocabulary(t *testing.T) {
	m := NewManager(t.TempDir())
	p, err := m.Create("vocab", "", "operator", nil, nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	manifest := filepath.Join(p.Dir, manifestName)

	good, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatalf("read good manifest: %v", err)
	}

	for _, tc := range []struct {
		name    string
		replace map[string]any
		wantErr string
	}{
		{
			name:    "state typo",
			replace: map[string]any{"state": "archieved"},
			wantErr: `state must be open, closed or archived (got "archieved")`,
		},
		{
			name:    "created_by typo",
			replace: map[string]any{"created_by": "Operator"},
			wantErr: `created_by must be operator or identity (got "Operator")`,
		},
		{
			name:    "empty name",
			replace: map[string]any{"name": "  "},
			wantErr: "a project needs a name",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if err := os.WriteFile(manifest, good, 0o644); err != nil {
					t.Fatalf("restore good manifest: %v", err)
				}
			}()

			var raw map[string]any
			if err := json.Unmarshal(good, &raw); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			for k, v := range tc.replace {
				raw[k] = v
			}
			b, err := json.Marshal(raw)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if err := os.WriteFile(manifest, b, 0o644); err != nil {
				t.Fatalf("write repaired manifest: %v", err)
			}

			_, err = m.Load(p.ID)
			if err == nil {
				t.Fatal("load accepted a hand-repaired typo — the inertness specimen itself")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("refusal must name field, value, and cure:\n got %q\nwant substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestLoadAcceptsLegacyHandmade pins the compatibility bound. The
// handmade shape (no created_by; a stray id nobody reads) predates the
// vocabulary and must keep loading — the validation refuses wrong
// values, never demands absent ones.
func TestLoadAcceptsLegacyHandmade(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "handmade")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	b := []byte(`{"id":"handmade","name":"Handmade","state":"open"}`)
	if err := os.WriteFile(filepath.Join(dir, manifestName), b, 0o644); err != nil {
		t.Fatalf("write handmade manifest: %v", err)
	}
	p, err := NewManager(root).Load("handmade")
	if err != nil {
		t.Fatalf("legacy handmade manifest must stay loadable: %v", err)
	}
	if p.Name != "Handmade" || p.State != "open" {
		t.Fatalf("loaded wrong values: %+v", p.Manifest)
	}
}
