package test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// staticOnLinux is every executable the Linux packages ship except the probe.
var staticOnLinux = []string{"cmd/aii", "cmd/aii-app"}

const puregoModule = "github.com/ebitengine/purego"

var dynamicImport = regexp.MustCompile(`(?m)^//go:cgo_import_dynamic\s`)

type linkedPackage struct {
	imports []string
	files   []string // non-test Go files, absolute
}

// linuxGraph is the CGO_ENABLED=0 linux/goarch closure of the packages.
func linuxGraph(t *testing.T, root, goarch, overlay string, packages ...string) map[string]linkedPackage {
	t.Helper()
	args := []string{"list", "-e", "-deps", "-f", "{{.ImportPath}}\t{{if .Error}}{{.Error}}{{end}}\t{{join .Imports \" \"}}\t{{.Dir}}{{range .GoFiles}}\t{{.}}{{end}}"}
	if overlay != "" {
		args = append(args, "-overlay="+overlay)
	}
	for _, p := range packages {
		args = append(args, "./"+p)
	}
	cmd := exec.Command(goToolPath(), args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+goarch)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("linux/%s: go list failed (a listing failure is a guard failure): %v\n%s", goarch, err, stderr.String())
	}
	g := map[string]linkedPackage{}
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 4 {
			t.Fatalf("linux/%s: unreadable go list line %q", goarch, line)
		}
		if f[1] != "" {
			t.Fatalf("linux/%s: go list reports %s: %s", goarch, f[0], f[1])
		}
		pkg := linkedPackage{imports: strings.Fields(f[2])}
		for _, name := range f[4:] {
			pkg.files = append(pkg.files, filepath.Join(f[3], name))
		}
		g[f[0]] = pkg
	}
	return g
}

// whyDynamic names each package in g that would make its executable dynamic.
func whyDynamic(t *testing.T, g map[string]linkedPackage, read func(string) ([]byte, error)) map[string]string {
	t.Helper()
	why := map[string]string{}
	for p, pkg := range g {
		switch {
		case p == "runtime/cgo":
			why[p] = "is the cgo runtime"
			continue
		case p == puregoModule || strings.HasPrefix(p, puregoModule+"/"):
			why[p] = "is purego, whose cgo shim makes its executable dynamic"
			continue
		}
		for _, f := range pkg.files {
			src, err := read(f)
			if err != nil {
				t.Fatalf("%s: %v", p, err)
			}
			if dynamicImport.Match(src) {
				why[p] = filepath.Base(f) + " declares //go:cgo_import_dynamic"
				break
			}
		}
	}
	return why
}

// importChain is one path from the executable to p: the finding's evidence.
func importChain(g map[string]linkedPackage, from, p string) string {
	parent := map[string]string{from: ""}
	for queue := []string{from}; len(queue) > 0; queue = queue[1:] {
		for _, imp := range g[queue[0]].imports {
			if _, seen := parent[imp]; !seen {
				parent[imp] = queue[0]
				queue = append(queue, imp)
			}
		}
	}
	var chain []string
	for n := p; n != ""; n = parent[n] {
		chain = append([]string{strings.TrimPrefix(n, modulePrefix)}, chain...)
	}
	return strings.Join(chain, " -> ")
}

// guard:tree
func TestTheLinuxRuntimeLinksStatically(t *testing.T) {
	root := repoRoot(t)
	for _, goarch := range []string{"amd64", "arm64"} {
		for _, exe := range staticOnLinux {
			g := linuxGraph(t, root, goarch, "", exe)
			why := whyDynamic(t, g, os.ReadFile)
			var found []string
			for p := range why {
				found = append(found, p)
			}
			sort.Strings(found)
			for _, p := range found {
				t.Errorf("linux/%s %s would need an ELF interpreter and libc: %s %s (%s). A shared-library read belongs in its own executable, as cmd/aii-vulkan-probe does it",
					goarch, exe, strings.TrimPrefix(p, modulePrefix), why[p], importChain(g, modulePrefix+exe, p))
			}
		}
		// The probe is the one Linux executable that must be dynamic. If the
		// rule cannot see that, it sees nothing; if the probe stopped needing
		// it, the read moved again and this account of it is wrong.
		if len(whyDynamic(t, linuxGraph(t, root, goarch, "", "cmd/aii-vulkan-probe"), os.ReadFile)) == 0 {
			t.Errorf("linux/%s: cmd/aii-vulkan-probe links nothing dynamic: either this guard went blind or the Vulkan read left the probe", goarch)
		}
	}
}

