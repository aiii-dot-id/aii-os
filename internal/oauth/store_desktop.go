//go:build !android && !ios

// store_desktop.go — where adoptable credentials live on a desktop or
// server: under the operator's home directory, exactly where the vendor
// CLI put them. os.UserHomeDir resolves $HOME on Linux and macOS and
// %USERPROFILE% on Windows, and filepath.Join supplies the separator, so
// the same relative layout (.claude/.credentials.json, .codex/auth.json)
// addresses all three.
package oauth

import "os"

// platformAdopts reports whether this platform can adopt another tool's
// credential file at all.
const platformAdopts = true

func credentialHome() (string, error) { return os.UserHomeDir() }
