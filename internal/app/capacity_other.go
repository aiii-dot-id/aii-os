//go:build !linux && !windows && !darwin

package app

import "github.com/aiii-dot-id/aii-os/internal/pluginfacility"

// .
type hostCapacity struct{}

func (hostCapacity) Measure() pluginfacility.Availability { return pluginfacility.Availability{} }
