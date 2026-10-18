package test

// layering_test.go — the layers point down, and the semantic core's
// closure holds no engine.
//
// Every package in the module has a level, and a package imports only
// packages at its own level or below. The levels are the ones the
// structure plan names (primitives; the record; values; the projection;
// semantics; runtime; features; the composition root; surfaces). On the
// day this guard landed the graph was acyclic and exactly two edges
// pointed up, both cognitive's (to llm and to memory); both closed the
// same day, when what cognitive named moved to leaves under llm and
// memory. An edge that points up must be listed in upwardToday with what
// it carries and what closes it, and the list only shrinks: an entry no
// platform row shows any more is stale and is refused, so a fixed edge
// cannot quietly re-open. The list is empty and stays, so that a new
// upward edge is refused rather than listed.
//
// The second rule is an EXCLUSION, and it claims nothing more: for each
// package named in excludesEngines, on every platform row the gate
// builds, the transitive import closure contains none of database/sql,
// modernc.org/sqlite, net/http or os/exec — a database engine, an HTTP
// client, a subprocess. It does not say "no I/O": os is in every core
// package's closure (through logsink and crypto), and so is file I/O.
// It proves nothing about authority, purity of behaviour, failure
// semantics or lifecycle; those are the behavioural tests' job. A
// package joins excludesEngines the day its prerequisite lands, not
// before.
//
// Both rules run on six rows because the graph differs by platform
// (darwin adds oauth -> hostcap, windows adds app -> consolewin, mobile
// drops install -> hostcap). A go list failure is a test failure. And a
// guard that cannot fail is indistinguishable from one that passed, so
// TestTheLayeringGuardRefusesWhatItIsFor shows both rules a violation on
// every row, through go list overlays that add a synthetic import and
// synthetic packages without writing a byte into the checkout.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const modulePrefix = "github.com/aiii-dot-id/aii-os/"

var levelNames = []string{"primitive", "record", "values", "projection", "semantics", "runtime", "feature", "composition", "surface"}

// layers is the level of every package in the module. A new package is
// declared here the day it lands; an undeclared one is refused.
var layers = map[string]int{
	// 0 primitives — no domain, importable by anyone.
	"internal/atomicfile": 0, "internal/canonicaljson": 0, "internal/crypto": 0, "internal/sigenvelope": 0,
	"internal/fileperm": 0, "internal/filelock": 0, "internal/logsink": 0, "internal/hostcap": 0, "internal/untrusted": 0,
	"internal/vulkancap": 0, "internal/vulkancap/driver": 0, // isolated OS/driver facts; no identity or plugin policy
	"internal/quiesce": 0, "internal/tokenestimate": 0, "internal/version": 0, "internal/jsonschema": 0, "internal/browsertest": 0,
	"internal/bbb": 0, "internal/consolewin": 0, "internal/fsdir": 0, "internal/foreground": 0, "internal/retryafter": 0,
	"internal/store/compressvfs": 0, "internal/memory/actr": 0, "internal/memory/carrd": 0,
	"internal/memory/fuse": 0, "internal/memory/trigram": 0, "internal/memory/vec": 0,
	"internal/pluginworker/wasmgen": 0, "internal/genesis/genesislive": 0, "config": 0, "packaging/icon": 0,
	"internal/tlsfault": 0, "internal/tlsfault/tlsfaulttest": 0,
	// 1 the record.
	"internal/ledger": 1,
	// 2 values shared by record owners — and every leaf carved out of an
	// owner to close an edge (store/rows, store/cursor, llm/wire,
	// memory/salience, memory/attention): it sits here with the values it
	// serves, whatever it imports, because it carries its owner's shapes
	// or policy, not a primitive.
	"internal/interaction": 2, "internal/store/cursor": 2, "internal/store/rows": 2, "internal/llm/wire": 2,
	"internal/memory/salience": 2, "internal/memory/attention": 2,
	// 3 the projection.
	"internal/store": 3,
	// 4 semantics — what the identity is and reads.
	"internal/ring": 4, "internal/prompt": 4, "internal/cognitive": 4, "internal/cognitive/landing": 4,
	"internal/genesis": 4,
	// 5 runtime — the organs that act, fetch, supervise and speak.
	"internal/identity": 5, "internal/conversation": 5, "internal/memory": 5, "internal/llm": 5,
	"internal/tools": 5, "internal/firewall": 5, "internal/broker": 5, "internal/pluginhost": 5,
	"internal/pluginworker": 5, "internal/workercmd": 5, "internal/supervisor": 5, "internal/witness": 5,
	"internal/witness/witnesstest": 5, "internal/pluginhost/pluginhosttest": 5, "internal/certs": 5, "internal/relay": 5, "internal/relay/relaytest": 5,
	"internal/updates": 5, "internal/audio": 5, "internal/speech": 5, "internal/speecheval": 5, "internal/memory/memoryeval": 5, "internal/acceptance": 5,
	"internal/oauth": 5, "internal/escrow": 5, "internal/project": 5, "internal/sections": 5,
	"internal/packagefmt": 5, "internal/packagefmt/packagetest": 5,
	"internal/pluginfacility": 5, "internal/googleoidc": 5, "internal/install": 5, "internal/genesis/genesistest": 5,
	"internal/machine":   5, // the account's machine service and an identity's link to it (engine split, L1a)
	"internal/plugstore": 5,
	// 6 features the composition root wires.
	"internal/dashboard": 6, "internal/plugincatalog": 6,
	"internal/publicname": 6, "internal/publicname/publicnametest": 6,
	"internal/continuity": 6, "internal/continuity/continuitytest": 6, // continuity: the pass, snapshots, restore, escrow gate (T23, Wave 4)

	"internal/uninstall": 6,
	// 7 the composition root.
	"internal/app": 7,
	// 8 surfaces.
	"cmd/aii": 8, "cmd/aii-app": 8, "cmd/aii-devsign": 8, "cmd/aii-plugin-worker": 8, "cmd/aii-release": 8,
	"cmd/aii-setup": 8, "cmd/aii-speech-eval": 8, "cmd/aii-vulkan-probe": 8, "cmd/rewrap": 8, "cmd/verify": 8, "export": 8, "mobile": 8, "test": 8,
	"test/mobilehandoffprobe": 8,
	"test/decls":              8, // the pure-move proof tool: it reads Go source and imports nothing of the module
	"test/skipreport":         8, // reads what go test printed (skips, release-critical tests): the standard library only
}

