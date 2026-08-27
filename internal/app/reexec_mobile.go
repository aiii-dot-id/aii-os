//go:build android || ios

package app

import "fmt"

// reexecSelf cannot exist on the mobile app host: iOS forbids exec,
// and the runtime is a library inside a host app with no standalone
// binary to become. The startLive hostcap.SelfReplace gate makes this
// unreachable; if a future path reaches it anyway, it fails in words
// instead of an OS signal.
func reexecSelf() error {
	return fmt.Errorf("re-exec is impossible on the mobile app host: the platform store owns the binary lifecycle")
}
