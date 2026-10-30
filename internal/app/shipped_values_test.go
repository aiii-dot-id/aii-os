package app

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// shippedMade says how shipped_values.json was made, in the file itself.
const shippedMade = "Every value a release wrote into an identity's config.json and providers.json, by the line it wrote it at " +
	"(providers.json's list by each entry's name). For each release: its tree at the commit named (its tag, or, for a " +
	"release that has none, the last commit of main before the next version), with one command added that calls " +
	"LoadConfig and loadProvidersFile on an empty directory, run once: the files a first start writes, which every " +
	"later save wrote back with the release's defaults for what the file left out; and its config/providers.json. " +
	"Releases after these write only what was named, so no default of theirs reaches a file. Remade by " +
	"TestWriteShippedValues."

// TestWriteShippedValues remakes shipped_values.json from a directory
// holding releases.txt (lines "<version> <commit>") and, under each
// version, config.json and providers.json as that release's first start
// wrote them, and embedded-providers.json (its config/providers.json). It
// runs only when AII_SHIPPED_DUMPS names that directory.
func TestWriteShippedValues(t *testing.T) {
	dir := os.Getenv("AII_SHIPPED_DUMPS")
	if dir == "" {
		t.Skip("AII_SHIPPED_DUMPS names no directory of release dumps: shipped_values.json is not remade")
	}
	f, err := os.Open(filepath.Join(dir, "releases.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := shippedHistory{Made: shippedMade}
	config, providers := map[string]*shippedPath{}, map[string]*shippedPath{}
	add := func(into map[string]*shippedPath, path []string, v *jsonDoc) {
		k := pathKey(path)
		p := into[k]
		if p == nil {
			p = &shippedPath{Path: path}
			into[k] = p
		}
		c := canonicalJSON(v.compact())
		if !slices.ContainsFunc(p.Values, func(have json.RawMessage) bool { return string(canonicalJSON(have)) == string(c) }) {
			p.Values = append(p.Values, json.RawMessage(v.compact()))
		}
	}
	read := func(name string) *jsonDoc {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		d, err := parseJSONDoc(raw)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return d
	}
	lines := bufio.NewScanner(f)
	for lines.Scan() {
		version, commit, ok := strings.Cut(strings.TrimSpace(lines.Text()), " ")
		if !ok {
			continue
		}
		h.Releases = append(h.Releases, version+" "+commit)
		at := filepath.Join(dir, version)
		docLeaves(read(filepath.Join(at, "config.json")), nil, func(p []string, v *jsonDoc) { add(config, p, v) })
		for _, name := range []string{"providers.json", "embedded-providers.json"} {
			d := read(filepath.Join(at, name))
			explodeProviders(d)
			docLeaves(d, nil, func(p []string, v *jsonDoc) { add(providers, p, v) })
		}
	}
	for _, from := range []struct {
		m    map[string]*shippedPath
		into *[]shippedPath
	}{{config, &h.Config}, {providers, &h.Providers}} {
		keys := make([]string, 0, len(from.m))
		for k := range from.m {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			*from.into = append(*from.into, *from.m[k])
		}
	}
	// One line for each line of a file, so a release's change reads as a
	// change of lines.
	var out strings.Builder
	head, _ := json.Marshal(h.Made)
	releases, _ := json.Marshal(h.Releases)
	out.WriteString("{\n \"made\": " + string(head) + ",\n \"releases\": " + string(releases))
	for _, part := range []struct {
		name  string
		paths []shippedPath
	}{{"config.json", h.Config}, {"providers.json", h.Providers}} {
		out.WriteString(",\n \"" + part.name + "\": [")
		for i, p := range part.paths {
			line, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			if i > 0 {
				out.WriteString(",")
			}
			out.WriteString("\n  " + string(line))
		}
		out.WriteString("\n ]")
	}
	out.WriteString("\n}\n")
	if err := os.WriteFile("shipped_values.json", []byte(out.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}