var upwardToday = map[[2]string]string{}

// engines are what the semantic core's closure must not contain.
var engines = []string{"database/sql", "modernc.org/sqlite", "net/http", "os/exec"}

var excludesEngines = []string{"internal/ring", "internal/prompt", "internal/cognitive", "internal/cognitive/landing"}

type platformRow struct {
	goos, goarch string
	patterns     []string
}

// The rows the repo ships for, and the one row var of package test: the
// scripts' ROWS (test/toolchain.sh) plus ios. ios lists what the matrix
// builds there: the cmd mains need cgo linking on ios and are shipped
// through gomobile, so go list reports a link-mode error on them that is
// not a graph error.
var platformRows = []platformRow{
	{"linux", "amd64", []string{"./..."}},
	{"linux", "arm64", []string{"./..."}},
	{"darwin", "arm64", []string{"./..."}},
	{"windows", "amd64", []string{"./..."}},
	{"android", "arm64", []string{"./..."}},
	{"ios", "arm64", []string{"./internal/...", "./mobile/..."}},
}

func (r platformRow) String() string { return r.goos + "/" + r.goarch }

// The scripts and the tests read the tree as the same rows: the scripts'
// ROWS are platformRows less ios, which they cannot link (toolchain.sh
// says why). The rows had drifted once already, three ways.
func TestTheScriptsReadTheTreeAsTheTestsRows(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRoot(t), "test", "toolchain.sh"))
	if err != nil {
		t.Fatal(err)
	}
	var scripts string
	for _, line := range strings.Split(string(src), "\n") {
		if v, ok := strings.CutPrefix(line, "ROWS="); ok {
			scripts = strings.Trim(v, `"`)
		}
	}
	var tests []string
	for _, r := range platformRows {
		if r.goos != "ios" {
			tests = append(tests, r.String())
		}
	}
	if scripts != strings.Join(tests, " ") {
		t.Errorf("test/toolchain.sh reads the tree as %q; platformRows less ios is %q", scripts, strings.Join(tests, " "))
	}
}

// importGraph maps an import path to its non-test imports, for every
// package in the listed closure (standard library and modules included).
type importGraph map[string][]string

