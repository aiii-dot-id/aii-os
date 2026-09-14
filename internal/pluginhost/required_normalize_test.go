package pluginhost

import (
	"strings"
	"testing"
)

// .
// .
// .
// .
// .
func TestNormalizeRequiredAcceptsOnlyArraysOfNonEmptyStrings(t *testing.T) {
	for _, c := range []struct {
		name    string
		raw     interface{}
		present bool
		want    []string
		refuse  string
	}{
		{"array of strings", []interface{}{"message", "id"}, true, []string{"message", "id"}, ""},
		{"already canonical", []string{"message"}, true, []string{"message"}, ""},
		{"declared empty", []interface{}{}, true, []string{}, ""},
		{"absent", nil, false, nil, ""},
		{"scalar", "message", true, nil, "is string"},
		{"null", nil, true, nil, "is <nil>"},
		{"object", map[string]interface{}{"message": true}, true, nil, "want an array"},
		{"numeric member", []interface{}{"message", 3.0}, true, nil, "[1] is float64"},
		{"mixed", []interface{}{1.0, "message"}, true, nil, "[0] is float64"},
		{"empty member", []interface{}{"message", ""}, true, nil, "[1] is empty"},
		{"empty canonical member", []string{""}, true, nil, "[0] is empty"},
	} {
		t.Run(c.name, func(t *testing.T) {
			schema := map[string]interface{}{"type": "object"}
			if c.present {
				schema["required"] = c.raw
			}
			err := normalizeRequired(schema)
			if c.refuse != "" {
				if err == nil || !strings.Contains(err.Error(), c.refuse) {
					t.Fatalf("want a refusal naming %q, got %v", c.refuse, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, present := schema["required"]
			if !c.present {
				if present {
					t.Fatal("an absent key must stay absent")
				}
				return
			}
			list, ok := got.([]string)
			if !ok {
				t.Fatalf("normalised shape is %T, want []string", got)
			}
			if len(list) != len(c.want) {
				t.Fatalf("got %v, want %v", list, c.want)
			}
			for i := range list {
				if list[i] != c.want[i] {
					t.Fatalf("got %v, want %v", list, c.want)
				}
			}
		})
	}
}
