package tools

import (
	"context"
	"strings"
	"testing"
)

// .
// .
type decodedSchemaTool struct{}

func (decodedSchemaTool) Name() string        { return "store" }
func (decodedSchemaTool) Description() string { return "a plugin op with a decoded schema" }
func (decodedSchemaTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"key": map[string]interface{}{"type": "string"}},
		"required":   []interface{}{"key"},
	}
}
func (decodedSchemaTool) Execute(context.Context, map[string]interface{}) (Result, error) {
	return Result{Output: "proceeded"}, nil
}

// .
// .
func TestRequiredArgumentsValidateForADecodedSchema(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	r.Register(decodedSchemaTool{})
	res, err := r.Execute(context.Background(), "store", map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Error, "missing required key") {
		t.Fatalf("a call missing a required argument proceeded past a decoded schema: %+v", res)
	}
	res, _ = r.Execute(context.Background(), "store", map[string]interface{}{"key": "k"})
	if res.Error != "" {
		t.Fatalf("a complete call was refused: %+v", res)
	}
}