func listImportGraph(t *testing.T, root string, row platformRow, overlay string, patterns []string) importGraph {
	t.Helper()
	args := []string{"list", "-e", "-deps", "-f", "{{.ImportPath}}\t{{join .Imports \" \"}}\t{{if .Error}}{{.Error}}{{end}}"}
	if overlay != "" {
		args = append(args, "-overlay="+overlay)
	}
	cmd := exec.Command(goToolPath(), append(args, patterns...)...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "CGO_ENABLED=0", "GOOS="+row.goos, "GOARCH="+row.goarch)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s: go list failed (a listing failure is a guard failure): %v\n%s", row, err, stderr.String())
	}
	g := importGraph{}
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		f := strings.SplitN(line, "\t", 3)
		if len(f) < 2 {
			t.Fatalf("%s: unreadable go list line %q", row, line)
		}
		if len(f) == 3 && f[2] != "" {
			t.Fatalf("%s: go list reports %s: %s", row, f[0], f[2])
		}
		g[f[0]] = strings.Fields(f[1])
	}
	return g
}

type findingKind int

const (
	undeclaredPackage findingKind = iota
	upwardEdge
	staleUpwardEntry
	staleLayer
)

type finding struct {
	kind     findingKind
	from, to string
}

func (f finding) String() string {
	switch f.kind {
	case undeclaredPackage:
		return fmt.Sprintf("%s has no level: declare it in layers (test/layering_test.go) the day it lands", f.from)
	case upwardEdge:
		return fmt.Sprintf("%s (%s) imports %s (%s): a package imports only packages at its own level or below",
			f.from, levelNames[layers[f.from]], f.to, levelNames[layers[f.to]])
	case staleUpwardEntry:
		return fmt.Sprintf("upwardToday still lists %s -> %s, which no platform row has any more: remove the entry, so the edge cannot quietly re-open", f.from, f.to)
	case staleLayer:
		return fmt.Sprintf("layers declares %s, which no platform row lists: remove it", f.from)
	}
	return "unknown finding"
}

// observe collects, from one row's graph, the module packages it lists
// and every upward or undeclared edge among them.
func observe(g importGraph, packages map[string]bool, up map[[2]string]bool, undeclared map[string]bool) {
	for p, imports := range g {
		from, ok := strings.CutPrefix(p, modulePrefix)
		if !ok {
			continue
		}
		packages[from] = true
		lf, declared := layers[from]
		if !declared {
			undeclared[from] = true
			continue
		}
		for _, imp := range imports {
			to, ok := strings.CutPrefix(imp, modulePrefix)
			if !ok {
				continue
			}
			if lt, declared := layers[to]; declared && lt > lf {
				up[[2]string{from, to}] = true
			}
		}
	}
}

