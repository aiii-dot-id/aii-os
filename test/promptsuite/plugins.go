package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/promptsuite"
)

type pluginSource interface {
	install(t *promptsuite.Task, slot string) (config map[string]any, roles []bound, err error)
}

// bound is the plugin a trial's package gives a role.
type bound struct {
	role string
	id   string // the package's verified plugin id
}

// rolesFile is the optional file in a run's plugins directory that names
// the package for a role by its file name, where the operations alone do
// not single one out: {"memory": "<file>.aiiospkg", ...}.
const rolesFile = "roles.json"

// published is the plugins a live run measures: the published packages
// the operator hands the runner in a directory outside the tree, one per
// role. The suite measures the plugins' own descriptions as part of the
// prompt, so a live run takes them from nowhere else; the catalog stays
// off, and each package is installed as one copied into plugins/ by hand
// is, with the grants an install from the catalog gives it.
type published struct {
	dir   string
	roles map[string]*pkg // role -> its package
}

// pkg is one published package, as its signature proved it.
type pkg struct {
	file     string // its file name in the run's plugins directory
	path     string
	id       string
	version  string
	sha256   string
	tier     packagefmt.Tier
	methods  map[string]bool
	envelope []string // the capabilities its signed envelope asks for
}

// errNoPlugins refuses a live run whose tasks need plugins it was not
// given: a fixture would be another plugin's description, and so another
// prompt than the one measured.
var errNoPlugins = errors.New("needs the published plugin packages: name the directory that holds them with -plugins (the suite measures the plugins' own descriptions as part of the prompt; fixture plugins are for the offline proofs only)")

// shippedTrust is the trust this build ships, with the revocation status
// it ships for it: what `aii plugin verify` checks a package against when
// it is named nothing else, and what a host trusts before an operator
// configures anything.
func shippedTrust() packagefmt.TrustRoots {
	roots := packagefmt.ShippedRoots()
	roots.Revocation = packagefmt.ShippedRevocationStatus(roots)
	return roots
}

