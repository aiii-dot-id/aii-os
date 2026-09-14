package jsonschema

import (
	"encoding/json"
	"errors"
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
func TestSchemaSubsetVectorsMatchTheKit(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "schema_subset.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Accept []map[string]interface{} `json:"accept"`
		Refuse []struct {
			Schema  map[string]interface{} `json:"schema"`
			Keyword string                 `json:"keyword"`
			Path    string                 `json:"path"`
		} `json:"refuse"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Accept) == 0 || len(v.Refuse) == 0 {
		t.Fatal("the vectors must carry both accepted and refused schemas")
	}
	for i, s := range v.Accept {
		if _, err := Compile(s); err != nil {
			t.Errorf("accept[%d]: unexpected refusal %v", i, err)
		}
	}
	for i, r := range v.Refuse {
		_, err := Compile(r.Schema)
		var ce *CompileError
		if !errors.As(err, &ce) || ce.Keyword != r.Keyword || ce.Path != r.Path {
			t.Errorf("refuse[%d]: want %s at %s, got %v", i, r.Keyword, r.Path, err)
		}
	}
}
