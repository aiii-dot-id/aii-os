package tools

import (
	"go/build"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
func TestEveryPlatformDefinesItsSharedPredicates(t *testing.T) {
	// .
	// .
	predicates := []string{"isRooted", "normalizeShellOutput"}

	// .
	// .
	platforms := []struct{ goos, goarch string }{
		{"linux", "amd64"}, {"linux", "arm64"},
		{"darwin", "arm64"},
		{"windows", "amd64"},
		{"android", "arm64"},
		{"ios", "arm64"},
	}

	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range platforms {
		t.Run(p.goos+"/"+p.goarch, func(t *testing.T) {
			ctx := build.Default
			ctx.GOOS, ctx.GOARCH = p.goos, p.goarch
			ctx.CgoEnabled = false
			pkg, err := ctx.ImportDir(dir, 0)
			if err != nil {
				t.Fatalf("resolving build constraints for %s/%s: %v", p.goos, p.goarch, err)
			}
			for _, pred := range predicates {
				count := 0
				var where []string
				for _, f := range pkg.GoFiles {
					src, err := os.ReadFile(filepath.Join(dir, f))
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(string(src), "func "+pred+"(") {
						count++
						where = append(where, f)
					}
				}
				switch {
				case count == 0:
					t.Errorf("%s/%s compiles no definition of %s(), but shared code calls it — this platform will not build",
						p.goos, p.goarch, pred)
				case count > 1:
					t.Errorf("%s/%s compiles %d definitions of %s() (%s) — the partitions overlap",
						p.goos, p.goarch, count, pred, strings.Join(where, ", "))
				}
			}
		})
	}
}
