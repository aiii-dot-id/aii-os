package jsonschema

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func decode(t *testing.T, s string) interface{} {
	t.Helper()
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func compileText(t *testing.T, s string) *Schema {
	t.Helper()
	m, ok := decode(t, s).(map[string]interface{})
	if !ok {
		t.Fatal("schema fixture is not an object")
	}
	c, err := Compile(m)
	if err != nil {
		t.Fatalf("compile %s: %v", s, err)
	}
	return c
}

// .
// .
func TestTheSubsetValidatesAndNamesThePath(t *testing.T) {
	s := compileText(t, `{"type":"object","properties":{
		"id":{"type":"integer","minimum":1},
		"name":{"type":"string","minLength":1,"maxLength":8,"pattern":"^[a-z]+$"},
		"tags":{"type":"array","items":{"type":"string"},"maxItems":2,"uniqueItems":true},
		"mode":{"enum":["fast","slow"]},
		"ratio":{"type":"number","exclusiveMaximum":1,"multipleOf":0.25},
		"kind":{"const":"thing"},
		"maybe":{"type":["string","null"]}
	},"required":["id"],"additionalProperties":false}`)
	for _, tc := range []struct{ value, wantPath string }{
		{`{"id":3}`, ""},
		{`{"id":3,"name":"abc","tags":["a","b"],"mode":"fast","ratio":0.5,"kind":"thing","maybe":null}`, ""},
		{`{}`, "$.id"},
		{`{"id":0}`, "$.id"},
		{`{"id":1.5}`, "$.id"},
		{`{"id":1,"name":""}`, "$.name"},
		{`{"id":1,"name":"ABC"}`, "$.name"},
		{`{"id":1,"tags":["a","b","c"]}`, "$.tags"},
		{`{"id":1,"tags":["a","a"]}`, "$.tags[1]"},
		{`{"id":1,"tags":[1]}`, "$.tags[0]"},
		{`{"id":1,"mode":"medium"}`, "$.mode"},
		{`{"id":1,"ratio":1}`, "$.ratio"},
		{`{"id":1,"ratio":0.3}`, "$.ratio"},
		{`{"id":1,"kind":"other"}`, "$.kind"},
		{`{"id":1,"maybe":2}`, "$.maybe"},
		{`{"id":1,"extra":true}`, "$.extra"},
		{`[]`, "$"},
	} {
		err := s.Validate(decode(t, tc.value))
		switch {
		case tc.wantPath == "" && err != nil:
			t.Errorf("%s: unexpected violation %v", tc.value, err)
		case tc.wantPath != "":
			var ve *ValidationError
			if !errors.As(err, &ve) || ve.Path != tc.wantPath {
				t.Errorf("%s: want a violation at %s, got %v", tc.value, tc.wantPath, err)
			}
		}
	}
}

// .
// .
func TestKeywordsOutsideTheSubsetAreRefusedByName(t *testing.T) {
	for _, tc := range []struct{ schema, keyword, path string }{
		{`{"$ref":"#/x"}`, "$ref", "#"},
		{`{"allOf":[]}`, "allOf", "#"},
		{`{"type":"object","properties":{"a":{"oneOf":[]}}}`, "oneOf", "#/properties/a"},
		{`{"patternProperties":{}}`, "patternProperties", "#"},
		{`{"additionalProperties":{"type":"string"}}`, "additionalProperties", "#"},
		{`{"items":[{"type":"string"}]}`, "items", "#"},
		{`{"type":"thing"}`, "type", "#"},
		{`{"pattern":"("}`, "pattern", "#"},
		{`{"minLength":-1}`, "minLength", "#"},
		{`{"multipleOf":0}`, "multipleOf", "#"},
		{`{"required":[""]}`, "required", "#"},
	} {
		m := decode(t, tc.schema).(map[string]interface{})
		_, err := Compile(m)
		var ce *CompileError
		if !errors.As(err, &ce) || ce.Keyword != tc.keyword || ce.Path != tc.path {
			t.Errorf("%s: want refusal at %s keyword %s, got %v", tc.schema, tc.path, tc.keyword, err)
		}
	}
	// .
	// .
	compileText(t, `{"$schema":"x","$id":"y","title":"t","description":"d","default":1,"examples":[1],"deprecated":true,"format":"email","type":"string"}`)
	if got := strings.Join(Keywords(), ","); !strings.Contains(got, "additionalProperties") || strings.Contains(got, "$ref") {
		t.Fatalf("the keyword list must be the subset itself, got %s", got)
	}
}

// .
// .
func TestANilSchemaIsTheOpenContract(t *testing.T) {
	var s *Schema
	if err := s.Validate(decode(t, `{"anything":[1,2]}`)); err != nil {
		t.Fatal(err)
	}
}
