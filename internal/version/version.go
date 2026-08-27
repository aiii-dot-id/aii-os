// Package version owns AII OS semantic-version comparison. Values use the
// unprefixed form stored in VERSION and signed manifests.
package version

import (
	"strings"

	"golang.org/x/mod/semver"
)

// Valid reports whether v is a semantic version without a leading "v".
func Valid(v string) bool {
	core := v
	if i := strings.IndexAny(core, "-+"); i >= 0 {
		core = core[:i]
	}
	return strings.Count(core, ".") == 2 && semver.IsValid("v"+v)
}

// Compare compares two valid versions.
func Compare(a, b string) int { return semver.Compare("v"+a, "v"+b) }
