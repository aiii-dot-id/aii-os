//go:build darwin && !ios

package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/aiii-dot-id/aii-os/internal/firewall"
)

const shellWallDescription = " Commands run behind the operating system's wall: one that reaches a place the host keeps fails there with 'Operation not permitted'. ps, top, sudo, crontab and at do not run behind it; pgrep and pkill do."

const sandboxExec = "/usr/bin/sandbox-exec"

func behindWall(home string, w shellWall, bin string, argv []string) (string, []string, error) {
	if _, err := os.Stat(sandboxExec); err != nil {
		return "", nil, fmt.Errorf("the operating system's wall (%s) is not available: %w", sandboxExec, err)
	}
	profile, err := shellProfile(home, w, perUserTempDirs())
	if err != nil {
		return "", nil, err
	}
	wrapped := append([]string{"-p", profile, "--", bin}, argv...)
	return sandboxExec, wrapped, nil
}

func shellProfile(home string, w shellWall, userTemps []string) (string, error) {
	in := firewall.ShellWallInput{
		Home:     firewall.ResolvePath(home),
		Roots:    resolveAll(w.roots),
		Kept:     resolvedPlaces(w.kept),
		Held:     heldNodes(w.kept),
		Boundary: w.boundary,
	}
	if w.boundary {
		in.Temp = darwinShellTemps(home, userTemps)
	}
	return firewall.ShellProfile(in)
}

func resolvedPlaces(rules []*firewall.Rule) []*firewall.Rule {
	out := make([]*firewall.Rule, 0, len(rules))
	for _, r := range rules {
		if r == nil || r.Path == "" {
			continue
		}
		c := *r
		c.Path = firewall.ResolvePath(r.Path)
		if r.Stem {
			c.Path = firewall.ResolveSlot(r.Path)
		}
		c.Except = nil
		for _, e := range r.Except {
			if e == "" {
				continue
			}
			x := firewall.ResolveSlot(e)
			if r.Stem && strings.HasPrefix(x, c.Path) || !r.Stem && strings.HasPrefix(x, strings.TrimSuffix(c.Path, "/")+"/") {
				c.Except = append(c.Except, x)
			}
		}
		out = append(out, &c)
	}
	return out
}

func heldNodes(rules []*firewall.Rule) []string {
	var held []string
	for _, r := range rules {
		if r == nil || r.Path == "" || !filepath.IsAbs(r.Path) {
			continue
		}
		for p := filepath.Clean(r.Path); ; p = filepath.Dir(p) {
			held = append(held, firewall.ResolveSlot(p))
			if filepath.Dir(p) == p {
				break
			}
		}
	}
	return held
}

func resolveAll(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p != "" {
			out = append(out, firewall.ResolvePath(p))
		}
	}
	return out
}

func darwinShellTemps(home string, userTemps []string) []string {
	temps := append([]string{}, userTemps...)
	temps = append(temps, firewall.ResolvePath("/private/tmp"), firewall.ResolvePath("/private/var/tmp"))
	if vol := volumeOf(home); vol != "" {

		staging := filepath.Join(vol, ".TemporaryItems", "folders."+strconv.Itoa(os.Getuid()))
		temps = append(temps, firewall.ResolvePath(staging))
	}
	return temps
}

var userFolders = firewall.ResolvePath("/var/folders")

var perUserTempDirs = sync.OnceValue(userTempDirs)

func userTempDirs() []string { return userTempDirsIn(userFolders) }

func userTempDirsIn(root string) []string {
	var out []string
	uid := os.Getuid()
	matches, err := filepath.Glob(filepath.Join(root, "*", "*", "T"))
	if err != nil {
		return nil
	}
	for _, m := range matches {
		info, err := os.Lstat(m)
		if err != nil || !info.Mode().IsDir() {
			continue
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) == uid {
			if r := firewall.ResolvePath(m); !slices.Contains(out, r) {
				out = append(out, r)
			}
		}
	}
	return out
}

func volumeOf(dir string) string {
	at := firewall.ResolvePath(dir)
	for {
		var st syscall.Statfs_t
		if err := syscall.Statfs(at, &st); err == nil {
			mnt := bytesToString(st.Mntonname[:])
			if mnt == "/" {
				return ""
			}
			return mnt
		}
		parent := filepath.Dir(at)
		if parent == at {
			return ""
		}
		at = parent
	}
}

func bytesToString(b []int8) string {
	buf := make([]byte, 0, len(b))
	for _, c := range b {
		if c == 0 {
			break
		}
		buf = append(buf, byte(c))
	}
	return string(buf)
}
