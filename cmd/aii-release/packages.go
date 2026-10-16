package main

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/hostcap"
	"github.com/aiii-dot-id/aii-os/internal/updates"
)

func checkPackage(dir, version string, t updates.Target) []string {
	path := filepath.Join(dir, updates.PackageAssetName(version, t.Arch))
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return []string{"is not in " + dir}
	}
	arch, files, err := packageContents(path)
	if err != nil {
		return []string{err.Error()}
	}
	var problems []string
	say := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }
	if arch != t.Arch {
		say("declares Architecture %q, not %s", arch, t.Arch)
	}
	for _, p := range updates.ShippedPrograms(t.Platform) {
		image, ok := files["usr/bin/"+p.Name]
		if !ok || len(image) == 0 {
			say("usr/bin/%s is missing", p.Name)
			continue
		}
		for _, why := range checkProgram(t, image, false) {
			say("usr/bin/%s (%s) %s", p.Name, p.Command, why)
		}
	}
	for _, f := range updates.ArchiveFiles(t.Platform) {
		if strings.HasPrefix(f, "share/") && len(files["usr/"+f]) == 0 {
			say("usr/%s is missing", f)
		}
	}
	return problems
}

func packageContents(path string) (arch string, files map[string][]byte, err error) {
	if can := hostcap.Can(hostcap.Subprocess); !can.Available {
		return "", nil, fmt.Errorf("cannot be read here: %s", can.Reason)
	}
	run := func(args ...string) ([]byte, error) {
		cmd := exec.Command("dpkg-deb", args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if errors.Is(err, exec.ErrNotFound) {
			return nil, errors.New("cannot be read here: dpkg-deb is not on this machine")
		}
		if err != nil {
			return nil, fmt.Errorf("dpkg-deb %s: %v: %s", args[0], err, strings.TrimSpace(stderr.String()))
		}
		return out, nil
	}
	field, err := run("-f", path, "Architecture")
	if err != nil {
		return "", nil, err
	}
	fsys, err := run("--fsys-tarfile", path)
	if err != nil {
		return "", nil, err
	}
	files = map[string][]byte{}
	tr := tar.NewReader(bytes.NewReader(fsys))
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return strings.TrimSpace(string(field)), files, nil
		}
		if err != nil {
			return "", nil, fmt.Errorf("its file tree cannot be read: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			return "", nil, fmt.Errorf("%s: %w", hdr.Name, err)
		}
		files[strings.TrimPrefix(hdr.Name, "./")] = body
	}
}
