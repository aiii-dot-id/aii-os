package app

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
)

// keysThisBuildAdds is every key this build's Config declares that 0.1.23's
// does not, derived, never listed by hand: configKeyPaths run on this
// build's Config, less testdata/config_keys_v0.1.23.txt (the same function
// run on 0.1.23's). A key inside a new object is the object's: 0.1.23
// refuses the object whole. Sorted.
func keysThisBuildAdds(t *testing.T) []string {
	t.Helper()
	objects, values := predecessorKeys(t)
	isNew := map[string]bool{}
	for _, line := range configKeyPaths(reflect.TypeOf(Config{})) {
		_, path, _ := strings.Cut(line, " ")
		if !objects[path] && !values[path] {
			isNew[path] = true
		}
	}
	var added []string
	for path := range isNew {
		inside := false
		for i := strings.LastIndex(path, "."); i > 0; i = strings.LastIndex(path[:i], ".") {
			if isNew[path[:i]] {
				inside = true
				break
			}
		}
		if !inside {
			added = append(added, path)
		}
	}
	sort.Strings(added)
	return added
}

// setNonZero gives the key at path in v a value that is not its zero, so
// that a field written only once set is written: the control half of the
// proof below. A map's key ("*") gets one entry and a list of objects
// ("[]") one element.
func setNonZero(t *testing.T, v reflect.Value, path []string) {
	t.Helper()
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		v = v.Elem()
	}
	if len(path) == 0 {
		fillNonZero(t, v)
		return
	}
	seg := path[0]
	switch {
	case seg == "*":
		if v.IsNil() {
			v.Set(reflect.MakeMap(v.Type()))
		}
		e := reflect.New(v.Type().Elem()).Elem()
		setNonZero(t, e, path[1:])
		v.SetMapIndex(reflect.ValueOf("fixture").Convert(v.Type().Key()), e)
		return
	case strings.HasSuffix(seg, "[]"):
		f := fieldByKey(t, v, strings.TrimSuffix(seg, "[]"))
		e := reflect.New(f.Type().Elem()).Elem()
		setNonZero(t, e, path[1:])
		f.Set(reflect.Append(reflect.MakeSlice(f.Type(), 0, 1), e))
		return
	}
	setNonZero(t, fieldByKey(t, v, seg), path[1:])
}

// fieldByKey is the field of struct v that config.json names key.
func fieldByKey(t *testing.T, v reflect.Value, key string) reflect.Value {
	t.Helper()
	for i := 0; i < v.NumField(); i++ {
		f := v.Type().Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" {
			name = f.Name
		}
		if f.IsExported() && name == key {
			return v.Field(i)
		}
	}
	t.Fatalf("rig: %s declares no key %q", v.Type(), key)
	return reflect.Value{}
}

// fillNonZero sets v to a value that is not its type's zero.
func fillNonZero(t *testing.T, v reflect.Value) {
	t.Helper()
	switch v.Kind() {
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		fillNonZero(t, v.Elem())
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1)
	case reflect.String:
		v.SetString("/fixture")
	case reflect.Slice:
		e := reflect.New(v.Type().Elem()).Elem()
		fillNonZero(t, e)
		v.Set(reflect.Append(reflect.MakeSlice(v.Type(), 0, 1), e))
	case reflect.Map:
		e := reflect.New(v.Type().Elem()).Elem()
		fillNonZero(t, e)
		m := reflect.MakeMap(v.Type())
		m.SetMapIndex(reflect.ValueOf("fixture").Convert(v.Type().Key()), e)
		v.Set(m)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				fillNonZero(t, v.Field(i))
				return
			}
		}
		t.Fatalf("rig: %s has no field to set", v.Type())
	default:
		t.Fatalf("rig: a %s cannot be given a value here", v.Type())
	}
}

// predecessorKeys is testdata/config_keys_v0.1.23.txt: every key 0.1.23's
// loader declares, as objects ("O") and values read whole ("L").
func predecessorKeys(t *testing.T) (objects, values map[string]bool) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "config_keys_v0.1.23.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	objects, values = map[string]bool{}, map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		kind, path, ok := strings.Cut(line, " ")
		if !ok {
			t.Fatalf("testdata line %q is not \"O path\" or \"L path\"", line)
		}
		switch kind {
		case "O":
			objects[path] = true
		case "L":
			values[path] = true
		default:
			t.Fatalf("testdata line %q is not \"O path\" or \"L path\"", line)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return objects, values
}

// predecessorRefuses is every key in a config.json that 0.1.23's loader
// does not declare, and so refuses ("json: unknown field"), sorted.
func predecessorRefuses(t *testing.T, raw []byte) []string {
	t.Helper()
	objects, values := predecessorKeys(t)
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the saved file does not read: %v\n%s", err, raw)
	}
	var unknown []string
	var walk func(v any, path string)
	walk = func(v any, path string) {
		m, ok := v.(map[string]any)
		if !ok {
			return
		}
		for k, child := range m {
			p := k
			if path != "" {
				p = path + "." + k
			}
			star := "*"
			if path != "" {
				star = path + ".*"
			}
			switch {
			case objects[p]:
				walk(child, p)
			case objects[star]:
				walk(child, star)
			case values[p], values[star]:
				if list, ok := child.([]any); ok && objects[p+"[]"] {
					for _, e := range list {
						walk(e, p+"[]")
					}
				}
			default:
				unknown = append(unknown, p)
			}
		}
	}
	walk(doc, "")
	sort.Strings(unknown)
	return unknown
}

func TestEveryKeyThisBuildAddsIsWrittenOnlyOnceSet(t *testing.T) {
	added := keysThisBuildAdds(t)
	if len(added) == 0 {
		t.Fatal("rig: this build adds no key over 0.1.23, so nothing here is proved")
	}
	unset := func(what string, raw []byte) {
		t.Helper()
		if refused := predecessorRefuses(t, raw); len(refused) != 0 {
			t.Errorf("%s: 0.1.23 would refuse this file for %v, which this build writes unset:\n%s", what, refused, raw)
		}
	}
	for what, cfg := range map[string]*Config{"a Config at its zero": {}, "the defaults": defaultConfig()} {
		raw, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		unset(what, raw)
	}

	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	unset("the first boot's config.json", []byte(readFile(t, path)))
	a := New(cfg)
	if _, err := a.applyConfigChange(map[string]any{"prompt.alarm_firings": float64(30)}); err != nil {
		t.Fatal(err)
	}
	cleared := 0
	for _, key := range added {
		for _, v := range []any{nil, float64(0)} {
			if _, err := a.applyConfigChange(map[string]any{key: v}); err == nil {
				cleared++
			}
		}
	}
	if cleared == 0 {
		t.Fatal("rig: the door took no new key cleared, so the save half proves nothing")
	}
	raw := []byte(readFile(t, path))
	if !strings.Contains(string(raw), `"alarm_firings": 30`) {
		t.Fatalf("rig: the save did not write the setting it changed:\n%s", raw)
	}
	unset("a save with each new key the door takes sent cleared", raw)

	// Control: every new key set, and the check names exactly them.
	var full Config
	for _, key := range added {
		setNonZero(t, reflect.ValueOf(&full).Elem(), strings.Split(key, "."))
	}
	raw, err = json.Marshal(&full)
	if err != nil {
		t.Fatal(err)
	}
	if got := predecessorRefuses(t, raw); !slices.Equal(got, added) {
		t.Errorf("with every new key set, the keys 0.1.23 refuses are %v; want exactly the keys this build adds, %v", got, added)
	}

}
