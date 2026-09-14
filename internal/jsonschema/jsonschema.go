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
package jsonschema

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

// .
// .
const maxDepth = 32

// .
type CompileError struct {
	Path    string
	Keyword string
	Reason  string
}

func (e *CompileError) Error() string {
	return fmt.Sprintf("schema %s: keyword %q: %s", e.Path, e.Keyword, e.Reason)
}

// .
type ValidationError struct {
	Path   string
	Reason string
}

func (e *ValidationError) Error() string { return fmt.Sprintf("%s: %s", e.Path, e.Reason) }

// .
type Schema struct {
	types      []string
	properties map[string]*Schema
	propNames  []string
	required   []string
	additional *bool
	items      *Schema
	enum       []interface{}
	hasConst   bool
	constVal   interface{}
	minimum    *float64
	maximum    *float64
	exclMin    *float64
	exclMax    *float64
	multipleOf *float64
	minLength  *int
	maxLength  *int
	pattern    *regexp.Regexp
	minItems   *int
	maxItems   *int
	unique     bool
	minProps   *int
	maxProps   *int
}

var annotations = map[string]bool{
	"$schema": true, "$id": true, "title": true, "description": true,
	"default": true, "examples": true, "deprecated": true, "format": true,
}

var typeNames = map[string]bool{
	"object": true, "array": true, "string": true, "number": true,
	"integer": true, "boolean": true, "null": true,
}

// .
// .
func Keywords() []string {
	k := []string{"type", "properties", "required", "additionalProperties", "items", "enum", "const",
		"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf",
		"minLength", "maxLength", "pattern", "minItems", "maxItems", "uniqueItems",
		"minProperties", "maxProperties"}
	sort.Strings(k)
	return k
}

// .
// .
func Compile(raw map[string]interface{}) (*Schema, error) {
	return compile(raw, "#", 0)
}

func compile(raw map[string]interface{}, path string, depth int) (*Schema, error) {
	if depth > maxDepth {
		return nil, &CompileError{Path: path, Keyword: "", Reason: fmt.Sprintf("nested deeper than %d", maxDepth)}
	}
	s := &Schema{}
	for k, v := range raw {
		if annotations[k] {
			continue
		}
		var err error
		switch k {
		case "type":
			err = s.setType(v, path)
		case "properties":
			err = s.setProperties(v, path, depth)
		case "required":
			err = s.setRequired(v, path)
		case "additionalProperties":
			b, ok := v.(bool)
			if !ok {
				err = &CompileError{Path: path, Keyword: k, Reason: "must be a boolean in the closed subset"}
			} else {
				s.additional = &b
			}
		case "items":
			m, ok := v.(map[string]interface{})
			if !ok {
				err = &CompileError{Path: path, Keyword: k, Reason: "must be one schema object in the closed subset"}
			} else {
				s.items, err = compile(m, path+"/items", depth+1)
			}
		case "enum":
			list, ok := v.([]interface{})
			if !ok || len(list) == 0 {
				err = &CompileError{Path: path, Keyword: k, Reason: "must be a non-empty array"}
			} else {
				s.enum = list
			}
		case "const":
			s.hasConst, s.constVal = true, v
		case "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf":
			f, ok := v.(float64)
			if !ok {
				err = &CompileError{Path: path, Keyword: k, Reason: "must be a number"}
				break
			}
			if k == "multipleOf" && f <= 0 {
				err = &CompileError{Path: path, Keyword: k, Reason: "must be greater than zero"}
				break
			}
			fv := f
			switch k {
			case "minimum":
				s.minimum = &fv
			case "maximum":
				s.maximum = &fv
			case "exclusiveMinimum":
				s.exclMin = &fv
			case "exclusiveMaximum":
				s.exclMax = &fv
			case "multipleOf":
				s.multipleOf = &fv
			}
		case "minLength", "maxLength", "minItems", "maxItems", "minProperties", "maxProperties":
			n, cerr := nonNegativeInt(v)
			if cerr != "" {
				err = &CompileError{Path: path, Keyword: k, Reason: cerr}
				break
			}
			nv := n
			switch k {
			case "minLength":
				s.minLength = &nv
			case "maxLength":
				s.maxLength = &nv
			case "minItems":
				s.minItems = &nv
			case "maxItems":
				s.maxItems = &nv
			case "minProperties":
				s.minProps = &nv
			case "maxProperties":
				s.maxProps = &nv
			}
		case "pattern":
			p, ok := v.(string)
			if !ok {
				err = &CompileError{Path: path, Keyword: k, Reason: "must be a string"}
				break
			}
			re, rerr := regexp.Compile(p)
			if rerr != nil {
				err = &CompileError{Path: path, Keyword: k, Reason: "not RE2 syntax: " + rerr.Error()}
				break
			}
			s.pattern = re
		case "uniqueItems":
			b, ok := v.(bool)
			if !ok {
				err = &CompileError{Path: path, Keyword: k, Reason: "must be a boolean"}
			} else {
				s.unique = b
			}
		default:
			err = &CompileError{Path: path, Keyword: k, Reason: "outside the closed subset"}
		}
		if err != nil {
			return nil, err
		}
	}
	return s, nil
}

