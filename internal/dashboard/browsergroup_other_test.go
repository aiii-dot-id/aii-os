//go:build !windows && !(darwin && !ios) && !(linux && !android)

package dashboard

import "errors"

// processGroupMembers: browser tests run on the desktops; elsewhere the kernel is not asked.
func processGroupMembers(int) ([]int, error) { return nil, errors.ErrUnsupported }
