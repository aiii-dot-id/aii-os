//go:build !darwin

package updates

import "fmt"

func applyIfBundle(target, []byte, string, string, string) (handled, already bool, err error) {
	return false, false, nil
}

func swapBundles(a, b string) error {
	return fmt.Errorf("an application bundle is exchanged only on macOS: %s <-> %s", a, b)
}
