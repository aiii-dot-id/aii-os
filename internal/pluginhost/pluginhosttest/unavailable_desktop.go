//go:build !android && !ios

package pluginhosttest

import (
	"errors"

	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

func UnavailableNotStopped(err error) bool {
	var unavailable *supervisor.UnavailableError
	return errors.As(err, &unavailable) && unavailable.State != supervisor.StateStopped
}
