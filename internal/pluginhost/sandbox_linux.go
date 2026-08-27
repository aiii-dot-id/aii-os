//go:build linux

package pluginhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Containment for a NATIVE T3 child, Linux mechanism: bubblewrap.
//
// T0-T2 run as wasm under wazero, where a guest cannot perform I/O at
// all unless the host hands it a capability. Native T3 is the deliberate
// exception to that model — code that must be native — and until now it
// was an exception to the containment too: plain exec, the daemon's own
// filesystem, and nothing stopping it opening a socket.
//
// T3 IS AIII-SIGNED, WHICH ANSWERS PROVENANCE, NOT CORRECTNESS. Canon
// carves proc.spawn and secret.read out of T3 defaults explicitly
// ("regardless of trust tier"), because a bug in signed native code
// still reaches whatever the process can reach.
//
// --unshare-net is the sharpest line here. broker.go's rule is "a grant
// is not a socket": net.outbound means http.get through the broker, not
// a connection the plugin opens. That was a policy sentence a native
// plugin could simply ignore. Now there is no network in its namespace
// to open, and every byte it sends goes through the broker or nowhere.
//
// Read-only root and no writable path: the artifact is already extracted
// and the plugin talks over stdio, which bwrap passes through untouched.
// No --proc: mounting a fresh procfs fails when the daemon is ITSELF
// sandboxed (uid_map is unwritable through a read-only /proc), and the
// two halves of this story have to compose — a runtime-level wrapper
// would contain the daemon itself, this contains its plugins. (The
// wrapper is FUTURE WORK: an earlier comment here named
// scripts/aii-sandbox.sh as if it existed — it does not exist in this
// tree, and a comment recording aspiration as fact is exactly the
// doc-rot the 2026-08-26 review hunt caught.)
//
// Platform-owned file under the five-platform law, exactly like the
// address-space envelope beside it: the mechanism lives here, the honest
// no-op record lives in sandbox_other.go, and core code never branches
// on GOOS.
func containArgv(argv []string) ([]string, string, error) {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		// FAIL CLOSED. This used to return the bare argv with a
		// telemetry line, on the reasoning that "no mechanism is not a
		// mechanism refusing" — which is true of a PLATFORM that has
		// none, and false of a platform that has one and has not
		// installed it. Linux has one. A host missing bwrap ran signed
		// native code with the daemon's whole filesystem and an open
		// network, while PLUGIN_FRAMEWORK promised containment is
		// fail-closed.
		//
		// The operator gets a plugin that does not run and a reason they
		// can act on, rather than a plugin that runs with none of the
		// wall they were told about.
		return nil, "", fmt.Errorf("bubblewrap is not installed; native T3 plugins are not run uncontained (install bwrap)")
	}
	if len(argv) == 0 {
		return nil, "", fmt.Errorf("nothing to contain")
	}
	wrapped := []string{
		bwrap,
		"--unshare-all",     // net included: a grant is not a socket
		"--die-with-parent", // the child cannot outlive its supervisor
		"--ro-bind", "/", "/",
		"--dev", "/dev",
	}
	wrapped = append(wrapped, credentialMasks()...)
	wrapped = append(wrapped, "--")
	return append(wrapped, argv...), "contained (bubblewrap: no network, read-only filesystem, credential stores masked)", nil
}

// credentialMasks hides the stores the OS itself defines — the shadow
// files and ssh key directories — from the read-only root view. R75:
// native T3 is a FIRST-CLASS lane (human-level voice and local LLMs
// run there) and must not be penalized, so this is the evolution that
// costs a legitimate plugin nothing: no model file, library, or
// device node lives in these paths, and the 2026-08-26 probe opened
// /etc/shadow and listed /root/.ssh through the old whole-root view.
// Each mask is stat-gated: bwrap refuses a bind whose destination
// does not exist, and a missing store needs no mask. The device
// profile (accelerator nodes for NPU/GPU plugins) evolves WITH the
// first accelerated plugin, tested against it — not blind (R75).
func credentialMasks() []string {
	var m []string
	// The scope is what the DAEMON can reach as itself: the system
	// stores plus its own user's key dir. Other users' homes are the
	// OS's own perimeter — a non-root daemon cannot read them, and a
	// root daemon's operative dir is /root/.ssh. (A /home/* glob was
	// tried and dropped: the behavioral test caught bwrap refusing a
	// mountpoint under another user's home on this very host.)
	dirs := []string{"/etc/ssh", "/root/.ssh"}
	if h, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(h, ".ssh"))
	}
	seen := map[string]bool{}
	for _, d := range dirs {
		// Resolve symlinks so the mask lands on the path bwrap will
		// actually see when it mounts.
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