func nonNegativeInt(v interface{}) (int, string) {
	f, ok := v.(float64)
	if !ok || f != math.Trunc(f) || f < 0 || f > math.MaxInt32 {
		return 0, "must be a non-negative integer"
	}
	return int(f), ""
}

func (s *Schema) setType(v interface{}, path string) error {
	switch t := v.(type) {
	case string:
		if !typeNames[t] {
			return &CompileError{Path: path, Keyword: "type", Reason: fmt.Sprintf("unknown type %q", t)}
		}
		s.types = []string{t}
	case []interface{}:
		if len(t) == 0 {
			return &CompileError{Path: path, Keyword: "type", Reason: "empty type list"}
		}
		for _, e := range t {
			name, ok := e.(string)
			if !ok || !typeNames[name] {
				return &CompileError{Path: path, Keyword: "type", Reason: fmt.Sprintf("unknown type %v", e)}
			}
			s.types = append(s.types, name)
		}
	default:
		return &CompileError{Path: path, Keyword: "type", Reason: "must be a type name or a list of type names"}
	}
	return nil
}

func (s *Schema) setProperties(v interface{}, path string, depth int) error {
	m, ok := v.(map[string]interface{})
	if !ok {
		return &CompileError{Path: path, Keyword: "properties", Reason: "must be an object of schemas"}
	}
	s.properties = make(map[string]*Schema, len(m))
	for name, sub := range m {
		subm, ok := sub.(map[string]interface{})
		if !ok {
			return &CompileError{Path: path + "/properties/" + name, Keyword: "properties", Reason: "each property must be a schema object"}
		}
		c, err := compile(subm, path+"/properties/"+name, depth+1)
		if err != nil {
			return err
		}
		s.properties[name] = c
		s.propNames = append(s.propNames, name)
	}
	sort.Strings(s.propNames)
	return nil
}

func (s *Schema) setRequired(v interface{}, path string) error {
	switch list := v.(type) {
	case []string:
		for _, name := range list {
			if name == "" {
				return &CompileError{Path: path, Keyword: "required", Reason: "empty property name"}
			}
		}
		s.required = append([]string{}, list...)
	case []interface{}:
		for _, e := range list {
			name, ok := e.(string)
			if !ok || name == "" {
				return &CompileError{Path: path, Keyword: "required", Reason: "must list non-empty property names"}
			}
			s.required = append(s.required, name)
		}
	default:
		return &CompileError{Path: path, Keyword: "required", Reason: "must be an array of property names"}
	}
	return nil
}

// .
// .
func (s *Schema) Validate(v interface{}) error {
	if s == nil {
		return nil
	}
	return s.validate(v, "$")
}

func typeOf(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64:
		if t == math.Trunc(t) && !math.IsInf(t, 0) {
			return "integer"
		}
		return "number"
	case string:
		return "string"
	case []interface{}:
		return "array"
	case map[string]interface{}:
		return "object"
	}
	return fmt.Sprintf("%T", v)
}

func (s *Schema) typeAllowed(actual string) bool {
	if len(s.types) == 0 {
		return true
	}
	for _, want := range s.types {
		if want == actual || (want == "number" && actual == "integer") {
			return true
		}
	}
	return false
}

