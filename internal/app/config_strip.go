package app

import (
	_ "embed"
	"encoding/json"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

//go:embed shipped_values.json
var shippedValuesFile []byte

type shippedHistory struct {
	Made      string        `json:"made"`
	Releases  []string      `json:"releases"`
	Config    []shippedPath `json:"config.json"`
	Providers []shippedPath `json:"providers.json"`
}

type shippedPath struct {
	Path   []string          `json:"path"`
	Values []json.RawMessage `json:"values"`
}

type shippedSet map[string]map[string]bool

var shippedSets = sync.OnceValues(func() (config, providers shippedSet) {
	var h shippedHistory
	if err := json.Unmarshal(shippedValuesFile, &h); err != nil {
		panic("embedded shipped_values.json is invalid: " + err.Error())
	}
	config = setOf(h.Config)
	config.spelledHere(identityPaths)
	return config, setOf(h.Providers)
})

func (s shippedSet) spelledHere(lines [][]string) {
	for _, line := range lines {
		vals := s[pathKey(line)]
		var here []string
		for v := range vals {
			var path string
			if json.Unmarshal([]byte(v), &path) != nil {
				continue
			}
			if raw, err := json.Marshal(filepath.FromSlash(path)); err == nil {
				here = append(here, string(canonicalJSON(raw)))
			}
		}
		for _, v := range here {
			vals[v] = true
		}
	}
}

func setOf(paths []shippedPath) shippedSet {
	out := shippedSet{}
	for _, p := range paths {
		vals := map[string]bool{}
		for _, v := range p.Values {
			vals[string(canonicalJSON(v))] = true
		}
		out[pathKey(p.Path)] = vals
	}
	return out
}

func pathKey(path []string) string { return strings.Join(path, "\x00") }

func (s shippedSet) shipped(path []string, v *jsonDoc) bool {
	return s[pathKey(path)][string(canonicalJSON(v.compact()))]
}

func docLeaves(d *jsonDoc, path []string, f func(path []string, v *jsonDoc)) {
	if !d.isObject() || len(d.keys) == 0 && len(path) > 0 {
		f(path, d)
		return
	}
	for _, k := range d.keys {
		docLeaves(d.members[k], append(slices.Clone(path), k), f)
	}
}

func removeAt(d *jsonDoc, path []string, keep int) {
	if len(path) == 1 {
		d.remove(path[0])
		return
	}
	m := d.member(path[0])
	if !m.isObject() {
		return
	}
	removeAt(m, path[1:], keep-1)
	if len(m.keys) == 0 && keep <= 0 {
		d.remove(path[0])
	}
}

func explodeProviders(d *jsonDoc) {
	l, ok := keyedElements(d.member("providers"), "name")
	if !ok || d.member("providers") == nil {
		return
	}
	d.set("providers", &jsonDoc{keys: l.order, members: l.byKey})
}

func implodeProviders(d *jsonDoc) {
	list := d.member("providers")
	if !list.isObject() {
		return
	}
	els := make([]json.RawMessage, 0, len(list.keys))
	for _, k := range list.keys {
		els = append(els, list.members[k].compact())
	}
	raw, _ := json.Marshal(els)
	d.set("providers", &jsonDoc{raw: raw})
}

func configPathName(path []string) string { return strings.Join(path, ".") }

func providersPathName(path []string) string {
	if len(path) >= 2 && path[0] == "providers" {
		return "providers[" + strconv.Quote(path[1]) + "]" + strings.Join(append([]string{""}, path[2:]...), ".")
	}
	return strings.Join(path, ".")
}

var identityPaths = [][]string{{"identity", "ledger_path"}, {"identity", "db_path"}, {"identity", "key_path"}}

func configWrittenWhole(d *jsonDoc) bool {
	config, _ := shippedSets()
	for _, p := range identityPaths {
		v := d.member(p[0]).member(p[1])
		if v == nil || !config.shipped(p, v) {
			return false
		}
	}
	return true
}

var keptConfigPaths = [][]string{{"contacts"}, {"dashboard", "port"}}

func stripConfig(data []byte, inForce *Config) (out []byte, taken []string, err error) {
	d, err := parseJSONDoc(data)
	if err != nil || !d.isObject() || !configWrittenWhole(d) {
		return nil, nil, err
	}
	config, _ := shippedSets()
	var paths [][]string
	docLeaves(d, nil, func(path []string, v *jsonDoc) {
		for _, kept := range keptConfigPaths {
			if slices.Equal(path[:min(len(kept), len(path))], kept) {
				return
			}
		}
		if config.shipped(path, v) {
			paths = append(paths, path)
		}
	})
	for _, p := range paths {
		removeAt(d, p, 0)
		taken = append(taken, configPathName(p))
	}
	n, err := parseJSONDoc(inForce.stated)
	if err != nil {
		return nil, nil, err
	}
	carryRetired(d, n)
	dropUnstated(d, n, n, true)

	if stateLogDir(d, n) {
		taken = slices.DeleteFunc(taken, func(p string) bool { return p == configPathName([]string{"logs", "dir"}) })
	}
	return d.encode(), taken, nil
}

func stripProviders(data []byte) (out []byte, taken []string, err error) {
	d, err := parseJSONDoc(data)
	if err != nil || !d.isObject() {
		return nil, nil, err
	}
	explodeProviders(d)
	_, providers := shippedSets()
	var paths [][]string
	docLeaves(d, nil, func(path []string, v *jsonDoc) {
		if len(path) >= 3 && path[0] == "providers" && isChoiceField(path[2]) {
			return
		}
		if providers.shipped(path, v) {
			paths = append(paths, path)
		}
	})
	if len(paths) == 0 {
		return nil, nil, nil
	}
	shippedView, err := providersView(embeddedProviders)
	if err != nil {
		return nil, nil, err
	}

	for len(paths) > 0 {
		trial, err := parseJSONDoc(d.compact())
		if err != nil {
			return nil, nil, err
		}
		for _, p := range paths {
			keep := 0
			if p[0] == "providers" {
				keep = 2
			}
			removeAt(trial, p, keep)
		}
		implodeProviders(trial)
		after, err := providersView(trial.encode())
		if err != nil {
			return nil, nil, nil
		}
		kept := slices.DeleteFunc(slices.Clone(paths), func(p []string) bool {
			want, ok := shippedView[pathKey(p)]
			if !ok && p[0] == "providers" {
				return false
			}
			return after[pathKey(p)] == want
		})
		if len(kept) == 0 {
			for _, p := range paths {
				taken = append(taken, providersPathName(p))
			}
			return trial.encode(), taken, nil
		}
		paths = slices.DeleteFunc(paths, func(p []string) bool {
			return slices.ContainsFunc(kept, func(k []string) bool { return slices.Equal(k, p) })
		})
	}
	return nil, nil, nil
}

func providersView(data []byte) (map[string]string, error) {
	reg, err := decodeProviders(ProvidersFileName, data)
	if err != nil {
		return nil, err
	}
	view := map[string]string{}
	add := func(prefix []string, v any) {
		raw, err := json.Marshal(v)
		if err != nil {
			return
		}
		d, err := docOf(raw)
		if err != nil {
			return
		}
		docLeaves(d, prefix, func(path []string, v *jsonDoc) {
			view[pathKey(path)] = string(canonicalJSON(v.compact()))
		})
	}
	for _, e := range reg.Providers {
		add([]string{"providers", e.Name}, e)
	}
	eff := reg.effective()
	for m, row := range eff.models {
		add([]string{"model_capabilities", m}, row)
	}
	for dialect, floor := range eff.floors {
		add([]string{"dialect_effort_floor", dialect}, floor)
	}
	add([]string{"capability_lookup"}, reg.capabilityLookup())
	for name, contract := range reg.oauthContracts() {
		add([]string{"oauth", name}, contract)
	}
	if reg.ModelCatalogueURL != nil {
		add([]string{"model_catalogue_url"}, *reg.ModelCatalogueURL)
	}
	return view, nil
}

func (a *App) stripShipped() {
	cfg := a.configSnapshot()
	path := cfg.SourcePath
	if path == "" {
		return
	}
	data, err := atomicfile.ReadFile(path)
	if err != nil {
		return
	}
	out, taken, err := stripConfig(data, &cfg)
	if err != nil || out == nil {
		return
	}
	if _, err := writeFileAtomic(path, out); err != nil {
		logsink.Warn("config.error", "%s was written whole by an earlier release, and taking out the values no one changed could not be written: %v; it is read as it is", path, err)
		return
	}
	logsink.Info("config.decision", "%s was written whole by an earlier release, which put every default into it as though you had typed it: took out the %d values that still equal one a release shipped, which now follow this release's defaults (%s); a value of yours that differs from every shipped one stays",
		path, len(taken), strings.Join(taken, ", "))

	if fresh, err := decodeConfig(path, out); err == nil {
		a.cfgMu.Lock()
		if now, err := json.Marshal(a.cfg); err == nil && string(now) == string(a.cfg.stated) {
			*a.cfg = *fresh
		}
		a.cfgMu.Unlock()
	} else {
		logsink.Warn("config.error", "%s, with the shipped values taken out, does not load (%v): the configuration read before stays in force for this run", path, err)
	}

	ppath := providerFilePath(path)
	pdata, err := atomicfile.ReadFile(ppath)
	if err != nil {
		return
	}
	pout, ptaken, err := stripProviders(pdata)
	if err != nil || pout == nil {
		return
	}
	if _, err := writeFileAtomic(ppath, pout); err != nil {
		logsink.Warn("providers.error", "%s: taking out the vendor facts an earlier release copied into it could not be written: %v; it is read as it is", ppath, err)
		return
	}
	logsink.Info("providers.decision", "%s: took out the %d vendor facts an earlier release copied into it that still equal one a release shipped, which this release now lends at each load (%s); a value of yours that differs from every shipped one stays",
		ppath, len(ptaken), strings.Join(ptaken, ", "))
}
