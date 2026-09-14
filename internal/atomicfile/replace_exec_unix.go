//go:build !windows

package atomicfile

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
func ReplaceExecutable(srcPath, dstPath string) (published bool, err error) {
	return Replace(srcPath, dstPath)
}
