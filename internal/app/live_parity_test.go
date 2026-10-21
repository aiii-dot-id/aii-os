package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// THE DOOR AND A RELOAD SAY THE SAME THING ABOUT WHAT IS LIVE. The door
// holds a key for a restart by listing it (restart = append(restart, key));
// a reload calls a change restart-only when it survives blankLiveAppliable.
// Both were kept by hand, one setting at a time, and they drifted: keys the
// door applies live were announced by a reload as SAVED FOR NEXT BOOT, and
// the one agency key the door holds for a restart was announced as live.
// This reads every key the door's switch accepts from setup.go, follows it
// to the field it sets, and holds the two verdicts equal.
func TestTheDoorAndAReloadAgreeOnWhatIsLive(t *testing.T) {
	atDoor := doorRestartVerdicts(t)
	if len(atDoor) < 40 {
		t.Fatalf("read %d keys from the door's switch — the walk is not reading the door", len(atDoor))
	}
	// The two key families the door takes by prefix, before its switch.
	atDoor["plugins.settings"] = false
	atDoor["plugins.grants"] = false
	// The overrides block's digest sets no field: it rides with a save of
	// the block, which it guards, and alone is refused.
	delete(atDoor, "overrides_digest")
	keys := make([]string, 0, len(atDoor))
	for k := range atDoor {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		onReload, ok := reloadRestartVerdict(key)
		if !ok {
			t.Errorf("%s: no configuration field answers to this key", key)
			continue
		}
		if onReload != atDoor[key] {
			t.Errorf("%s: the door says %s, a reload says %s", key, liveVerdict(atDoor[key]), liveVerdict(onReload))
		}
	}
}

func liveVerdict(restart bool) string {
	if restart {
		return "restart-required"
	}
	return "live"
}

// doorRestartVerdicts reads applyConfigChangeWith: every string a `switch
// key` case names, and whether that case lists the key for a restart.
func doorRestartVerdicts(t *testing.T) map[string]bool {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "setup.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	verdicts := map[string]bool{}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "applyConfigChangeWith" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok {
				return true
			}
			if tag, ok := sw.Tag.(*ast.Ident); !ok || tag.Name != "key" {
				return true
			}
			for _, s := range sw.Body.List {
				clause := s.(*ast.CaseClause)
				restart := false
				ast.Inspect(clause, func(n ast.Node) bool {
					as, ok := n.(*ast.AssignStmt)
					if !ok || len(as.Lhs) != 1 {
						return true
					}
					if id, ok := as.Lhs[0].(*ast.Ident); ok && id.Name == "restart" {
						restart = true
					}
					return true
				})
				for _, e := range clause.List {
					lit, ok := e.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					k, _ := strconv.Unquote(lit.Value)
					verdicts[k] = verdicts[k] || restart
				}
			}
			return true
		})
	}
	return verdicts
}

// reloadRestartVerdict edits the one field a door key sets and asks
// blankLiveAppliable whether a reload would call the edit restart-only.
func reloadRestartVerdict(key string) (restart, ok bool) {
	base, edited := defaultConfig(), defaultConfig()
	field, ok := fieldForKey(reflect.ValueOf(edited).Elem(), strings.Split(key, "."))
	if !ok {
		return false, false
	}
	perturb(field)
	a, b := *base, *edited
	blankLiveAppliable(&a)
	blankLiveAppliable(&b)
	return !reflect.DeepEqual(a, b), true
}

// fieldForKey follows a door key through the JSON names the file uses;
// the door's witness.* keys are the file's witness_server section.
func fieldForKey(v reflect.Value, path []string) (reflect.Value, bool) {
	for i, name := range path {
		if i == 0 && name == "witness" {
			name = "witness_server"
		}
		for v.Kind() == reflect.Pointer {
			if v.IsNil() {
				v.Set(reflect.New(v.Type().Elem()))
			}
			v = v.Elem()
		}
		if v.Kind() != reflect.Struct {
			return reflect.Value{}, false
		}
		next, found := reflect.Value{}, false
		for j := 0; j < v.NumField(); j++ {
			if strings.Split(v.Type().Field(j).Tag.Get("json"), ",")[0] == name {
				next, found = v.Field(j), true
				break
			}
		}
		if !found {
			return reflect.Value{}, false
		}
		v = next
	}
	return v, v.CanSet()
}

// perturb gives a field a value it did not have.
func perturb(v reflect.Value) {
	switch v.Kind() {
	case reflect.Bool:
		v.SetBool(!v.Bool())
	case reflect.String:
		v.SetString(v.String() + "-edited")
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(v.Int() + 7)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(v.Uint() + 7)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(v.Float() + 0.5)
	case reflect.Pointer:
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		perturb(v.Elem())
	case reflect.Slice:
		v.Set(reflect.Append(v, reflect.New(v.Type().Elem()).Elem()))
	case reflect.Map:
		if v.IsNil() {
			v.Set(reflect.MakeMap(v.Type()))
		}
		k := reflect.New(v.Type().Key()).Elem()
		if k.Kind() == reflect.String {
			k.SetString("edited")
		}
		v.SetMapIndex(k, reflect.New(v.Type().Elem()).Elem())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				perturb(v.Field(i))
				return
			}
		}
	}
}
