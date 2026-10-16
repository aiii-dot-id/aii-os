package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/hostcap"
	"github.com/aiii-dot-id/aii-os/internal/updates"
)

const ThirdPartyLicensesFile = "THIRD_PARTY_LICENSES"

func shippedCommands(platform string) []string {
	var commands []string
	for _, p := range updates.ShippedPrograms(platform) {
		commands = append(commands, p.Command)
	}
	return append(commands, installerCommands[platform]...)
}

func goosOf(platform string) string {
	if platform == "macos" {
		return "darwin"
	}
	return platform
}

type thirdPartyModule struct {
	Path, Version, Dir string
}

func thirdPartyModules(goBin, repo string) ([]thirdPartyModule, error) {
	seen := map[string]thirdPartyModule{}
	for _, target := range updates.SupportedTargets() {
		mods, err := modulesForTarget(goBin, repo, target.Platform, target.Arch)
		if err != nil {
			return nil, err
		}
		for _, m := range mods {
			seen[m.Path] = m
		}
	}
	mods := make([]thirdPartyModule, 0, len(seen))
	for _, m := range seen {
		mods = append(mods, m)
	}
	sort.Slice(mods, func(i, j int) bool { return mods[i].Path < mods[j].Path })
	return mods, nil
}

func modulesForTarget(goBin, repo, platform, goarch string) ([]thirdPartyModule, error) {
	commands, goos := shippedCommands(platform), goosOf(platform)
	if len(commands) == 0 {
		return nil, fmt.Errorf("no shipped commands are named for %s", platform)
	}
	args := append([]string{"list", "-deps", "-f", `{{with .Module}}{{if not .Main}}{{.Path}}{{"\t"}}{{.Version}}{{"\t"}}{{.Dir}}{{end}}{{end}}`}, commands...)
	out, err := goTool(goBin, repo, []string{"GOOS=" + goos, "GOARCH=" + goarch, "CGO_ENABLED=0", "GOFLAGS=-mod=readonly", "GOWORK=off"}, args...)
	if err != nil {
		return nil, fmt.Errorf("go list for %s/%s: %v", goos, goarch, err)
	}
	seen := map[string]bool{}
	var mods []thirdPartyModule
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 3 || f[0] == "" || seen[f[0]] {
			continue
		}
		if f[2] == "" {
			return nil, fmt.Errorf("module %s %s is not in the module cache", f[0], f[1])
		}
		seen[f[0]] = true
		mods = append(mods, thirdPartyModule{Path: f[0], Version: f[1], Dir: f[2]})
	}
	return mods, nil
}

func goTool(goBin, dir string, env []string, args ...string) ([]byte, error) {
	if can := hostcap.Can(hostcap.Subprocess); !can.Available {
		return nil, fmt.Errorf("the go tool cannot be run here: %s", can.Reason)
	}
	cmd := exec.Command(goBin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func licenseFilesIn(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		upper := strings.ToUpper(e.Name())
		if strings.Contains(upper, "LICENSE") || strings.Contains(upper, "LICENCE") ||
			strings.HasPrefix(upper, "COPYING") || strings.HasPrefix(upper, "NOTICE") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

const licensesRule = "================================================================================\n"

func renderThirdPartyLicenses(goLicense []byte, mods []thirdPartyModule) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("THIRD-PARTY SOFTWARE IN AII OS\n\n")
	b.WriteString("The AII OS programs are built from the software listed below, each under its own\n")
	b.WriteString("licence, which is reproduced here in full as that licence asks. AII OS itself is\n")
	b.WriteString("under the licence in the file LICENSE beside this one.\n\n")
	b.WriteString("This file is generated from the module graph of the shipped programs when\n")
	b.WriteString("they are packaged (aii-release licenses) and is not edited by hand.\n\n")
	section := func(title, file string, text []byte) {
		b.WriteString(licensesRule)
		b.WriteString(title + "\n" + file + "\n")
		b.WriteString(licensesRule)
		b.Write(bytes.TrimRight(bytes.ReplaceAll(text, []byte("\r\n"), []byte("\n")), "\n"))
		b.WriteString("\n\n")
	}
	if len(bytes.TrimSpace(goLicense)) == 0 {
		return nil, errors.New("the Go distribution's LICENSE is empty or was not found")
	}
	section("The Go programming language: runtime and standard library", "LICENSE", goLicense)
	for _, m := range mods {
		names, err := licenseFilesIn(m.Dir)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %v", m.Path, m.Version, err)
		}
		if len(names) == 0 {
			return nil, fmt.Errorf("%s %s has no licence file at its root (%s): its terms must be found and added by hand", m.Path, m.Version, m.Dir)
		}
		for _, name := range names {
			text, err := os.ReadFile(filepath.Join(m.Dir, name))
			if err != nil {
				return nil, fmt.Errorf("%s %s: %v", m.Path, m.Version, err)
			}
			section(m.Path+" "+m.Version, name, text)
		}
	}

	return append(bytes.TrimRight(b.Bytes(), "\n"), '\n'), nil
}

func generateThirdPartyLicenses(goBin, repo string) ([]byte, error) {
	root, err := goTool(goBin, repo, nil, "env", "GOROOT")
	if err != nil {
		return nil, fmt.Errorf("go env GOROOT: %v", err)
	}
	goLicense, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(root)), "LICENSE"))
	if err != nil {
		return nil, fmt.Errorf("the Go distribution's LICENSE: %v", err)
	}
	mods, err := thirdPartyModules(goBin, repo)
	if err != nil {
		return nil, err
	}
	return renderThirdPartyLicenses(goLicense, mods)
}

func runLicenses(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("licenses", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("o", "", "write here (default: standard output)")
	repo := fs.String("repo", ".", "the repository root")
	goBin := fs.String("go", "go", "the go tool")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	data, err := generateThirdPartyLicenses(*goBin, *repo)
	if err != nil {
		fmt.Fprintf(stderr, "aii-release: %v\n", err)
		return 1
	}
	if *out == "" {
		_, _ = stdout.Write(data)
		return 0
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fmt.Fprintf(stderr, "aii-release: %v\n", err)
		return 1
	}
	return 0
}
