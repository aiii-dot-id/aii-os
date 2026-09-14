//go:build android || ios

package app

import "fmt"

// .
// .
// .
// .
// .
func reexecSelf() error {
	return fmt.Errorf("re-exec is impossible on the mobile app host: the platform store owns the binary lifecycle")
}