// judge turns what the rows showed into findings, sorted.
func judge(packages map[string]bool, up map[[2]string]bool, undeclared map[string]bool, known map[[2]string]string) []finding {
	var out []finding
	for p := range undeclared {
		out = append(out, finding{kind: undeclaredPackage, from: p})
	}
	for e := range up {
		if _, listed := known[e]; !listed {
			out = append(out, finding{kind: upwardEdge, from: e[0], to: e[1]})
		}
	}
	for e := range known {
		if !up[e] {
			out = append(out, finding{kind: staleUpwardEntry, from: e[0], to: e[1]})
		}
	}
	for p := range layers {
		if !packages[p] {
			out = append(out, finding{kind: staleLayer, from: p})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// engineChains returns, for each engine in pkg's closure, one import
// chain that reaches it — the chain is the finding's evidence.
func engineChains(g importGraph, pkg string) map[string][]string {
	parent := map[string]string{pkg: ""}
	queue := []string{pkg}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, imp := range g[p] {
			if _, seen := parent[imp]; !seen {
				parent[imp] = p
				queue = append(queue, imp)
			}
		}
	}
	found := map[string][]string{}
	for _, e := range engines {
		if _, reached := parent[e]; !reached {
			continue
		}
		var chain []string
		for n := e; n != ""; n = parent[n] {
			chain = append([]string{strings.TrimPrefix(n, modulePrefix)}, chain...)
		}
		found[e] = chain
	}
	return found
}

// guard:tree
func TestTheLayersPointDown(t *testing.T) {
	root := repoRoot(t)
	packages, up, undeclared := map[string]bool{}, map[[2]string]bool{}, map[string]bool{}
	for _, row := range platformRows {
		observe(listImportGraph(t, root, row, "", row.patterns), packages, up, undeclared)
	}
	for _, f := range judge(packages, up, undeclared, upwardToday) {
		t.Error(f)
	}
}

// guard:tree
func TestTheSemanticCoreExcludesTheEngines(t *testing.T) {
	root := repoRoot(t)
	for _, row := range platformRows {
		g := listImportGraph(t, root, row, "", row.patterns)
		for _, pkg := range excludesEngines {
			if _, listed := g[modulePrefix+pkg]; !listed {
				t.Errorf("%s: %s is held to the exclusion but the row does not list it", row, pkg)
				continue
			}
			for engine, chain := range engineChains(g, modulePrefix+pkg) {
				t.Errorf("%s: %s reaches %s: %s", row, pkg, engine, strings.Join(chain, " -> "))
			}
		}
	}
}

// guard:tree
func TestTheLayeringGuardRefusesWhatItIsFor(t *testing.T) {
	root := repoRoot(t)
	dir := t.TempDir()
	write := func(name, src string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// One synthetic upward import into a real package (values -> semantics),
	// one synthetic package per engine imported directly, and one that
	// reaches the engines only through a real package.
	replace := map[string]string{
		filepath.Join(root, "internal", "interaction", "zz_layering_probe.go"): write("up.go",
			"package interaction\n\nimport _ \""+modulePrefix+"internal/ring\"\n"),
	}
	probes := map[string][]string{} // synthetic package -> engines it must be refused for
	for i, e := range engines {
		name := fmt.Sprintf("zzlayeringprobe%d", i)
		replace[filepath.Join(root, "internal", "ring", name, "p.go")] = write(name+".go", "package "+name+"\n\nimport _ \""+e+"\"\n")
		probes["internal/ring/"+name] = []string{e}
	}
	replace[filepath.Join(root, "internal", "ring", "zzlayeringprobestore", "p.go")] = write("store.go",
		"package zzlayeringprobestore\n\nimport _ \""+modulePrefix+"internal/store\"\n")
	probes["internal/ring/zzlayeringprobestore"] = []string{"database/sql", "modernc.org/sqlite"}
	overlay, err := json.Marshal(map[string]map[string]string{"Replace": replace})
	if err != nil {
		t.Fatal(err)
	}
	overlayPath := write("overlay.json", string(overlay))
	patterns := []string{"./internal/interaction"}
	for p := range probes {
		patterns = append(patterns, "./"+p)
	}

	for _, row := range platformRows {
		g := listImportGraph(t, root, row, overlayPath, patterns)

		packages, up, undeclared := map[string]bool{}, map[[2]string]bool{}, map[string]bool{}
		observe(g, packages, up, undeclared)
		got := map[findingKind]map[string]bool{}
		for _, f := range judge(packages, up, undeclared, map[[2]string]string{}) {
			if got[f.kind] == nil {
				got[f.kind] = map[string]bool{}
			}
			got[f.kind][f.from+" -> "+f.to] = true
		}
		if !got[upwardEdge]["internal/interaction -> internal/ring"] {
			t.Errorf("%s: a synthetic values -> semantics import was not refused as an upward edge", row)
		}
		for p := range probes {
			if !got[undeclaredPackage][p+" -> "] {
				t.Errorf("%s: synthetic package %s has no level and was not refused", row, p)
			}
			reached := engineChains(g, modulePrefix+p)
			for _, e := range probes[p] {
				if _, ok := reached[e]; !ok {
					t.Errorf("%s: %s imports %s (directly or through store) and the exclusion did not see it", row, p, e)
				}
			}
		}
	}

	// The allowlist only shrinks: an entry no row shows is refused.
	gone := [2]string{"internal/cognitive", "internal/llm"}
	stale := judge(map[string]bool{}, map[[2]string]bool{}, map[string]bool{}, map[[2]string]string{gone: "closed"})
	found := false
	for _, f := range stale {
		if f.kind == staleUpwardEntry && f.from == gone[0] && f.to == gone[1] {
			found = true
		}
	}
	if !found {
		t.Error("an upwardToday entry that no row shows was not refused as stale")
	}
}

var appMustNotImport = []string{"internal/certs", "internal/relay", "internal/relay/relaytest", "internal/escrow", "internal/supervisor"}

// appMustNotImportOwner is the package that owns each entry, for the
// refusal's words; its test kit is that path plus its last element and
// "test" (internal/publicname/publicnametest).
var appMustNotImportOwner = map[string]string{
	"internal/certs":           "internal/publicname",
	"internal/relay":           "internal/publicname",
	"internal/relay/relaytest": "internal/publicname",
	"internal/escrow":          "internal/continuity",
	"internal/supervisor":      "internal/pluginhost",
}

// listAppImports is every import of internal/app's own files on one row:
// the package's, its in-package tests' and its external tests'.
func listAppImports(t *testing.T, root string, row platformRow, overlay string) []string {
	t.Helper()
	args := []string{"list", "-e", "-f", "{{if .Error}}ERROR {{.Error}}{{else}}{{join .Imports \" \"}} {{join .TestImports \" \"}} {{join .XTestImports \" \"}}{{end}}"}
	if overlay != "" {
		args = append(args, "-overlay="+overlay)
	}
	cmd := exec.Command(goToolPath(), append(args, "./internal/app")...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "CGO_ENABLED=0", "GOOS="+row.goos, "GOARCH="+row.goarch)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s: go list failed (a listing failure is a guard failure): %v\n%s", row, err, stderr.String())
	}
	line := strings.TrimSpace(string(out))
	if strings.HasPrefix(line, "ERROR ") {
		t.Fatalf("%s: go list reports internal/app: %s", row, strings.TrimPrefix(line, "ERROR "))
	}
	return strings.Fields(line)
}

// forbiddenAppImports is the appMustNotImport entries among imports,
// sorted and once each.
func forbiddenAppImports(imports []string) []string {
	have := map[string]bool{}
	for _, imp := range imports {
		have[strings.TrimPrefix(imp, modulePrefix)] = true
	}
	var out []string
	for _, p := range appMustNotImport {
		if have[p] {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// guard:tree
func TestTheAppImportsNoFeatureInternals(t *testing.T) {
	root := repoRoot(t)
	for _, row := range platformRows {
		imports := listAppImports(t, root, row, "")
		if len(imports) < 50 {
			t.Fatalf("%s: read %d imports of internal/app — the guard is not reading the package", row, len(imports))
		}
		for _, p := range forbiddenAppImports(imports) {
			owner := appMustNotImportOwner[p]
			t.Errorf("%s: internal/app imports %s in its own files: it is %s's — reach it through that package (or, in a test, its kit %s/%stest)", row, p, owner, owner, filepath.Base(owner))
		}
	}
}

// guard:tree
func TestTheAppImportGuardRefusesWhatItIsFor(t *testing.T) {
	root := repoRoot(t)
	dir := t.TempDir()
	write := func(name, src string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// One forbidden import in each kind of file the package has: its own
	// code, an in-package test and an external test.
	app := filepath.Join(root, "internal", "app")
	replace := map[string]string{
		filepath.Join(app, "zz_import_probe.go"): write("probe.go",
			"package app\n\nimport (\n\t_ \""+modulePrefix+"internal/certs\"\n\t_ \""+modulePrefix+"internal/escrow\"\n)\n"),
		filepath.Join(app, "zz_import_probe_test.go"): write("probe_test.go",
			"package app\n\nimport (\n\t_ \""+modulePrefix+"internal/relay\"\n\t_ \""+modulePrefix+"internal/supervisor\"\n)\n"),
		filepath.Join(app, "zz_import_probe_x_test.go"): write("probe_x_test.go",
			"package app_test\n\nimport _ \""+modulePrefix+"internal/relay/relaytest\"\n"),
	}
	overlay, err := json.Marshal(map[string]map[string]string{"Replace": replace})
	if err != nil {
		t.Fatal(err)
	}
	overlayPath := write("overlay.json", string(overlay))
	want := append([]string(nil), appMustNotImport...)
	sort.Strings(want)
	for _, row := range platformRows {
		got := forbiddenAppImports(listAppImports(t, root, row, overlayPath))
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("%s: synthetic imports of %v in internal/app's code, test and external test were refused as %v", row, want, got)
		}
	}
}
