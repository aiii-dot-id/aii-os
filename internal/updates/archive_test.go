package updates

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/vulkancap"
)

// AN UPDATE TOOK aii FROM ANY ENTRY NAMED LIKE IT. The extractor matched the
// binary by the last element of its name, at any depth, and checked only its
// size: a symlink, hard link or directory named aii was taken — a link as an
// EMPTY binary, which the swap then installed — and so was a nested copy, a
// dot-prefixed one, or the first of two. The helper beside it was already
// held to the shape release.sh writes; these hold the binary to the same one,
// in the tar.gz every Unix archive is and in the zip Windows takes. The
// archive is signature-verified before it is opened, so reaching any of this
// takes the release key — which is exactly when the extractor is the last
// thing standing.

// misshapenAii is every shape of an aii entry release.sh does not write — it
// writes one regular, non-empty file named aii at the archive root, once. The
// links declare a size, so their type alone refuses them.
func misshapenAii() map[string][]tarEntry {
	other := []byte("\x7fELF another aii")
	link := func(flag byte, target string) []tarEntry {
		return []tarEntry{{hdr: tar.Header{Name: "aii", Typeflag: flag, Linkname: target, Mode: 0o777, Size: int64(len(other))}}}
	}
	return map[string][]tarEntry{
		"outside the archive": {regularEntry("../aii", other)},
		"absolute":            {regularEntry("/aii", other)},
		"in a subdirectory":   {regularEntry("bin/aii", other)},
		"dot-prefixed":        {regularEntry("./aii", other)},
		"a backslash path":    {regularEntry(`bin\aii`, other)},
		"a symlink":           link(tar.TypeSymlink, "/bin/sh"),
		"a hard link":         link(tar.TypeLink, "LICENSE"),
		"a directory":         {{hdr: tar.Header{Name: "aii/", Typeflag: tar.TypeDir, Mode: 0o755}}},
	}
}

// besideAii places a misshapen entry alone, ahead of the real aii and after
// it. Alone, the old extractor installed it; ahead, it won over the real one;
// after, it was skipped — an archive that names aii twice means two things,
// and the signature proves its bytes, not which one release.sh meant.
func besideAii[E any](bad []E, real, license E) map[string][]E {
	return map[string][]E{
		"alone":        append(slices.Clone(bad), license),
		"ahead of aii": append(slices.Clone(bad), real, license),
		"after aii":    append([]E{real, license}, bad...),
	}
}

// refused asserts an extraction was refused, naming the entry it refused.
func refused(t *testing.T, binary []byte, err error, entry string) {
	t.Helper()
	if err == nil {
		t.Fatalf("a misshapen %q entry was taken: binary %q", entry, binary)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("%q", entry)) {
		t.Errorf("the refusal must name the entry it refused, %q: %v", entry, err)
	}
}

func TestAnUpdateTakesAiiOnlyInTheShapeReleaseShWritesIt(t *testing.T) {
	binary, beside, err := extractFromTarGz("linux", releaseShLayout(t, []byte("new binary"), []byte("the helper")))
	if helper := besideNamed(beside, vulkancap.Helper); err != nil || string(binary) != "new binary" || string(helper) != "the helper" {
		t.Fatalf("the release's own layout: binary %q, helper %q, %v", binary, helper, err)
	}
	real, license := regularEntry("aii", []byte("new binary")), regularEntry("LICENSE", []byte("the license"))
	for shape, bad := range misshapenAii() {
		for where, entries := range besideAii(bad, real, license) {
			t.Run(shape+"/"+where, func(t *testing.T) {
				binary, _, err := extractFromTarGz("linux", tarGzOf(t, entries...))
				refused(t, binary, err, bad[0].hdr.Name)
			})
		}
	}
}

// An empty aii is refused, not installed: a binary that cannot start never
// runs its own rollback check. And a second aii refuses the archive.
func TestAnEmptyOrSecondAiiRefusesTheArchive(t *testing.T) {
	for shape, entries := range map[string][]tarEntry{
		"empty": {regularEntry("aii", nil), regularEntry("LICENSE", []byte("the license"))},
		"twice": {regularEntry("aii", []byte("new binary")), regularEntry("aii", []byte("the other one"))},
	} {
		t.Run(shape, func(t *testing.T) {
			binary, _, err := extractFromTarGz("linux", tarGzOf(t, entries...))
			refused(t, binary, err, "aii")
		})
	}
}

