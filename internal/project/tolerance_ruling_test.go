package project

import (
	"os"
	"path/filepath"
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
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

// .
// .
func TestManifestToleratesUnknownFields(t *testing.T) {
	m := NewManager(t.TempDir())

	// .
	// .
	// .
	dir := filepath.Join(m.root, "tolerated")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{
		"name": "Tolerated",
		"state": "open",
		"created_by": "operator",
		"creatd_by": "Operator",
		"id": "not-an-id-field",
		"future_field_v2": {"anything": true}
	}`)
	if err := os.WriteFile(filepath.Join(dir, manifestName), manifest, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := m.Load("tolerated")
	if err != nil {
		t.Fatalf("ruling violated: unknown fields must not block load: %v", err)
	}
	if got.Name != "Tolerated" {
		t.Errorf("known field Name did not bind: got %q", got.Name)
	}
	if got.State != "open" {
		t.Errorf("known field State did not bind: got %q", got.State)
	}
	if got.CreatedBy != "operator" {
		t.Errorf("known field CreatedBy did not bind: got %q", got.CreatedBy)
	}

	// .
	// .
	// .
	// .
	if got.ID != "tolerated" {
		t.Errorf("ID must be directory-derived; manifest id field is read by nobody: got %q", got.ID)
	}
}
