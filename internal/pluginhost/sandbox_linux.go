//go:build linux

package pluginhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func containArgv(argv []string) ([]string, string, error) {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		return nil, "", fmt.Errorf("bubblewrap is not installed; native T3 plugins are not run uncontained (install bwrap)")
	}
	if len(argv) == 0 {
		return nil, "", fmt.Errorf("nothing to contain")
	}
	wrapped := []string{
		bwrap,
		"--unshare-all",
		"--die-with-parent",
		"--ro-bind", "/", "/",
		"--dev", "/dev",
	}
	wrapped = append(wrapped, credentialMasks()...)
	wrapped = append(wrapped, "--")
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	return append(wrapped, argv...), "contained (bubblewrap: no network, read-only filesystem, ssh and shadow files masked; other user-readable credentials are NOT)", nil
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func credentialMasks() []string {
	var m []string
	// .
	// .
	// .
	// .
	// .
	// .
	dirs := []string{"/etc/ssh", "/root/.ssh"}
	if h, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(h, ".ssh"))
	}
	seen := map[string]bool{}
	for _, d := range dirs {
		// .
		// .
		if r, err := filepath.EvalSymlinks(d); err == nil {
			d = r
		}
		if seen[d] {
			continue
		}
		seen[d] = true
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			m = append(m, "--tmpfs", d)
		}
	}
	for _, f := range []string{"/etc/shadow", "/etc/gshadow"} {
		if st, err := os.Stat(f); err == nil && st.Mode().IsRegular() {
			m = append(m, "--ro-bind", "/dev/null", f)
		}
	}
	return m
}
