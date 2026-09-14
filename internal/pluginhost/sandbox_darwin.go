//go:build darwin

package pluginhost

import (
	"fmt"
	"os/exec"
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
// .
// .
const seatbeltProfile = `(version 1)
(allow default)
(deny network*)
(deny file-write*)
(deny file-read* (literal "/etc/master.passwd"))
(deny file-read* (subpath "/etc/ssh"))
(deny file-read* (subpath "/var/root/.ssh"))
(deny file-read* (regex #"^/Users/[^/]+/\.ssh"))
`

func containArgv(argv []string) ([]string, string, error) {
	if len(argv) == 0 {
		return nil, "", fmt.Errorf("nothing to contain")
	}
	sb, err := exec.LookPath("sandbox-exec")
	if err != nil {
		// .
		// .
		// .
		// .
		// .
		return nil, "", fmt.Errorf("sandbox-exec is not on PATH; native T3 plugins are not run uncontained")
	}
	wrapped := append([]string{sb, "-p", seatbeltProfile, "--"}, argv...)
	// .
	// .
	// .
	// .
	return wrapped, "contained (Seatbelt: no network, read-only filesystem, ssh and master.passwd denied; other user-readable credentials are NOT)", nil
}
