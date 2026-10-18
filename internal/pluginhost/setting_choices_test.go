package pluginhost

import (
	"fmt"
	"strings"
	"testing"
)

// A string setting may name the operation that looks up its choices; no
// other type may, and the name must be an operation name.
func TestChoicesFromBelongsToAStringAndNamesAnOperation(t *testing.T) {
	ok := `[{"key":"model","type":"string","title":"Model","choices_from":"models"}]`
	decls, err := ParseSettings([]byte(ok))
	if err != nil || decls[0].ChoicesFrom != "models" {
		t.Fatalf("a string setting names its choices' operation: %v %+v", err, decls)
	}
	for _, bad := range []string{
		`[{"key":"model","type":"enum","title":"Model","values":["a"],"choices_from":"models"}]`,
		`[{"key":"model","type":"secret","title":"Model","choices_from":"models"}]`,
		`[{"key":"model","type":"string","title":"Model","choices_from":"Not An Op"}]`,
	} {
		if _, err := ParseSettings([]byte(bad)); err == nil {
			t.Fatalf("refused: %s", bad)
		}
	}
}

// The answer is held to an enum's bounds and refused whole, never trimmed.
func TestSettingChoicesAreHeldToTheirBounds(t *testing.T) {
	got, err := ParseSettingChoices([]byte(`{"choices":[{"value":"jev-latest","label":"Jev (latest)"},{"value":"jev-preview"}]}`))
	if err != nil || len(got) != 2 || got[0].Label != "Jev (latest)" || got[1].Value != "jev-preview" {
		t.Fatalf("choices read whole: %v %+v", err, got)
	}
	if got, err := ParseSettingChoices([]byte(`{"choices":[]}`)); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("an empty list is an answer: %v %+v", err, got)
	}
	var many []string
	for i := 0; i <= MaxSettingEnumValues; i++ {
		many = append(many, fmt.Sprintf(`{"value":"m%d"}`, i))
	}
	for name, bad := range map[string]string{
		"not json":   `models: a, b`,
		"too many":   `{"choices":[` + strings.Join(many, ",") + `]}`,
		"empty":      `{"choices":[{"value":""}]}`,
		"twice":      `{"choices":[{"value":"a"},{"value":"a"}]}`,
		"long label": `{"choices":[{"value":"a","label":"` + strings.Repeat("x", MaxSettingLabelBytes+1) + `"}]}`,
		"long value": `{"choices":[{"value":"` + strings.Repeat("x", MaxSettingStringBytes+1) + `"}]}`,
	} {
		if _, err := ParseSettingChoices([]byte(bad)); err == nil {
			t.Fatalf("%s: refused", name)
		}
	}
}
