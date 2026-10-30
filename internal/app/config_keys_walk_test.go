package app

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
)

// configKeyPaths is every key a configuration type declares, as the strict
// loader reads a file: one line per key, "O <path>" for a key whose value
// is an object the loader reads key by key, "L <path>" for a value it reads
// whole (a number, a string, a list of plain values, a value with its own
// decoding such as a limit that may be "unlimited"). A map's keys are "*",
// and a list of objects is "[]" after its key.
//
// THE SAME FUNCTION MADE testdata/config_keys_v0.1.23.txt: it was copied
// into a checkout of v0.1.23 and run on that release's Config, so the list
// is what 0.1.23's own types declare, not what this build remembers of
// them.
func configKeyPaths(t reflect.Type) []string {
	var out []string
	unmarshaler := reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()
	var walk func(t reflect.Type, path string, seen map[reflect.Type]bool)
	objectish := func(t reflect.Type) bool {
		if t.Kind() != reflect.Struct {
			return false
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.IsExported() && (f.Tag.Get("json") != "" || f.Anonymous) {
				return true
			}
		}
		// A struct with no keys of its own that decodes itself is one value.
		return !reflect.PointerTo(t).Implements(unmarshaler)
	}
	walk = func(t reflect.Type, path string, seen map[reflect.Type]bool) {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		switch {
		case t.Kind() == reflect.Map:
			out = append(out, "O "+path)
			walk(t.Elem(), path+".*", seen)
			return
		case t.Kind() == reflect.Slice && t.Elem().Kind() != reflect.Uint8:
			e := t.Elem()
			for e.Kind() == reflect.Pointer {
				e = e.Elem()
			}
			if objectish(e) {
				out = append(out, "L "+path)
				walk(e, path+"[]", seen)
				return
			}
			out = append(out, "L "+path)
			return
		case !objectish(t):
			out = append(out, "L "+path)
			return
		}
		if seen[t] {
			return
		}
		seen = maps1(seen, t)
		if path != "" {
			out = append(out, "O "+path)
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			tag := f.Tag.Get("json")
			name, _, _ := strings.Cut(tag, ",")
			if name == "-" {
				continue
			}
			if f.Anonymous && name == "" {
				walk(f.Type, path, seen)
				continue
			}
			if name == "" {
				name = f.Name
			}
			p := name
			if path != "" {
				p = path + "." + name
			}
			walk(f.Type, p, seen)
		}
	}
	walk(t, "", map[reflect.Type]bool{})
	sort.Strings(out)
	return slicesCompact(out)
}

func maps1(m map[reflect.Type]bool, t reflect.Type) map[reflect.Type]bool {
	n := make(map[reflect.Type]bool, len(m)+1)
	for k, v := range m {
		n[k] = v
	}
	n[t] = true
	return n
}

func slicesCompact(s []string) []string {
	var out []string
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}
