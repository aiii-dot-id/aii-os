package pluginhost

import (
	"time"

	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

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
// .
// .
// .

// .
// .
// .
type StartupAllowance struct {
	Effective time.Duration
	Requested time.Duration
	Ceiling   time.Duration
	Source    string
	Capped    bool
}

// .
// .
const DefaultStartupCeiling = 300 * time.Second

// .
// .
// .
func startupAllowance(opts *Options, pluginID string, profile *AcceleratorProfile) StartupAllowance {
	out := StartupAllowance{Source: "default", Requested: supervisor.DefaultReadyTimeout, Ceiling: DefaultStartupCeiling}
	if opts != nil && opts.StartupCeiling > 0 {
		out.Ceiling = opts.StartupCeiling
	}
	switch {
	case opts != nil && opts.ReadyTimeout[pluginID] > 0:
		out.Source, out.Requested = "operator", opts.ReadyTimeout[pluginID]
	case profile != nil && profile.StartupMS != nil && *profile.StartupMS > 0:
		out.Source, out.Requested = "package", time.Duration(*profile.StartupMS)*time.Millisecond
	}
	out.Effective = out.Requested
	if out.Effective > out.Ceiling {
		out.Effective, out.Capped = out.Ceiling, true
	}
	return out
}

// .
// .
// .
// .
func (a StartupAllowance) Sentence() string {
	asked := ""
	switch a.Source {
	case "operator":
		asked = "you set " + a.Requested.String()
	case "package":
		asked = "the package asked for " + a.Requested.String()
	default:
		asked = "the default is " + a.Requested.String()
	}
	if a.Capped {
		return asked + "; capped at the ceiling " + a.Ceiling.String()
	}
	return asked + "; ceiling " + a.Ceiling.String()
}
