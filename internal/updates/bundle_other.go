//go:build !darwin

package updates

func applyIfBundle(target, []byte, string) (handled, already bool, err error) {
	return false, false, nil
}
