// .
// .
package version

import (
	_ "embed"
	"strings"

	"golang.org/x/mod/semver"
)

// .
// .
// .
// .
// .
// .

//go:embed VERSION
var raw string

// .
// .
// .
// .
func Authored() string { return strings.TrimSpace(raw) }

// .
func Valid(v string) bool {
	core := v
	if i := strings.IndexAny(core, "-+"); i >= 0 {
		core = core[:i]
	}
	return strings.Count(core, ".") == 2 && semver.IsValid("v"+v)
}

// .
func Compare(a, b string) int { return semver.Compare("v"+a, "v"+b) }
