package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/updates"
)

var installerCommands = map[string][]string{"windows": {"./cmd/aii-setup"}}

func runComplete(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("complete", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "the directory holding the built archives (required)")
	version := fs.String("version", "", "release version (required)")
	var only, unsigned []string
	fs.Func("platform", "check this platform's archives only (repeatable; default: every supported target)", func(p string) error {
		only = append(only, p)
		return nil
	})
	fs.Func("unsigned", "a build of this platform that is not a release: do not ask that its programs are signed (repeatable)", func(p string) error {
		unsigned = append(unsigned, p)
		return nil
	})
	packages := fs.Bool("packages", false, "check the system packages only")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *dir == "" || *version == "" || (*packages && len(only) > 0) {
		fs.Usage()
		return 2
	}
	for _, p := range slices.Concat(only, unsigned) {
		if len(updates.ShippedPrograms(p)) == 0 {
			fmt.Fprintf(stderr, "aii-release: no release is made for platform %q\n", p)
			return 2
		}
	}
	failed := false
	for _, t := range updates.PackageTargets() {
		name := updates.PackageAssetName(*version, t.Arch)
		if len(only) > 0 {
			fmt.Fprintf(stdout, "not checked: %s (not asked for)\n", name)
			continue
		}
		problems := checkPackage(*dir, *version, t)
		if len(problems) == 0 {
			fmt.Fprintf(stdout, "complete: %s\n", name)
			continue
		}
		failed = true
		for _, p := range problems {
			fmt.Fprintf(stderr, "INCOMPLETE %s: %s\n", name, p)
		}
	}
	for _, t := range updates.SupportedTargets() {
		if *packages {
			fmt.Fprintf(stdout, "not checked: %s (not asked for)\n", updates.AssetName(*version, t.Platform, t.Arch))
			continue
		}
		if len(only) > 0 && !slices.Contains(only, t.Platform) {
			fmt.Fprintf(stdout, "not checked: %s (not asked for)\n", updates.AssetName(*version, t.Platform, t.Arch))
			continue
		}
		name := updates.AssetName(*version, t.Platform, t.Arch)
		signed := !slices.Contains(unsigned, t.Platform)
		problems := checkArchive(*dir, *version, t, signed)
		if len(problems) == 0 {
			note := ""
			if !signed {
				note = "; signatures not asked for (-unsigned): not a release's check"
			}
			fmt.Fprintf(stdout, "complete: %s (%d files%s)\n", name, len(updates.ArchiveFiles(t.Platform)), note)
			continue
		}
		failed = true
		for _, p := range problems {
			fmt.Fprintf(stderr, "INCOMPLETE %s: %s\n", name, p)
		}
	}
	if failed {
		return 1
	}
	return 0
}

func checkArchive(dir, version string, t updates.Target, signed bool) []string {
	raw, err := os.ReadFile(filepath.Join(dir, updates.AssetName(version, t.Platform, t.Arch)))
	if errors.Is(err, os.ErrNotExist) {
		return []string{"is not in " + dir}
	}
	if err != nil {
		return []string{err.Error()}
	}
	members, err := archiveMembers(t.Platform, raw)
	if err != nil {
		return []string{err.Error()}
	}
	var problems []string
	say := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }

	for _, want := range updates.ArchiveFiles(t.Platform) {
		switch got := members[want]; {
		case len(got) == 0:
			say("%s is missing", want)
		case len(got) > 1:
			say("%s appears %d times", want, len(got))
		case len(got[0]) == 0:
			say("%s is empty or not a regular file", want)
		}
	}

	installs, err := updates.InstalledFrom(t.Platform, raw)
	if err != nil {
		say("the updater refuses it: %v", err)
	} else if want := updates.Installs(t.Platform); !slices.Equal(installs, want) {
		say("an update would install %q; the platform's installer puts %q on disk", installs, want)
	}
	for _, p := range updates.ShippedPrograms(t.Platform) {
		got := members[p.Name]
		if len(got) != 1 || len(got[0]) == 0 {
			continue
		}
		for _, why := range checkProgram(t, got[0], signed) {
			say("%s (%s) %s", p.Name, p.Command, why)
		}
	}

	if t.Platform == "macos" && signed && len(members["aii"]) == 1 {
		bundle := updates.BundleAssetName(version, t.Platform, t.Arch)
		inBundle, err := bundleBinary(filepath.Join(dir, bundle))
		switch {
		case err != nil:
			say("aii cannot be held to the bundle's: %v", err)
		case !bytes.Equal(inBundle, members["aii"][0]):
			say("aii is not the file %s carries at Contents/MacOS/aii", bundle)
		}
	}
	return problems
}

