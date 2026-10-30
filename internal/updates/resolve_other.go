//go:build !windows

package updates

import "errors"

func finalPathOf(string) (string, error) {
	return "", errors.ErrUnsupported
}
