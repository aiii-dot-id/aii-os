package tools

import (
	"go/build"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// platform_matrix_test.go — every mandated platform must define the
// predicates the shared code calls, and define them exactly once.
//
// This exists because I broke the matrix twice in one day, the same way
// both times: a helper was added to the unix and windows partitions and
// not to the mobile one, and tools.go calls it unconditionally. Linux,
// macOS and Windows stayed green while android went red, so nothing in
// the host gate noticed — the five-platform law is precisely the law
// that a green host cannot enforce.
//
// go/build resolves the constraints exactly as the compiler does,
// including the alias rule that makes android satisfy "linux" and ios
// satisfy "darwin" for both //go:build tags and _suffix filenames. That
// alias is why "it compiles on linux" says nothing about android.
//
// A compile of every row would also catch this, and CI runs one. This
// is the version that fails in SECONDS, at the boundary that owns the
// symbol, and names which platform is missing which predicate.
func TestEveryPlatformDefinesItsSharedPredicates(t *testing.T) {
	// The predicates shared code calls unconditionally. A symbol added
	// here without a definition in every partition fails immediately.
	predicates := []string{"isRooted", "normalizeShellOutput"}

	// The mandated matrix. Mobile leaves BETA QUALIFICATION scope, never
	// the build matrix (BETA1_CONTRACT support matrix).
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
