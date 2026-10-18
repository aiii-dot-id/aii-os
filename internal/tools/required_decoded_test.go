package tools

import (
	"context"
	"strings"
	"testing"
)

// A tool whose schema arrived from a packaged JSON file: "required" is
// []any, the shape encoding/json produces into a map.
type decodedSchemaTool struct{}

func (decodedSchemaTool) Name() string        { return "store" }
func (decodedSchemaTool) Description() string { return "a plugin op with a decoded schema" }
func (decodedSchemaTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"key": map[string]any{"type": "string"}},
		"required":   []any{"key"},
	}
}
func (decodedSchemaTool) Execute(context.Context, map[string]any) (Result, error) {
	return Result{Output: "proceeded"}, nil
}

// The required-argument pre-validation must fire for a packaged schema,
// not only for one constructed in Go.
func TestRequiredArgumentsValidateForADecodedSchema(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	r.Register(decodedSchemaTool{})
	res, err := r.Execute(context.Background(), "store", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Error, "missing required key") {
		t.Fatalf("a call missing a required argument proceeded past a decoded schema: %+v", res)
	}
	res, _ = r.Execute(context.Background(), "store", map[string]any{"key": "k"})
	if res.Error != "" {
		t.Fatalf("a complete call was refused: %+v", res)
	}
}
