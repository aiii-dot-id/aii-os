package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var testSeams = []string{"internal/genesis/genesislive", "internal/genesis/genesistest"}

// shipped reports whether rel is a package a release builds a product
// from: a command, the mobile binding or the export tool.
func shipped(rel string) bool {
	return strings.HasPrefix(rel, "cmd/") || rel == "mobile" || rel == "export"
}

// seamChains returns, for each test seam in pkg's non-test closure, one
// import chain that reaches it.
func seamChains(g importGraph, pkg string) map[string][]string {
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
	for _, seam := range testSeams {
		if _, reached := parent[modulePrefix+seam]; !reached {
			continue
		}
		var chain []string
		for n := modulePrefix + seam; n != ""; n = parent[n] {
			chain = append([]string{strings.TrimPrefix(n, modulePrefix)}, chain...)
		}
		found[seam] = chain
	}
	return found
}

func TestNoShippedCommandLinksATestSeam(t *testing.T) {
	root := repoRoot(t)
	for _, row := range platformRows {
		g := listImportGraph(t, root, row, "", row.patterns)
		products := 0
		for pkg := range g {
			rel, ok := strings.CutPrefix(pkg, modulePrefix)
			if !ok || !shipped(rel) {
				continue
			}
			products++
			for seam, chain := range seamChains(g, pkg) {
				t.Errorf("%s: %s links the test seam %s: %s", row, rel, seam, strings.Join(chain, " -> "))
			}
		}
		if products == 0 {
			t.Errorf("%s: no shipped package was listed: the guard read nothing", row)
		}
	}
}

// A guard that cannot fail is indistinguishable from one that passed: by
// overlay, the app is given an import of the seam, and the command that
// links the app must be refused for both seams (the one imports the
// other).
func TestTheTestSeamGuardRefusesWhatItIsFor(t *testing.T) {
	root := repoRoot(t)
	dir := t.TempDir()
	probe := filepath.Join(dir, "probe.go")
	if err := os.WriteFile(probe, []byte("package app\n\nimport _ \""+modulePrefix+"internal/genesis/genesistest\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	overlay, err := json.Marshal(map[string]map[string]string{"Replace": {filepath.Join(root, "internal", "app", "zz_seam_probe.go"): probe}})
	if err != nil {
		t.Fatal(err)
	}
	overlayPath := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(overlayPath, overlay, 0o600); err != nil {
		t.Fatal(err)
	}
	g := listImportGraph(t, root, platformRows[0], overlayPath, []string{"./cmd/aii"})
	chains := seamChains(g, modulePrefix+"cmd/aii")
	for _, seam := range testSeams {
		if len(chains[seam]) == 0 {
			t.Errorf("the guard did not see %s linked through the app", seam)
		}
	}
}