func archiveMembers(platform string, raw []byte) (map[string][][]byte, error) {
	members := map[string][][]byte{}
	if platform == "windows" {
		zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
		if err != nil {
			return nil, fmt.Errorf("not a zip archive: %w", err)
		}
		for _, f := range zr.File {
			var body []byte
			if f.Mode().IsRegular() {
				rc, err := f.Open()
				if err != nil {
					return nil, fmt.Errorf("%s: %w", f.Name, err)
				}
				body, err = io.ReadAll(rc)
				rc.Close()
				if err != nil {
					return nil, fmt.Errorf("%s: %w", f.Name, err)
				}
			}
			members[f.Name] = append(members[f.Name], body)
		}
		return members, nil
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("not a gzip archive: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return members, nil
		}
		if err != nil {
			return nil, fmt.Errorf("not a tar archive: %w", err)
		}
		var body []byte
		if hdr.Typeflag == tar.TypeReg {
			if body, err = io.ReadAll(tr); err != nil {
				return nil, fmt.Errorf("%s: %w", hdr.Name, err)
			}
		}
		members[hdr.Name] = append(members[hdr.Name], body)
	}
}

func bundleBinary(path string) ([]byte, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, ".app/Contents/MacOS/aii") && f.Mode().IsRegular() {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("%s carries no Contents/MacOS/aii", filepath.Base(path))
}

func checkProgram(t updates.Target, image []byte, signed bool) []string {
	var problems []string
	r := bytes.NewReader(image)
	switch t.Platform {
	case "linux":
		f, err := elf.NewFile(r)
		if err != nil {
			return []string{"is not an ELF executable: " + err.Error()}
		}
		if want := map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}[t.Arch]; f.Machine != want {
			problems = append(problems, fmt.Sprintf("is built for %v, not %s", f.Machine, t.Arch))
		}
	case "macos":
		f, err := macho.NewFile(r)
		if err != nil {
			return []string{"is not a Mach-O executable: " + err.Error()}
		}
		if want := map[string]macho.Cpu{"arm64": macho.CpuArm64, "amd64": macho.CpuAmd64}[t.Arch]; f.Cpu != want {
			problems = append(problems, fmt.Sprintf("is built for %v, not %s", f.Cpu, t.Arch))
		}
		if signed && !machoPublisherSigned(f, image) {
			problems = append(problems, "carries no Developer ID signature (none, or the linker's ad hoc one)")
		}
	case "windows":
		f, err := pe.NewFile(r)
		if err != nil {
			return []string{"is not a PE executable: " + err.Error()}
		}
		if want := map[string]uint16{"amd64": pe.IMAGE_FILE_MACHINE_AMD64, "arm64": pe.IMAGE_FILE_MACHINE_ARM64}[t.Arch]; f.Machine != want {
			problems = append(problems, fmt.Sprintf("is built for machine %#x, not %s", f.Machine, t.Arch))
		}
		types := peResourceTypes(f)
		for id, what := range map[uint32]string{rtGroupIcon: "icon", rtVersion: "name, publisher and version"} {
			if !slices.Contains(types, id) {
				problems = append(problems, "carries no "+what+" resource: it was not built as the installer's is (packaging/windows/build-setup.sh)")
			}
		}
		if signed && !peAuthenticodeSigned(f, image) {
			problems = append(problems, "carries no Authenticode signature")
		}
	}
	slices.Sort(problems)
	return problems
}

const (
	rtGroupIcon = 14
	rtVersion   = 16
)