// The rule is the mechanism: a package no list names, declaring one dynamic
// import, which reaches aii through one import line in a real package, is
// refused with its edge. Shown through a go list overlay, without writing a
// byte into the checkout.
//
// guard:tree
func TestTheStaticGuardRefusesAnyDynamicImport(t *testing.T) {
	root := repoRoot(t)
	dir := t.TempDir()
	write := func(name, src string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	const probe = modulePrefix + "internal/hostcap/zzstaticprobe"
	replace := map[string]string{
		filepath.Join(root, "internal", "hostcap", "zzstaticprobe", "p.go"): write("p.go",
			"package zzstaticprobe\n\n//go:cgo_import_dynamic zz_getpid getpid \"libc.so.6\"\n"),
		filepath.Join(root, "internal", "hostcap", "zz_static_probe.go"): write("edge.go",
			"package hostcap\n\nimport _ \""+probe+"\"\n"),
	}
	overlay, err := json.Marshal(map[string]map[string]string{"Replace": replace})
	if err != nil {
		t.Fatal(err)
	}
	overlayPath := write("overlay.json", string(overlay))
	read := func(f string) ([]byte, error) {
		if r, ok := replace[f]; ok {
			return os.ReadFile(r)
		}
		return os.ReadFile(f)
	}
	for _, goarch := range []string{"amd64", "arm64"} {
		g := linuxGraph(t, root, goarch, overlayPath, "cmd/aii")
		why := whyDynamic(t, g, read)
		if _, refused := why[probe]; !refused || len(why) != 1 {
			t.Fatalf("linux/%s: a dynamic import entering aii through internal/hostcap was not the one refusal: %v", goarch, why)
		}
		if chain := importChain(g, modulePrefix+"cmd/aii", probe); !strings.HasPrefix(chain, "cmd/aii -> ") ||
			!strings.HasSuffix(chain, "internal/hostcap -> internal/hostcap/zzstaticprobe") {
			t.Errorf("linux/%s: the refusal does not name the edge that brought it in: %s", goarch, chain)
		}
	}
}

// The packages carry what the guard assumes: aii and aii-app linked with -d,
// the probe beside them, and the libc it needs declared.
func TestTheLinuxPackagesShipTheProbeBesideAStaticAii(t *testing.T) {
	deb := readPackaging(t, "deb", "build-deb.sh")
	for _, want := range []string{
		`-ldflags "-s -w -d" -o "$stage/usr/bin/aii" ./cmd/aii`,
		`-ldflags "-s -w -d" -o "$stage/usr/bin/aii-app" ./cmd/aii-app`,
		`-o "$stage/usr/bin/aii-vulkan-probe" ./cmd/aii-vulkan-probe`,
		"Depends: libc6, ",
	} {
		if !strings.Contains(deb, want) {
			t.Errorf("packaging/deb/build-deb.sh lost %q", want)
		}
	}
	// These lines link the update archives' aii and aii-app the same way,
	// and leave the probe dynamic. The probe's place at the archive's ROOT,
	// the one place the in-app updater takes it from, is the release
	// contract's (internal/updates.ArchiveFiles), which the archive is
	// written from and read back against.
	arc := readPackaging(t, "archives.sh")
	for _, want := range []string{
		`ld="-s -w -d"; [ "$command" = ./cmd/aii-vulkan-probe ] && ld="-s -w"`,
		`CGO_ENABLED=0 GOOS="$goos" GOARCH="$arch" "$GO" build -trimpath -ldflags "$ld" -o "$stage/$name" "$command"`,
		`"$TOOL" archive-files -platform linux | tar -czf "$out/$asset" -C "$stage" -T -`,
	} {
		if !strings.Contains(arc, want) {
			t.Errorf("packaging/archives.sh lost %q", want)
		}
	}
}
