package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/updates"
)

func goBinary(t *testing.T) string {
	t.Helper()
	if bin := os.Getenv("AII_GO"); bin != "" {
		return bin
	}
	bin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("the go tool is needed to list the shipped programs' modules: %v", err)
	}
	return bin
}

func repoFile(t *testing.T, parts ...string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(append([]string{"..", ".."}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// WHAT SHIPS IS GENERATED FROM THE SHIPPED PROGRAMS' MODULE GRAPH, AND THE
// GRAPH OF TODAY CAN BE GENERATED. A release carried the product's own
// licence and none of the licences of what its programs are built from,
// though each of those asks that its text travel with a binary. The file
// is made at packaging time and never committed, so this is the gate's
// one look at it: every module the go tool reports for a shipped command
// on any supported target has its terms in the output, and the Go
// distribution's own are there. A dependency whose terms cannot be found
// fails here, before a release is being cut.
func TestTheThirdPartyLicencesGenerateForEveryShippedProgram(t *testing.T) {
	repo := filepath.Join("..", "..")
	mods, err := thirdPartyModules(goBinary(t), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) < 10 {
		t.Fatalf("only %d third-party modules were found for the shipped programs: %+v", len(mods), mods)
	}
	out, err := generateThirdPartyLicenses(goBinary(t), repo)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, m := range mods {
		if !strings.Contains(text, licensesRule+m.Path+" "+m.Version+"\n") {
			t.Errorf("%s %s is built into a shipped program and has no section", m.Path, m.Version)
		}
	}
	for _, said := range []string{"THIRD-PARTY SOFTWARE IN AII OS", "The Go programming language: runtime and standard library", "golang.org/x/crypto ", "modernc.org/sqlite ", "github.com/tetratelabs/wazero "} {
		if !strings.Contains(text, said) {
			t.Errorf("the generated file does not name %q", said)
		}
	}
	// EVERY TARGET'S OWN GRAPH IS IN IT: a module only one platform's
	// programs are built from is listed for that platform's sake.
	listed := map[string]bool{}
	for _, m := range mods {
		listed[m.Path] = true
	}
	for _, target := range updates.SupportedTargets() {
		own, err := modulesForTarget(goBinary(t), repo, target.Platform, target.Arch)
		if err != nil || len(own) == 0 {
			t.Fatalf("%s/%s: %d modules, %v", target.Platform, target.Arch, len(own), err)
		}
		for _, m := range own {
			if !listed[m.Path] {
				t.Errorf("%s is built into a program shipped for %s/%s and is not listed", m.Path, target.Platform, target.Arch)
			}
		}
	}
	if !strings.HasSuffix(text, "\n") || strings.HasSuffix(text, "\n\n") || strings.Contains(text, "\r") {
		t.Error("the generated file does not end with one newline, or carries a carriage return")
	}
	// The same graph gives the same bytes: nothing of the hour or the
	// host is in the file.
	again, err := generateThirdPartyLicenses(goBinary(t), repo)
	if err != nil || !bytes.Equal(out, again) {
		t.Fatalf("two generations from one checkout differ (%v)", err)
	}
	// It is not a file of this tree.
	if _, err := os.Stat(filepath.Join(repo, ".gitignore")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(repoFile(t, ".gitignore"), "\n/"+ThirdPartyLicensesFile+"\n") {
		t.Errorf("%s is generated at packaging time and .gitignore does not keep it out of the tree", ThirdPartyLicensesFile)
	}
}

// EVERY SUPPORTED TARGET HAS ITS PROGRAMS NAMED, so a target added to the
// release contract cannot ship programs whose modules nobody listed. And
// they are these: the list is derived from what a release installs
// (updates.ShippedPrograms) and the installer that carries it, and a
// program dropped from either is dropped from the licences too, so the
// sets are written out here once more, to be changed on purpose.
func TestEverySupportedTargetNamesItsShippedCommands(t *testing.T) {
	want := map[string][]string{
		"linux":   {"./cmd/aii", "./cmd/aii-app", "./cmd/aii-vulkan-probe"},
		"macos":   {"./cmd/aii", "./cmd/aii-app"},
		"windows": {"./cmd/aii", "./cmd/aii-app", "./cmd/aii-setup"},
	}
	for _, target := range updates.SupportedTargets() {
		got := shippedCommands(target.Platform)
		if len(got) == 0 {
			t.Errorf("no shipped commands are named for %s", target.Platform)
		}
		if !slices.Equal(got, want[target.Platform]) {
			t.Errorf("%s ships %q, want %q", target.Platform, got, want[target.Platform])
		}
		for _, command := range got {
			if fi, err := os.Stat(filepath.Join("..", "..", filepath.FromSlash(command))); err != nil || !fi.IsDir() {
				t.Errorf("%s ships %s, which is not a command of this tree (%v)", target.Platform, command, err)
			}
		}
	}
}

func TestAModuleWithNoLicenceFileStopsTheGeneration(t *testing.T) {
	with, without := t.TempDir(), t.TempDir()
	for name, body := range map[string]string{
		"LICENSE":      "Permission is hereby granted.\r\nSecond line.\r\n",
		"NOTICE":       "This product includes software by Example.\n",
		"COPYING.txt":  "copying terms\n",
		"readme.md":    "not terms",
		"license_test": "also carried: its name says licence",
	} {
		if err := os.WriteFile(filepath.Join(with, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(with, "LICENSES"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := renderThirdPartyLicenses([]byte("go terms\n"), []thirdPartyModule{{Path: "example.com/with", Version: "v1.2.3", Dir: with}})
	if err != nil {
		t.Fatal(err)
	}
	for _, said := range []string{
		"example.com/with v1.2.3\nLICENSE\n", "example.com/with v1.2.3\nNOTICE\n", "example.com/with v1.2.3\nCOPYING.txt\n",
		"Permission is hereby granted.\nSecond line.\n", "This product includes software by Example.", "go terms",
	} {
		if !bytes.Contains(out, []byte(said)) {
			t.Errorf("the rendering lacks %q:\n%s", said, out)
		}
	}
	if bytes.Contains(out, []byte("not terms")) || bytes.Contains(out, []byte("\r")) {
		t.Errorf("the rendering carries a file that states no terms, or a carriage return:\n%q", out)
	}
	if _, err := renderThirdPartyLicenses([]byte("go terms\n"), []thirdPartyModule{{Path: "example.com/without", Version: "v0.1.0", Dir: without}}); err == nil || !strings.Contains(err.Error(), "example.com/without v0.1.0 has no licence file") {
		t.Fatalf("a module with no licence file did not stop the generation: %v", err)
	}
	if _, err := renderThirdPartyLicenses(nil, nil); err == nil {
		t.Fatal("a missing licence of the Go distribution did not stop the generation")
	}
}

// EVERY ARCHIVE AND INSTALLER CARRIES THE FILE. Each line here is the
// one place a script puts it; a script that loses its line ships a
// program without the licences of what it is built from.
func TestEveryArchiveAndInstallerCarriesTheThirdPartyLicences(t *testing.T) {
	for _, c := range []struct {
		what string
		file []string
		line string
	}{
		{"the Linux update archives' generation", []string{"packaging", "archives.sh"}, "if [ \"$platform\" = linux ]; then\n  . packaging/third-party-licenses.sh\n  third_party_licenses \"$GO\"\nfi"},
		{"the Linux update archives' staging copy", []string{"packaging", "archives.sh"}, `cp THIRD_PARTY_LICENSES "$stage/THIRD_PARTY_LICENSES"`},
		{"the Linux update archives' file list", []string{"packaging", "archives.sh"}, `"$TOOL" archive-files -platform linux | tar -czf "$out/$asset" -C "$stage" -T -`},
		{"the Windows update archive, from what the installer writes", []string{"packaging", "archives.sh"}, `(cd "$staged" && "$TOOL" archive-files -platform windows | zip -q "$out/$asset" -@)`},
		{"what the Windows installer's build stages for it", []string{"packaging", "windows", "build-setup.sh"}, `cp "$payload/aii.exe" "$payload/AII OS.exe" "$payload/LICENSE" "$payload/THIRD_PARTY_LICENSES" "$staged/"`},
		{"the macOS update archive, from the bundle's programs", []string{"packaging", "archives.sh"}, `"$TOOL" archive-files -platform macos | COPYFILE_DISABLE=1 tar -czf "$out/$asset" -C "$staged" -T -`},
		{"what the macOS bundle's build stages for it", []string{"packaging", "macos", "build-dmg.sh"}, `cp LICENSE THIRD_PARTY_LICENSES "$bare/"`},
		{"every archive read back before a candidate is signed", []string{"packaging", "release.sh"}, `out=$("$TOOL" complete -dir "$CAND" -version "$VERSION" $unsigned 2>&1) || die`},
		{"the Ubuntu package's generation", []string{"packaging", "deb", "build-deb.sh"}, ". packaging/third-party-licenses.sh\nthird_party_licenses \"$GO\"\ncp THIRD_PARTY_LICENSES \"$stage/usr/share/doc/aii-os/THIRD_PARTY_LICENSES\""},
		{"the Ubuntu package's copyright file", []string{"packaging", "deb", "copyright"}, `/usr/share/doc/aii-os/THIRD_PARTY_LICENSES`},
		{"the macOS bundle", []string{"packaging", "macos", "build-dmg.sh"}, ". packaging/third-party-licenses.sh\nthird_party_licenses \"$GO\"\ncp THIRD_PARTY_LICENSES \"$app/Contents/Resources/THIRD_PARTY_LICENSES\""},
		{"the hand-off to the macOS builder", []string{"packaging", "build-all.sh"}, "    third_party_licenses \"$GO\"\n    tar -cz --exclude=.git packaging VERSION LICENSE THIRD_PARTY_LICENSES internal/pluginworker/testdata/echo.wasm"},
		{"the Windows installer's payload", []string{"packaging", "windows", "build-setup.sh"}, ". packaging/third-party-licenses.sh\nthird_party_licenses \"$GO\"\ncp THIRD_PARTY_LICENSES \"$payload/THIRD_PARTY_LICENSES\""},
		{"what the Windows installer writes", []string{"cmd", "aii-setup", "main_windows.go"}, `name == "LICENSE" || name == "THIRD_PARTY_LICENSES"`},
		{"the payload copy kept out of the tree", []string{".gitignore"}, "cmd/aii-setup/payload/THIRD_PARTY_LICENSES\n"},
		{"the shared step always generating where the source is", []string{"packaging", "third-party-licenses.sh"}, `"$1" run ./cmd/aii-release licenses -go "$1" -o THIRD_PARTY_LICENSES || {`},
		{"the shared step refusing to go on without the file", []string{"packaging", "third-party-licenses.sh"}, `[ -s THIRD_PARTY_LICENSES ] || {`},
	} {
		if !strings.Contains(repoFile(t, c.file...), c.line) {
			t.Errorf("%s: %s lost %q", c.what, filepath.Join(c.file...), c.line)
		}
	}
	// The file list the archive lines above ask for holds both texts, for
	// every platform a release is made for (and aii-release complete
	// refuses a built archive without them).
	for _, target := range updates.SupportedTargets() {
		for _, doc := range []string{"LICENSE", ThirdPartyLicensesFile} {
			if !slices.Contains(updates.ArchiveFiles(target.Platform), doc) {
				t.Errorf("the %s update archive does not carry %s", target.Platform, doc)
			}
		}
	}
}