func (s *Schema) validate(v interface{}, path string) error {
	actual := typeOf(v)
	if !s.typeAllowed(actual) {
		return &ValidationError{Path: path, Reason: fmt.Sprintf("is %s, want %s", actual, strings.Join(s.types, " or "))}
	}
	if s.hasConst && !equalJSON(v, s.constVal) {
		return &ValidationError{Path: path, Reason: "is not the declared constant"}
	}
	if s.enum != nil {
		found := false
		for _, e := range s.enum {
			if equalJSON(v, e) {
				found = true
				break
			}
		}
		if !found {
			return &ValidationError{Path: path, Reason: "is not one of the enumerated values"}
		}
	}
	switch t := v.(type) {
	case float64:
		if s.minimum != nil && t < *s.minimum {
			return &ValidationError{Path: path, Reason: fmt.Sprintf("%v is below the minimum %v", t, *s.minimum)}
		}
		if s.maximum != nil && t > *s.maximum {
			return &ValidationError{Path: path, Reason: fmt.Sprintf("%v is above the maximum %v", t, *s.maximum)}
		}
		if s.exclMin != nil && t <= *s.exclMin {
			return &ValidationError{Path: path, Reason: fmt.Sprintf("%v is not above %v", t, *s.exclMin)}
		}
		if s.exclMax != nil && t >= *s.exclMax {
			return &ValidationError{Path: path, Reason: fmt.Sprintf("%v is not below %v", t, *s.exclMax)}
		}
		if s.multipleOf != nil {
			q := t / *s.multipleOf
			if math.Abs(q-math.Round(q)) > 1e-9 {
				return &ValidationError{Path: path, Reason: fmt.Sprintf("%v is not a multiple of %v", t, *s.multipleOf)}
			}
		}
	case string:
		n := len([]rune(t))
		if s.minLength != nil && n < *s.minLength {
			return &ValidationError{Path: path, Reason: fmt.Sprintf("length %d is below the minimum %d", n, *s.minLength)}
		}
		if s.maxLength != nil && n > *s.maxLength {
			return &ValidationError{Path: path, Reason: fmt.Sprintf("length %d is above the maximum %d", n, *s.maxLength)}
		}
		if s.pattern != nil && !s.pattern.MatchString(t) {
			return &ValidationError{Path: path, Reason: "does not match the declared pattern"}
		}
	case []interface{}:
		if s.minItems != nil && len(t) < *s.minItems {
			return &ValidationError{Path: path, Reason: fmt.Sprintf("%d items is below the minimum %d", len(t), *s.minItems)}
		}
		if s.maxItems != nil && len(t) > *s.maxItems {
			return &ValidationError{Path: path, Reason: fmt.Sprintf("%d items is above the maximum %d", len(t), *s.maxItems)}
		}
		if s.unique {
			for i := range t {
				for j := i + 1; j < len(t); j++ {
					if equalJSON(t[i], t[j]) {
						return &ValidationError{Path: fmt.Sprintf("%s[%d]", path, j), Reason: "duplicates an earlier item"}
					}
				}
			}
		}
		if s.items != nil {
			for i, e := range t {
				if err := s.items.validate(e, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	case map[string]interface{}:
		if s.minProps != nil && len(t) < *s.minProps {
			return &ValidationError{Path: path, Reason: fmt.Sprintf("%d properties is below the minimum %d", len(t), *s.minProps)}
		}
		if s.maxProps != nil && len(t) > *s.maxProps {
			return &ValidationError{Path: path, Reason: fmt.Sprintf("%d properties is above the maximum %d", len(t), *s.maxProps)}
		}
		for _, name := range s.required {
			if _, ok := t[name]; !ok {
				return &ValidationError{Path: path + "." + name, Reason: "is required"}
			}
		}
		for _, name := range s.propNames {
			if child, ok := t[name]; ok {
				if err := s.properties[name].validate(child, path+"."+name); err != nil {
					return err
				}
			}
		}
		if s.additional != nil && !*s.additional {
			extra := make([]string, 0)
			for name := range t {
				if _, declared := s.properties[name]; !declared {
					extra = append(extra, name)
				}
			}
			if len(extra) > 0 {
				sort.Strings(extra)
				return &ValidationError{Path: path + "." + extra[0], Reason: "is not a declared property"}
			}
		}
	}
	return nil
}

// .
func equalJSON(a, b interface{}) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case float64:
		y, ok := b.(float64)
		return ok && x == y
	case string:
		y, ok := b.(string)
		return ok && x == y
	case []interface{}:
		y, ok := b.([]interface{})
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !equalJSON(x[i], y[i]) {
				return false
			}
		}
		return true
	case map[string]interface{}:
		y, ok := b.(map[string]interface{})
		if !ok || len(x) != len(y) {
			return false
		}
		for k, xv := range x {
			yv, ok := y[k]
			if !ok || !equalJSON(xv, yv) {
				return false
			}
		}
		return true
	}
	return false
}