func loadPublished(dir string, roles []promptsuite.Role, roots packagefmt.TrustRoots) (*published, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.aiiospkg"))
	if err != nil {
		return nil, err
	}
	var pkgs []*pkg
	for _, file := range files {
		p, err := readPackage(file, roots)
		if err != nil {
			return nil, err
		}
		pkgs = append(pkgs, p)
	}
	named := map[string]string{}
	if raw, err := os.ReadFile(filepath.Join(dir, rolesFile)); err == nil {
		if err := canonicaljson.DecodeStrict(raw, &named); err != nil {
			return nil, fmt.Errorf("%s: %w", rolesFile, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	out := &published{dir: dir, roles: map[string]*pkg{}}
	for _, r := range roles {
		var fits []*pkg
		for _, p := range pkgs {
			if file, ok := named[r.Role]; ok {
				if p.file == file {
					fits = append(fits, p)
				}
				continue
			}
			if p.declares(r.Operations) {
				fits = append(fits, p)
			}
		}
		switch {
		case len(fits) == 1 && fits[0].declares(r.Operations):
			out.roles[r.Role] = fits[0]
		case len(fits) == 1:
			return nil, fmt.Errorf("%s names %s for the %s role, and it does not declare %s", rolesFile, fits[0].file, r.Role, strings.Join(r.Operations, ", "))
		case len(fits) == 0:
			return nil, fmt.Errorf("no package in %s declares the %s role's operations (%s)", dir, r.Role, strings.Join(r.Operations, ", "))
		default:
			var names []string
			for _, p := range fits {
				names = append(names, p.file)
			}
			return nil, fmt.Errorf("%d packages in %s declare the %s role's operations (%s): name one in %s", len(fits), dir, r.Role, strings.Join(names, ", "), rolesFile)
		}
	}
	return out, nil
}

func readPackage(file string, roots packagefmt.TrustRoots) (*pkg, error) {
	res, err := packagefmt.VerifyFile(file, roots)
	if err != nil {
		return nil, fmt.Errorf("%s does not verify against the roots this build trusts: %w", filepath.Base(file), err)
	}
	if res.Tier < packagefmt.TierT1 || res.Manifest == nil || res.Manifest.Interfaces == nil {
		return nil, fmt.Errorf("%s is not a signed plugin package (tier %s): a published package is signed, and the host loads no other by default", filepath.Base(file), res.Tier)
	}
	blob, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(blob)
	p := &pkg{file: filepath.Base(file), path: file, id: res.Manifest.ID, version: res.Manifest.Version,
		sha256: hex.EncodeToString(sum[:]), tier: res.Tier, methods: map[string]bool{}}
	for _, decl := range append(append([]packagefmt.InterfaceDecl{}, res.Manifest.Interfaces.Core...), res.Manifest.Interfaces.Optional...) {
		for _, m := range decl.Methods {
			p.methods[m] = true
		}
	}
	if len(res.Manifest.CapabilityEnvelope) > 0 {
		if err := json.Unmarshal(res.Manifest.CapabilityEnvelope, &p.envelope); err != nil {
			return nil, fmt.Errorf("%s: its capability envelope: %w", p.file, err)
		}
	}
	return p, nil
}

func (p *pkg) declares(ops []string) bool {
	for _, op := range ops {
		if !p.methods[op] {
			return false
		}
	}
	return true
}

// install copies each role's package into the slot's plugins/<id>/ and
// gives it, in the configuration, every grant its signed envelope asks
// for (broker.InstallGrant, the rule an install from the catalog applies)
// but those the task withholds.
func (pp *published) install(t *promptsuite.Task, slot string) (map[string]any, []bound, error) {
	if len(t.Setup.Plugins) == 0 {
		return nil, nil, nil
	}
	grants := map[string]any{}
	var out []bound
	for _, role := range t.Setup.Plugins {
		p := pp.roles[role]
		if p == nil {
			return nil, nil, fmt.Errorf("task %s %w", t.ID, errNoPlugins)
		}
		dir := filepath.Join(slot, "plugins", p.id)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, nil, err
		}
		if err := copyFile(filepath.Join(dir, p.file), p.path); err != nil {
			return nil, nil, err
		}
		withheld := map[string]bool{}
		for _, g := range t.Setup.Withhold[role] {
			withheld[g] = true
		}
		given := map[string]any{}
		for _, c := range p.envelope {
			if field := broker.InstallGrant(c); field != "" && !withheld[field] {
				given[field] = true
			}
		}
		if len(given) > 0 {
			grants[p.id] = given
		}
		out = append(out, bound{role: role, id: p.id})
	}
	return map[string]any{"plugins": map[string]any{"grants": grants}}, out, nil
}

// pluginRecord is one bound package as a run's results record it: the
// file the operator handed, the version and the hash, outside the tree.
type pluginRecord struct {
	Role    string `json:"role"`
	File    string `json:"file"`
	ID      string `json:"id"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	Tier    string `json:"tier"`
}

// records is every bound package, by role.
func (pp *published) records() []pluginRecord {
	var out []pluginRecord
	for role, p := range pp.roles {
		out = append(out, pluginRecord{Role: role, File: p.file, ID: p.id, Version: p.version, SHA256: p.sha256, Tier: p.tier.String()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Role < out[j].Role })
	return out
}

// label is how a verdict names the run's packages: role, file, version
// and the first twelve hex of the hash, by role.
func (pp *published) label() string {
	var parts []string
	for _, r := range pp.records() {
		parts = append(parts, fmt.Sprintf("%s=%s %s sha256:%s", r.Role, r.File, r.Version, r.SHA256[:12]))
	}
	return strings.Join(parts, "; ")
}

// noPlugins is the source of a run whose tasks name no plugin role: a
// task that names one is refused with why.
type noPlugins struct{}

func (noPlugins) install(t *promptsuite.Task, _ string) (map[string]any, []bound, error) {
	if len(t.Setup.Plugins) > 0 {
		return nil, nil, fmt.Errorf("task %s %w", t.ID, errNoPlugins)
	}
	return nil, nil, nil
}

// pluginTools names the tool of each operation each bound role declares
// in the manifest, by "role.operation": what the checks' {role.operation}
// stand for.
func pluginTools(m *promptsuite.Manifest, roles []bound) map[string]string {
	out := map[string]string{}
	for _, b := range roles {
		r := m.Role(b.role)
		if r == nil {
			continue
		}
		for _, op := range r.Operations {
			out[b.role+"."+op] = pluginhost.ToolNameFor(b.id, op)
		}
	}
	return out
}