// An entry that declares more than the limit is refused from its header,
// before a byte of it is read — never buffered to find out. The body here is
// cut off after the header: reading it would fail on the cut instead.
func TestAnAiiOverTheLimitIsRefusedFromItsHeader(t *testing.T) {
	var tgz bytes.Buffer
	gz := gzip.NewWriter(&tgz)
	if err := tar.NewWriter(gz).WriteHeader(&tar.Header{Name: "aii", Typeflag: tar.TypeReg, Mode: 0o755, Size: maxDownloadSize + 1}); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, tarErr := extractFromTarGz("linux", tgz.Bytes())

	var z bytes.Buffer
	zw := zip.NewWriter(&z)
	cut := []byte("cut")
	w, err := zw.CreateRaw(&zip.FileHeader{Name: "aii.exe", Method: zip.Store,
		CompressedSize64: uint64(len(cut)), UncompressedSize64: maxDownloadSize + 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(cut); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, zipErr := extractFromZip("windows", z.Bytes())

	for format, err := range map[string]error{"tar.gz": tarErr, "zip": zipErr} {
		if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("exceeds the %d byte limit", maxDownloadSize)) {
			t.Errorf("%s: an entry declaring more than the limit must be refused for its size, from its header: %v", format, err)
		}
	}
}

// And the refusal reaches the install as one: through the real apply, a
// symlink named aii — the shape the old extractor swapped in as an empty
// binary — and a nested aii each leave aii exactly as it was.
func TestAMisshapenAiiRefusesTheUpdate(t *testing.T) {
	skipWithoutTarball(t)
	rig := newHelperRig(t)
	bad := misshapenAii()
	for _, shape := range []string{"a symlink", "in a subdirectory"} {
		t.Run(shape, func(t *testing.T) {
			in := rig.install(t, tarGzOf(t, append(bad[shape], regularEntry("LICENSE", []byte("the license")))...))
			if err := in.apply(); err == nil {
				t.Fatal("an archive whose aii entry is misshapen was installed")
			}
			in.unswapped(t)
		})
	}
}

// The archives an update installs from are the ones packaging/archives.sh
// writes, and it writes no name of its own: the files of each come from
// this package (ArchiveFiles, through aii-release archive-files) and the
// Linux programs it builds are named by it (ShippedPrograms, through
// aii-release programs). It used to spell the names itself, and this test
// held its spelling to the extractor's; now a line that stops asking fails
// here, and the names cannot differ.
func TestTheArchivesAreBuiltFromTheContractsOwnNames(t *testing.T) {
	sh := readRepoFile(t, "packaging", "archives.sh")
	for _, want := range []string{
		`"$TOOL" programs -platform linux | while IFS="$tab" read -r command name; do`,
		`-o "$stage/$name" "$command"`,
		`"$TOOL" archive-files -platform linux | tar -czf "$out/$asset" -C "$stage" -T -`,
		`(cd "$staged" && "$TOOL" archive-files -platform windows | zip -q "$out/$asset" -@)`,
		`"$TOOL" archive-files -platform macos | COPYFILE_DISABLE=1 tar -czf "$out/$asset" -C "$staged" -T -`,
		`"$TOOL" complete -dir "$out" -version "$VERSION" -platform "$platform"`,
	} {
		if !strings.Contains(sh, want) {
			t.Errorf("packaging/archives.sh lost %q, which takes the archive's contents from the release contract", want)
		}
	}
	// And the extractor's two names are the contract's first program.
	for platform, binary := range map[string]string{"linux": tarBinary, "macos": tarBinary, "windows": zipBinary} {
		if got := ArchiveFiles(platform)[0]; got != binary {
			t.Errorf("the %s archive's first file is %q; the extractor takes %q", platform, got, binary)
		}
	}
	// The file an archive holds for every program and licence text is at
	// its root, where the extractor takes them from and nowhere else.
	for _, tgt := range SupportedTargets() {
		files := ArchiveFiles(tgt.Platform)
		for _, name := range append(Installs(tgt.Platform), ShippedDocuments()...) {
			if !slices.Contains(files, name) {
				t.Errorf("the %s archive does not hold %s at its root", tgt.Platform, name)
			}
		}
	}
}

// THE WINDOWS ARCHIVE, the same rule: regular files at the root. A zip has
// no hard links; its symlink carries the target as its body, so the old
// extractor would have installed a path as aii.exe.

type zipEntry struct {
	name string
	mode fs.FileMode
	body []byte
}

func zipOf(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		fh := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		fh.SetMode(e.mode)
		w, err := zw.CreateHeader(fh)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestAWindowsUpdateTakesAiiExeOnlyInTheShapeReleaseShWritesIt(t *testing.T) {
	real := zipEntry{"aii.exe", 0o755, []byte("new binary")}
	license := zipEntry{"LICENSE", 0o644, []byte("the license")}
	binary, _, err := extractFromZip("windows", zipOf(t, real, license))
	if err != nil || string(binary) != "new binary" {
		t.Fatalf("release.sh's own layout: binary %q, %v", binary, err)
	}
	other := []byte("MZ another aii.exe")
	for shape, bad := range map[string]zipEntry{
		"outside the archive": {"../aii.exe", 0o755, other},
		"absolute":            {"/aii.exe", 0o755, other},
		"in a subdirectory":   {"bin/aii.exe", 0o755, other},
		"dot-prefixed":        {"./aii.exe", 0o755, other},
		"a backslash path":    {`bin\aii.exe`, 0o755, other},
		"a symlink":           {"aii.exe", fs.ModeSymlink | 0o777, []byte(`C:\Windows\System32\cmd.exe`)},
		"a directory":         {"aii.exe/", fs.ModeDir | 0o755, nil},
	} {
		for where, entries := range besideAii([]zipEntry{bad}, real, license) {
			t.Run(shape+"/"+where, func(t *testing.T) {
				binary, _, err := extractFromZip("windows", zipOf(t, entries...))
				refused(t, binary, err, bad.name)
			})
		}
	}
	for shape, entries := range map[string][]zipEntry{
		"empty": {{"aii.exe", 0o755, nil}, license},
		"twice": {real, {"aii.exe", 0o755, []byte("the other one")}},
	} {
		t.Run(shape, func(t *testing.T) {
			binary, _, err := extractFromZip("windows", zipOf(t, entries...))
			refused(t, binary, err, "aii.exe")
		})
	}
}