func peDataDirectory(f *pe.File, index int) (pe.DataDirectory, bool) {
	switch h := f.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		if index < int(h.NumberOfRvaAndSizes) {
			return h.DataDirectory[index], true
		}
	case *pe.OptionalHeader32:
		if index < int(h.NumberOfRvaAndSizes) {
			return h.DataDirectory[index], true
		}
	}
	return pe.DataDirectory{}, false
}

func peResourceTypes(f *pe.File) []uint32 {
	dd, ok := peDataDirectory(f, pe.IMAGE_DIRECTORY_ENTRY_RESOURCE)
	if !ok || dd.Size == 0 {
		return nil
	}
	for _, s := range f.Sections {
		if dd.VirtualAddress < s.VirtualAddress || dd.VirtualAddress >= s.VirtualAddress+s.VirtualSize {
			continue
		}
		data, err := s.Data()
		if err != nil {
			return nil
		}
		root := data[min(int(dd.VirtualAddress-s.VirtualAddress), len(data)):]

		if len(root) < 16 {
			return nil
		}
		named, numbered := int(binary.LittleEndian.Uint16(root[12:])), int(binary.LittleEndian.Uint16(root[14:]))
		var types []uint32
		for i := named; i < named+numbered; i++ {
			at := 16 + 8*i
			if at+8 > len(root) {
				return types
			}
			types = append(types, binary.LittleEndian.Uint32(root[at:]))
		}
		return types
	}
	return nil
}

func peAuthenticodeSigned(f *pe.File, image []byte) bool {

	dd, ok := peDataDirectory(f, pe.IMAGE_DIRECTORY_ENTRY_SECURITY)
	if !ok || dd.Size < 8 || uint64(dd.VirtualAddress)+8 > uint64(len(image)) {
		return false
	}

	entry := image[dd.VirtualAddress:]
	const winCertTypePKCSSignedData = 0x0002
	return binary.LittleEndian.Uint32(entry) > 8 && binary.LittleEndian.Uint16(entry[6:]) == winCertTypePKCSSignedData
}

func machoPublisherSigned(f *macho.File, image []byte) bool {
	const (
		lcCodeSignature   = 0x1d
		embeddedSignature = 0xfade0cc0
		cmsSlot           = 0x10000
		cmsBlob           = 0xfade0b01
	)
	for _, l := range f.Loads {
		raw := l.Raw()
		if len(raw) < 16 || f.ByteOrder.Uint32(raw) != lcCodeSignature {
			continue
		}
		off, size := uint64(f.ByteOrder.Uint32(raw[8:])), uint64(f.ByteOrder.Uint32(raw[12:]))
		if off+size > uint64(len(image)) || size < 12 {
			return false
		}
		blob := image[off : off+size]
		if binary.BigEndian.Uint32(blob) != embeddedSignature {
			return false
		}
		for i := 0; i < int(binary.BigEndian.Uint32(blob[8:])); i++ {
			at := 12 + 8*i
			if at+8 > len(blob) {
				return false
			}
			slot, where := binary.BigEndian.Uint32(blob[at:]), uint64(binary.BigEndian.Uint32(blob[at+4:]))
			if slot != cmsSlot || where+8 > uint64(len(blob)) {
				continue
			}

			return binary.BigEndian.Uint32(blob[where:]) == cmsBlob && binary.BigEndian.Uint32(blob[where+4:]) > 8
		}
	}
	return false
}

func runArchiveFiles(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("archive-files", flag.ContinueOnError)
	fs.SetOutput(stderr)
	platform := fs.String("platform", "", "release platform (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	files := updates.ArchiveFiles(*platform)
	if len(files) == 0 {
		fmt.Fprintf(stderr, "aii-release: no release is made for platform %q\n", *platform)
		return 2
	}
	for _, f := range files {
		fmt.Fprintln(stdout, f)
	}
	return 0
}

func runPrograms(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("programs", flag.ContinueOnError)
	fs.SetOutput(stderr)
	platform := fs.String("platform", "", "release platform (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	programs := updates.ShippedPrograms(*platform)
	if len(programs) == 0 {
		fmt.Fprintf(stderr, "aii-release: no release is made for platform %q\n", *platform)
		return 2
	}
	for _, p := range programs {
		fmt.Fprintf(stdout, "%s\t%s\n", p.Command, p.Name)
	}
	return 0
}
