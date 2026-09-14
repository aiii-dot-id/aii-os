package pluginhost

import (
	"runtime"
	"testing"
)

// .
// .
// .
// .
func skipWhereNativeIsRefused(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" && !WindowsContainedNativeQualified {
		t.Skip("native T3 is not admitted on Windows until the wall is qualified with the real backend")
	}
}

// .
// .
var exeSuffix = map[bool]string{true: ".exe"}[runtime.GOOS == "windows"]
