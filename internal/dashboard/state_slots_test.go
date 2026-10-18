package dashboard

import (
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// pageModules returns every embedded .js module's source by path.
func pageModules(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := fs.WalkDir(staticFS, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".js") {
			return nil
		}
		b, err := staticFS.ReadFile(p)
		if err != nil {
			return err
		}
		out[p] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 30 {
		t.Fatalf("read %d modules — the embed walk is broken", len(out))
	}
	return out
}

var (
	slotCalled   = regexp.MustCompile(`\bS\.([A-Za-z_$][\w$]*)\s*(?:\?\.)?\(|typeof\s+S\.([A-Za-z_$][\w$]*)\s*===?\s*'function'`)
	slotAssigned = regexp.MustCompile(`\bS\.([A-Za-z_$][\w$]*)\s*=[^=]`)
)

// TestEveryCalledStateSlotIsAssigned fails when a module calls a slot on S
// (S.x(), S.x?.(), if (S.x) S.x(), typeof S.x === 'function') that no
// embedded module assigns.
func TestEveryCalledStateSlotIsAssigned(t *testing.T) {
	called := map[string][]string{}
	assigned := map[string]bool{}
	for p, src := range pageModules(t) {
		for _, m := range slotCalled.FindAllStringSubmatch(src, -1) {
			name := m[1] + m[2]
			called[name] = append(called[name], p)
		}
		for _, m := range slotAssigned.FindAllStringSubmatch(src, -1) {
			assigned[m[1]] = true
		}
	}
	if len(called) < 20 {
		t.Fatalf("found %d called slots — the call pattern no longer matches the modules", len(called))
	}
	var names []string
	for name := range called {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !assigned[name] {
			t.Errorf("S.%s is called (%s) and no module assigns it: the call is silently nothing", name, strings.Join(dedupe(called[name]), ", "))
		}
	}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// stateKey finds a key of the S literal once comments are stripped.
var stateKey = regexp.MustCompile(`([A-Za-z_$][\w$]*)\s*:`)

// TestEveryStateKeyIsDeclared fails when a module uses a key of S that
// state.js does not declare: the declaration is where a reader finds what
// the page shares and which module owns it.
func TestEveryStateKeyIsDeclared(t *testing.T) {
	mods := pageModules(t)
	src, ok := mods["static/state.js"]
	if !ok {
		t.Fatal("state.js is not embedded")
	}
	body := src[strings.Index(src, "export const S = {"):]
	body = body[:strings.Index(body, "\n};")]
	body = regexp.MustCompile(`//[^\n]*`).ReplaceAllString(body, "")
	declared := map[string]bool{}
	for _, m := range stateKey.FindAllStringSubmatch(body, -1) {
		declared[m[1]] = true
	}
	if len(declared) < 30 {
		t.Fatalf("read %d keys from state.js — the declaration no longer parses", len(declared))
	}
	used := map[string][]string{}
	for p, src := range mods {
		for _, m := range regexp.MustCompile(`\bS\.([A-Za-z_$][\w$]*)`).FindAllStringSubmatch(src, -1) {
			used[m[1]] = append(used[m[1]], p)
		}
	}
	var names []string
	for name := range used {
		if !declared[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		t.Errorf("S.%s is used (%s) and state.js does not declare it", name, strings.Join(dedupe(used[name]), ", "))
	}
}
