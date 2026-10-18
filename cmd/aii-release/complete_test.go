package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/updates"
)

// A RELEASE SHIPPED ARCHIVES THAT DID NOT HOLD WHAT ITS PLATFORMS SHIP.
// 0.1.16's Windows archive had no launcher and an aii.exe without the
// installer's resources; no tarball had aii-app, a menu entry or an icon;
// and the macOS tarball's aii was a cross-build nobody had signed, beside a
// bundle whose aii was. Nothing read an archive back. These hold the check
// that does (aii-release complete) to each of those, on archives built here
// in the release contract's own layout.

const testVersion = "9.9.9"

// elfImage is the smallest thing debug/elf reads as a 64-bit executable
// for a machine.
func elfImage(arch string) []byte {
	machine := map[string]uint16{"amd64": 62, "arm64": 183}[arch]
	h := make([]byte, 64)
	copy(h, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	binary.LittleEndian.PutUint16(h[16:], 2) // ET_EXEC
	binary.LittleEndian.PutUint16(h[18:], machine)
	binary.LittleEndian.PutUint32(h[20:], 1)
	binary.LittleEndian.PutUint16(h[52:], 64) // e_ehsize
	return h
}

// machoImage is the smallest thing debug/macho reads as an arm64
// executable with an embedded code signature: ad hoc, as the Go linker
// leaves every arm64 image, or carrying a CMS signature, as a Developer
// ID's does. body tells two images apart.
func machoImage(publisher bool, body string) []byte {
	blob := func(magic uint32, payload []byte) []byte {
		b := make([]byte, 8, 8+len(payload))
		binary.BigEndian.PutUint32(b, magic)
		binary.BigEndian.PutUint32(b[4:], uint32(8+len(payload)))
		return append(b, payload...)
	}
	type slot struct {
		kind uint32
		data []byte
	}
	slots := []slot{{0, blob(0xfade0c02, []byte(body))}}
	if publisher {
		slots = append(slots, slot{0x10000, blob(0xfade0b01, []byte("a CMS signature"))})
	} else {
		slots = append(slots, slot{0x10000, blob(0xfade0b01, nil)}) // what `codesign -s -` writes
	}
	index := make([]byte, 0, 8*len(slots))
	var blobs []byte
	at := uint32(12 + 8*len(slots))
	for _, s := range slots {
		index = binary.BigEndian.AppendUint32(index, s.kind)
		index = binary.BigEndian.AppendUint32(index, at+uint32(len(blobs)))
		blobs = append(blobs, s.data...)
	}
	super := make([]byte, 12)
	binary.BigEndian.PutUint32(super, 0xfade0cc0)
	binary.BigEndian.PutUint32(super[4:], uint32(12+len(index)+len(blobs)))
	binary.BigEndian.PutUint32(super[8:], uint32(len(slots)))
	super = append(append(super, index...), blobs...)

	h := make([]byte, 48)
	for i, v := range []uint32{0xfeedfacf, 0x0100000c, 0, 2, 1, 16, 0, 0, 0x1d, 16, 48, uint32(len(super))} {
		binary.LittleEndian.PutUint32(h[4*i:], v)
	}
	return append(h, super...)
}

// peImages builds a Windows program twice, once as the installer's build
// makes it (with the resource object packaging/icon writes) and once bare,
// as the 0.1.16 archive's aii.exe was. Built once for the test process.
var peImages = sync.OnceValues(func() (withResources, bare []byte) {
	build := func(resources bool) []byte {
		dir, err := os.MkdirTemp("", "aii-release-pe")
		if err != nil {
			panic(err)
		}
		defer os.RemoveAll(dir)
		must := func(err error, out []byte) {
			if err != nil {
				panic(err.Error() + ": " + string(out))
			}
		}
		must(os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.invalid/pe\n\ngo 1.27\n"), 0o644), nil)
		must(os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644), nil)
		goBin := os.Getenv("AII_GO")
		if goBin == "" {
			goBin = "go"
		}
		if resources {
			gen := exec.Command(goBin, "run", "./packaging/icon", "syso", "-arch", "amd64", "-version", testVersion,
				"-file", "aii.exe", "-description", "a test program", "-o", filepath.Join(dir, "rsrc_windows_amd64.syso"))
			gen.Dir = filepath.Join("..", "..")
			out, err := gen.CombinedOutput()
			must(err, out)
		}
		exe := filepath.Join(dir, "p.exe")
		cmd := exec.Command(goBin, "build", "-o", exe, ".")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0", "GOWORK=off", "GOFLAGS=")
		out, err := cmd.CombinedOutput()
		must(err, out)
		image, err := os.ReadFile(exe)
		must(err, nil)
		return image
	}
	return build(true), build(false)
})

// authenticode appends a certificate table holding one PKCS #7 entry to a
// PE image and points the image's security directory at it: where signtool
// puts an Authenticode signature. The entry's bytes are not a signature;
// the check reads that one is there, and the platform verifies it.
func authenticode(t *testing.T, image []byte) []byte {
	t.Helper()
	image = slices.Clone(image)
	opt := int(binary.LittleEndian.Uint32(image[0x3c:])) + 4 + 20
	if binary.LittleEndian.Uint16(image[opt:]) != 0x20b {
		t.Fatal("the test program is not a PE32+ image")
	}
	entry := make([]byte, 8, 16)
	binary.LittleEndian.PutUint32(entry, 16)
	binary.LittleEndian.PutUint16(entry[4:], 0x0200) // WIN_CERT_REVISION_2_0
	binary.LittleEndian.PutUint16(entry[6:], 0x0002) // WIN_CERT_TYPE_PKCS_SIGNED_DATA
	entry = append(entry, "PKCS #7."...)
	security := opt + 112 + 4*8
	binary.LittleEndian.PutUint32(image[security:], uint32(len(image)))
	binary.LittleEndian.PutUint32(image[security+4:], uint32(len(entry)))
	return append(image, entry...)
}

// member is one archive entry; a nil body is written as a symlink.
type member struct {
	name string
	body []byte
}

func writeTarGz(t *testing.T, path string, members []member) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, m := range members {
		hdr := &tar.Header{Name: m.name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(m.body))}
		if m.body == nil {
			hdr = &tar.Header{Name: m.name, Typeflag: tar.TypeSymlink, Linkname: "/bin/sh", Mode: 0o777}
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(m.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeZip(t *testing.T, path string, members []member) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, m := range members {
		w, err := zw.Create(m.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(m.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// haveDpkgDeb is whether this machine can build and read a system package.
// Where it cannot (the macOS and Windows native runs), a candidate here
// has no packages and the archives are checked alone; the package tests
// say that they did not run.
var haveDpkgDeb = sync.OnceValue(func() bool { _, err := exec.LookPath("dpkg-deb"); return err == nil })

// writeDeb builds a package for arch holding files (by path under the
// package root), as packaging/deb/build-deb.sh builds the real one.
func writeDeb(t *testing.T, path, arch string, files []member) {
	t.Helper()
	stage := t.TempDir()
	if err := os.Chmod(stage, 0o755); err != nil {
		t.Fatal(err)
	}
	control := "Package: aii-os\nVersion: " + testVersion + "\nArchitecture: " + arch + "\nMaintainer: test <noreply@example.invalid>\nDescription: a test package\n"
	for _, m := range append([]member{{"DEBIAN/control", []byte(control)}}, files...) {
		full := filepath.Join(stage, filepath.FromSlash(m.name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, m.body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command("dpkg-deb", "--build", "--root-owner-group", stage, path).CombinedOutput(); err != nil {
		t.Fatalf("dpkg-deb --build: %v: %s", err, out)
	}
}

// candidate writes every archive a release carries into a directory, each
// in the release contract's layout with programs of its platform's kind,
// signed as a release's are, and returns the members it wrote so a test
// can change one and write that archive again.
type candidate struct {
	t        *testing.T
	dir      string
	members  map[updates.Target][]member
	packages map[string][]member // by architecture
}

func newCandidate(t *testing.T) *candidate {
	t.Helper()
	withResources, _ := peImages()
	c := &candidate{t: t, dir: t.TempDir(), members: map[updates.Target][]member{}, packages: map[string][]member{}}
	for _, tgt := range updates.SupportedTargets() {
		programs := map[string]bool{}
		for _, p := range updates.ShippedPrograms(tgt.Platform) {
			programs[p.Name] = true
		}
		for _, f := range updates.ArchiveFiles(tgt.Platform) {
			body := []byte("the contents of " + f)
			if programs[f] {
				switch tgt.Platform {
				case "linux":
					body = elfImage(tgt.Arch)
				case "macos":
					body = machoImage(true, f)
				case "windows":
					body = authenticode(t, withResources)
				}
			}
			c.members[tgt] = append(c.members[tgt], member{f, body})
		}
		c.write(tgt)
	}
	// The system packages: what the Linux archive holds, where a package
	// puts it.
	if haveDpkgDeb() {
		for _, tgt := range updates.PackageTargets() {
			for _, m := range c.members[updates.Target{Platform: tgt.Platform, Arch: tgt.Arch}] {
				switch {
				case strings.HasPrefix(m.name, "share/"):
					c.packages[tgt.Arch] = append(c.packages[tgt.Arch], member{"usr/" + m.name, m.body})
				case m.name != "LICENSE" && m.name != ThirdPartyLicensesFile:
					c.packages[tgt.Arch] = append(c.packages[tgt.Arch], member{"usr/bin/" + m.name, m.body})
				}
			}
			c.writePackage(tgt.Arch, tgt.Arch)
		}
	}
	// The macOS app archive beside the tarball, holding the same aii.
	for _, tgt := range updates.BundleTargets() {
		writeZip(t, filepath.Join(c.dir, updates.BundleAssetName(testVersion, tgt.Platform, tgt.Arch)),
			[]member{{"AII OS.app/Contents/MacOS/aii", c.file(tgt, "aii")}, {"AII OS.app/Contents/MacOS/AII OS", machoImage(true, "sealed")}})
	}
	return c
}

func (c *candidate) write(tgt updates.Target) {
	path := filepath.Join(c.dir, updates.AssetName(testVersion, tgt.Platform, tgt.Arch))
	if tgt.Platform == "windows" {
		writeZip(c.t, path, c.members[tgt])
		return
	}
	writeTarGz(c.t, path, c.members[tgt])
}

// writePackage writes one architecture's package, declaring declared.
func (c *candidate) writePackage(arch, declared string) {
	path := filepath.Join(c.dir, updates.PackageAssetName(testVersion, arch))
	_ = os.Remove(path)
	writeDeb(c.t, path, declared, c.packages[arch])
}

func (c *candidate) file(tgt updates.Target, name string) []byte {
	for _, m := range c.members[tgt] {
		if m.name == name {
			return m.body
		}
	}
	c.t.Fatalf("the %s archive holds no %s", tgt.Platform, name)
	return nil
}

// set replaces one member's bytes, drop removes it, add appends one; each
// writes the archive again.
func (c *candidate) set(tgt updates.Target, name string, body []byte) {
	for i, m := range c.members[tgt] {
		if m.name == name {
			c.members[tgt][i].body = body
		}
	}
	c.write(tgt)
}

func (c *candidate) drop(tgt updates.Target, name string) {
	c.members[tgt] = slices.DeleteFunc(c.members[tgt], func(m member) bool { return m.name == name })
	c.write(tgt)
}

func (c *candidate) add(tgt updates.Target, m member) {
	c.members[tgt] = append(c.members[tgt], m)
	c.write(tgt)
}

// complete runs the verb and returns its exit status and what it said.
func complete(t *testing.T, dir string, more ...string) (int, string) {
	t.Helper()
	open := func(name string) *os.File {
		f, err := os.Create(filepath.Join(t.TempDir(), name))
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	stdout, stderr := open("stdout"), open("stderr")
	if !haveDpkgDeb() && !slices.Contains(more, "-platform") && !slices.Contains(more, "-packages") {
		// No package can be built or read here: the archives alone.
		for _, tgt := range updates.SupportedTargets() {
			more = append(more, "-platform", tgt.Platform)
		}
	}
	rc := run(append([]string{"complete", "-dir", dir, "-version", testVersion}, more...), stdout, stderr)
	var said strings.Builder
	for _, f := range []*os.File{stdout, stderr} {
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		said.Write(b)
	}
	return rc, said.String()
}

var (
	linuxAMD64 = updates.Target{Platform: "linux", Arch: "amd64"}
	linuxARM64 = updates.Target{Platform: "linux", Arch: "arm64"}
	macOS      = updates.Target{Platform: "macos", Arch: "arm64"}
	windows    = updates.Target{Platform: "windows", Arch: "amd64"}
)

func TestACandidateWhoseArchivesHoldEverythingIsComplete(t *testing.T) {
	c := newCandidate(t)
	rc, said := complete(t, c.dir)
	if rc != 0 {
		t.Fatalf("a candidate in the contract's own layout was refused (%d):\n%s", rc, said)
	}
	for _, tgt := range updates.SupportedTargets() {
		if want := "complete: " + updates.AssetName(testVersion, tgt.Platform, tgt.Arch); !strings.Contains(said, want) {
			t.Errorf("the verdict does not name %s:\n%s", want, said)
		}
	}
	if !haveDpkgDeb() {
		return
	}
	for _, tgt := range updates.PackageTargets() {
		if want := "complete: " + updates.PackageAssetName(testVersion, tgt.Arch); !strings.Contains(said, want) {
			t.Errorf("the verdict does not name %s:\n%s", want, said)
		}
	}
	if strings.Contains(said, "not asked for") {
		t.Errorf("a release's check said it left something out:\n%s", said)
	}
}

// THE SYSTEM PACKAGES ARE HELD TO THE SAME LIST. arm64 had an archive and no
// package, so an install there had no menu entry and no removal; and a
// package is the one thing here installed as root. Each target the
// contract gives a package has it, for its architecture, with every
// program a Linux release installs and what the archive lays out under
// share/.
func TestAPackageThatLacksWhatALinuxReleaseShipsIsRefused(t *testing.T) {
	if !haveDpkgDeb() {
		t.Skip("dpkg-deb is not on this machine: a system package can be neither built nor read here, and the check says so (TestAPackageCannotPassWhereItCannotBeRead)")
	}
	drop := func(c *candidate, arch, name string) {
		c.packages[arch] = slices.DeleteFunc(c.packages[arch], func(m member) bool { return m.name == name })
		c.writePackage(arch, arch)
	}
	for name, tc := range map[string]struct {
		break_ func(c *candidate)
		says   string
	}{
		"no arm64 package at all (0.1.16)": {
			func(c *candidate) {
				if err := os.Remove(filepath.Join(c.dir, updates.PackageAssetName(testVersion, "arm64"))); err != nil {
					c.t.Fatal(err)
				}
			}, "aii-os_9.9.9_arm64.deb: is not in ",
		},
		"a package without the launcher":   {func(c *candidate) { drop(c, "arm64", "usr/bin/aii-app") }, "aii-os_9.9.9_arm64.deb: usr/bin/aii-app is missing"},
		"a package without the menu entry": {func(c *candidate) { drop(c, "amd64", "usr/share/applications/aii-os.desktop") }, "aii-os_9.9.9_amd64.deb: usr/share/applications/aii-os.desktop is missing"},
		"a package declaring another architecture": {
			func(c *candidate) { c.writePackage("arm64", "amd64") }, `aii-os_9.9.9_arm64.deb: declares Architecture "amd64", not arm64`,
		},
		"the arm64 package holding amd64 programs": {
			func(c *candidate) {
				for i, m := range c.packages["arm64"] {
					if m.name == "usr/bin/aii" {
						c.packages["arm64"][i].body = elfImage("amd64")
					}
				}
				c.writePackage("arm64", "arm64")
			}, "aii-os_9.9.9_arm64.deb: usr/bin/aii (./cmd/aii) is built for EM_X86_64, not arm64",
		},
		"a file that is not a package": {
			func(c *candidate) {
				if err := os.WriteFile(filepath.Join(c.dir, updates.PackageAssetName(testVersion, "amd64")), []byte("not a package"), 0o644); err != nil {
					c.t.Fatal(err)
				}
			}, "aii-os_9.9.9_amd64.deb: dpkg-deb ",
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := newCandidate(t)
			tc.break_(c)
			for _, flags := range [][]string{nil, {"-packages"}} {
				rc, said := complete(t, c.dir, flags...)
				if rc != 1 || !strings.Contains(said, tc.says) {
					t.Fatalf("%v: the candidate was not refused with %q (%d):\n%s", flags, tc.says, rc, said)
				}
			}
			// A check of the archives alone is not asked about packages,
			// and says so.
			rc, said := complete(t, c.dir, "-platform", "linux", "-platform", "macos", "-platform", "windows")
			if rc != 0 || !strings.Contains(said, "not checked: "+updates.PackageAssetName(testVersion, "arm64")) {
				t.Fatalf("a check of the archives alone (%d):\n%s", rc, said)
			}
		})
	}
	// And the packages alone are complete, with the archives said to be
	// left out.
	c := newCandidate(t)
	rc, said := complete(t, c.dir, "-packages")
	if rc != 0 || !strings.Contains(said, "not checked: "+updates.AssetName(testVersion, "linux", "arm64")) {
		t.Fatalf("-packages on a complete candidate (%d):\n%s", rc, said)
	}
	if rc, _ := complete(t, c.dir, "-packages", "-platform", "linux"); rc != 2 {
		t.Fatalf("-packages with -platform exited %d: they ask for different things", rc)
	}
}

// Each thing the 0.1.16 archives lacked, and each other way an archive can
// fall short of what its platform ships, refuses the candidate and names
// the archive and the file.
func TestAnArchiveThatLacksWhatItsPlatformShipsIsRefused(t *testing.T) {
	_, barePE := peImages()
	for name, tc := range map[string]struct {
		break_ func(c *candidate)
		says   []string
	}{
		"the Windows archive without the launcher (0.1.16)": {
			func(c *candidate) { c.drop(windows, "AII OS.exe") },
			[]string{"_windows_amd64.zip: AII OS.exe is missing", `the platform's installer puts ["aii.exe" "AII OS.exe" "LICENSE" "THIRD_PARTY_LICENSES"] on disk`},
		},
		"the Windows aii.exe without the installer's resources (0.1.16)": {
			func(c *candidate) { c.set(windows, "aii.exe", authenticode(c.t, barePE)) },
			[]string{"aii.exe (./cmd/aii) carries no icon resource", "aii.exe (./cmd/aii) carries no name, publisher and version resource"},
		},
		"a Windows program that is not signed": {
			func(c *candidate) { withResources, _ := peImages(); c.set(windows, "AII OS.exe", withResources) },
			[]string{"AII OS.exe (./cmd/aii-app) carries no Authenticode signature"},
		},
		"a tarball without aii-app (0.1.16)": {
			func(c *candidate) { c.drop(linuxARM64, "aii-app") },
			[]string{"_linux_arm64.tar.gz: aii-app is missing", `an update would install ["aii" "aii-vulkan-probe"]`},
		},
		"the arm64 tarball without the menu entry (0.1.16)": {
			func(c *candidate) { c.drop(linuxARM64, "share/applications/aii-os.desktop") },
			[]string{"_linux_arm64.tar.gz: share/applications/aii-os.desktop is missing"},
		},
		"a tarball without one size of the icon": {
			func(c *candidate) { c.drop(linuxAMD64, "share/icons/hicolor/48x48/apps/aii-os.png") },
			[]string{"_linux_amd64.tar.gz: share/icons/hicolor/48x48/apps/aii-os.png is missing"},
		},
		"a tarball without the licences of what it is built from": {
			func(c *candidate) { c.drop(linuxAMD64, "THIRD_PARTY_LICENSES") },
			[]string{"_linux_amd64.tar.gz: THIRD_PARTY_LICENSES is missing"},
		},
		"the arm64 tarball holding amd64 programs": {
			func(c *candidate) { c.set(linuxARM64, "aii", elfImage("amd64")) },
			[]string{"_linux_arm64.tar.gz: aii (./cmd/aii) is built for EM_X86_64, not arm64"},
		},
		"the macOS tarball's aii not the bundle's (0.1.16)": {
			func(c *candidate) { c.set(macOS, "aii", machoImage(true, "another build")) },
			[]string{"_macos_arm64.tar.gz: aii is not the file aii-os-app_9.9.9_macos_arm64.zip carries at Contents/MacOS/aii"},
		},
		"a macOS program signed by nobody (0.1.16)": {
			func(c *candidate) { c.set(macOS, "aii-app", machoImage(false, "aii-app")) },
			[]string{"aii-app (./cmd/aii-app) carries no Developer ID signature"},
		},
		"a program that is a link": {
			func(c *candidate) { c.set(linuxAMD64, "aii-app", nil) },
			[]string{"_linux_amd64.tar.gz: aii-app is empty or not a regular file", "the updater refuses it"},
		},
		"a program that is there twice": {
			func(c *candidate) { c.add(linuxAMD64, member{"aii-app", elfImage("amd64")}) },
			[]string{"_linux_amd64.tar.gz: aii-app appears 2 times", "the updater refuses it"},
		},
		"a program that is not at the root": {
			func(c *candidate) {
				c.drop(windows, "AII OS.exe")
				c.add(windows, member{"bin/AII OS.exe", c.file(windows, "aii.exe")})
			},
			[]string{"_windows_amd64.zip: AII OS.exe is missing", "the updater refuses it"},
		},
		"an archive that is not there": {
			func(c *candidate) {
				if err := os.Remove(filepath.Join(c.dir, updates.AssetName(testVersion, "linux", "arm64"))); err != nil {
					c.t.Fatal(err)
				}
			},
			[]string{"_linux_arm64.tar.gz: is not in "},
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := newCandidate(t)
			tc.break_(c)
			rc, said := complete(t, c.dir)
			if rc != 1 {
				t.Fatalf("the candidate was not refused (%d):\n%s", rc, said)
			}
			for _, want := range tc.says {
				if !strings.Contains(said, want) {
					t.Errorf("the refusal does not say %q:\n%s", want, said)
				}
			}
		})
	}
}

// A build that is not a release has no signer, and says so: its programs'
// signatures are not asked for, for the platforms it names and no other.
// What an archive holds is asked either way.
func TestABuildThatIsNotAReleaseIsCheckedForEverythingButSignatures(t *testing.T) {
	c := newCandidate(t)
	withResources, _ := peImages()
	c.set(windows, "aii.exe", withResources)
	c.set(windows, "AII OS.exe", withResources)
	if rc, said := complete(t, c.dir); rc != 1 || !strings.Contains(said, "carries no Authenticode signature") {
		t.Fatalf("unsigned Windows programs passed a release's check (%d):\n%s", rc, said)
	}
	rc, said := complete(t, c.dir, "-unsigned", "windows")
	if rc != 0 || !strings.Contains(said, "signatures not asked for (-unsigned): not a release's check") {
		t.Fatalf("an unsigned Windows build, said to be one (%d):\n%s", rc, said)
	}
	// It excuses Windows only.
	c.set(macOS, "aii-app", machoImage(false, "aii-app"))
	if rc, said := complete(t, c.dir, "-unsigned", "windows"); rc != 1 || !strings.Contains(said, "carries no Developer ID signature") {
		t.Fatalf("-unsigned windows excused a macOS program (%d):\n%s", rc, said)
	}
	// And never a file.
	c.drop(windows, "AII OS.exe")
	if rc, said := complete(t, c.dir, "-unsigned", "windows", "-unsigned", "macos"); rc != 1 || !strings.Contains(said, "AII OS.exe is missing") {
		t.Fatalf("-unsigned excused a missing program (%d):\n%s", rc, said)
	}
}

// A gate's packaging stage builds no macOS archive, so it asks for the
// platforms it built, and the verdict names what it did not look at.
func TestACheckOfSomePlatformsSaysWhichItLeftOut(t *testing.T) {
	c := newCandidate(t)
	if err := os.Remove(filepath.Join(c.dir, updates.AssetName(testVersion, "macos", "arm64"))); err != nil {
		t.Fatal(err)
	}
	rc, said := complete(t, c.dir, "-platform", "linux", "-platform", "windows")
	if rc != 0 {
		t.Fatalf("the platforms asked for are complete (%d):\n%s", rc, said)
	}
	if want := "not checked: " + updates.AssetName(testVersion, "macos", "arm64"); !strings.Contains(said, want) {
		t.Errorf("the verdict does not say %q:\n%s", want, said)
	}
	if rc, _ := complete(t, c.dir); rc != 1 {
		t.Fatal("a release's check passed with the macOS archive missing")
	}
	for _, bad := range [][]string{{"-platform", "plan9"}, {"-unsigned", "plan9"}} {
		if rc, said := complete(t, c.dir, bad...); rc != 2 || !strings.Contains(said, `no release is made for platform "plan9"`) {
			t.Errorf("%v: %d\n%s", bad, rc, said)
		}
	}
}

// What the archive builder asks is the release contract, line for line.
func TestTheBuildersQuestionsAreAnsweredFromTheContract(t *testing.T) {
	ask := func(args ...string) (int, string) {
		out, err := os.Create(filepath.Join(t.TempDir(), "out"))
		if err != nil {
			t.Fatal(err)
		}
		rc := run(args, out, os.Stderr)
		if err := out.Close(); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(out.Name())
		if err != nil {
			t.Fatal(err)
		}
		return rc, string(b)
	}
	for _, tgt := range updates.SupportedTargets() {
		rc, got := ask("archive-files", "-platform", tgt.Platform)
		if want := strings.Join(updates.ArchiveFiles(tgt.Platform), "\n") + "\n"; rc != 0 || got != want {
			t.Errorf("archive-files -platform %s (%d):\n%s\nwant:\n%s", tgt.Platform, rc, got, want)
		}
		var want strings.Builder
		for _, p := range updates.ShippedPrograms(tgt.Platform) {
			want.WriteString(p.Command + "\t" + p.Name + "\n")
		}
		if rc, got := ask("programs", "-platform", tgt.Platform); rc != 0 || got != want.String() {
			t.Errorf("programs -platform %s (%d):\n%s\nwant:\n%s", tgt.Platform, rc, got, want.String())
		}
	}
	for _, verb := range []string{"archive-files", "programs"} {
		if rc, _ := ask(verb, "-platform", "plan9"); rc != 2 {
			t.Errorf("%s for a platform no release is made for exited %d", verb, rc)
		}
	}
}

// THE LIST IS HELD TO THE TREE IT DESCRIBES. What the Linux archive lays
// out under share/ is what the Ubuntu package installs under /usr/share,
// from the same files; what the Windows archive holds is what the
// installer's build stages and the installer writes; and the macOS
// programs are the two the bundle's build signs for a bare install.
func TestWhatAnArchiveHoldsIsWhatItsPlatformsInstallerInstalls(t *testing.T) {
	// Linux: every icon the package installs, the menu entry, the manual.
	icons, err := filepath.Glob(filepath.Join("..", "..", "packaging", "deb", "icons", "hicolor", "*", "apps", "aii-os.png"))
	if err != nil || len(icons) == 0 {
		t.Fatalf("no icons under packaging/deb/icons (%v)", err)
	}
	want := []string{"share/applications/aii-os.desktop", "share/man/man1/aii.1.gz"}
	for _, icon := range icons {
		size := filepath.Base(filepath.Dir(filepath.Dir(icon)))
		want = append(want, "share/icons/hicolor/"+size+"/apps/aii-os.png")
	}
	var got []string
	for _, f := range updates.ArchiveFiles("linux") {
		if strings.HasPrefix(f, "share/") {
			got = append(got, f)
		}
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("the Linux archive lays out %q under share/; the package's tree has %q", got, want)
	}
	deb := repoFile(t, "packaging", "deb", "build-deb.sh")
	for _, line := range []string{
		`install -m 0644 packaging/deb/aii-os.desktop "$stage/usr/share/applications/aii-os.desktop"`,
		`install -m 0644 "$png" "$stage/usr/share/icons/hicolor/$size/apps/aii-os.png"`,
		`gzip -9nc packaging/deb/aii.1 > "$stage/usr/share/man/man1/aii.1.gz"`,
		`-o "$stage/usr/bin/aii" ./cmd/aii`, `-o "$stage/usr/bin/aii-app" ./cmd/aii-app`, `-o "$stage/usr/bin/aii-vulkan-probe" ./cmd/aii-vulkan-probe`,
	} {
		if !strings.Contains(deb, line) {
			t.Errorf("packaging/deb/build-deb.sh lost %q; the Linux archive's list was written against it", line)
		}
	}
	arc := repoFile(t, "packaging", "archives.sh")
	for _, line := range []string{
		`install -m 0644 packaging/deb/aii-os.desktop "$stage/share/applications/aii-os.desktop"`,
		`install -m 0644 "$png" "$stage/share/icons/hicolor/$size/apps/aii-os.png"`,
		`gzip -9nc packaging/deb/aii.1 > "$stage/share/man/man1/aii.1.gz"`,
	} {
		if !strings.Contains(arc, line) {
			t.Errorf("packaging/archives.sh lost %q", line)
		}
	}

	// Windows: the installer's build makes these programs under these
	// names, stages them with the licence texts, and the installer writes
	// every executable of its payload and those two texts.
	setup := repoFile(t, "packaging", "windows", "build-setup.sh")
	for _, p := range updates.ShippedPrograms("windows") {
		if line := `-o "$payload/` + p.Name + `" ` + p.Command; !strings.Contains(setup, line) {
			t.Errorf("packaging/windows/build-setup.sh does not build %s as %s (%q)", p.Command, p.Name, line)
		}
	}
	installer := repoFile(t, "cmd", "aii-setup", "main_windows.go")
	for _, line := range []string{
		`isExe := strings.HasSuffix(strings.ToLower(e.Name()), ".exe")`,
		`return name == "LICENSE" || name == "THIRD_PARTY_LICENSES"`,
	} {
		if !strings.Contains(installer, line) {
			t.Errorf("cmd/aii-setup/main_windows.go lost %q; what an update installs was written against it", line)
		}
	}
	if got, want := updates.Installs("windows"), []string{"aii.exe", "AII OS.exe", "LICENSE", "THIRD_PARTY_LICENSES"}; !slices.Equal(got, want) {
		t.Errorf("a Windows update installs %q; the installer writes %q", got, want)
	}

	// macOS: the bundle's aii, and the launcher signed on its own.
	dmg := repoFile(t, "packaging", "macos", "build-dmg.sh")
	for _, line := range []string{
		`bare="$OUT/macos-arm64.signed"`,
		`cp "$app/Contents/MacOS/AII OS" "$bare/aii-app"`,
		`cp "$app/Contents/MacOS/aii" "$bare/aii"`,
		`codesign --force --timestamp --options runtime -s "$IDENTITY" "$bare/aii-app"`,
		`zip -q -j "$barezip" "$bare/aii" "$bare/aii-app"`,
		`xcrun notarytool submit "$barezip" --keychain-profile "$PROFILE" --wait`,
	} {
		if !strings.Contains(dmg, line) {
			t.Errorf("packaging/macos/build-dmg.sh lost %q", line)
		}
	}
	all := repoFile(t, "packaging", "build-all.sh")
	for _, line := range []string{
		`archives linux`, `archives windows`, `archives macos`,
		`"$MAC_BUILDER:$rdir/out/macos-arm64.signed/*" "$OUT/macos-arm64.signed/"`,
	} {
		if !strings.Contains(all, line) {
			t.Errorf("packaging/build-all.sh lost %q", line)
		}
	}
}

// A machine without dpkg-deb cannot read a package, and the check there
// says so and fails: it never passes a package it did not open.
func TestAPackageCannotPassWhereItCannotBeRead(t *testing.T) {
	dir := t.TempDir()
	for _, tgt := range updates.PackageTargets() {
		if err := os.WriteFile(filepath.Join(dir, updates.PackageAssetName(testVersion, tgt.Arch)), []byte("a package nobody can open here"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", t.TempDir())
	rc, said := complete(t, dir, "-packages")
	if rc != 1 || !strings.Contains(said, "cannot be read here: dpkg-deb is not on this machine") {
		t.Fatalf("a package that could not be opened (%d):\n%s", rc, said)
	}
}
