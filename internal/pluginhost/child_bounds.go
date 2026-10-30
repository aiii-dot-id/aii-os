package pluginhost

import (
	"time"

	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

const (
	DefaultStopBudget = 5 * time.Second

	DefaultHandOverBudget = 30 * time.Second

	DefaultSessionControlBound = 5 * time.Second

	DefaultRestartAttempts = supervisor.DefaultMaxRestarts
)

type ChildBounds struct {
	Stop           time.Duration
	HandOver       time.Duration
	SessionControl time.Duration

	RestartAttempts int
}

func (b ChildBounds) StopBudget() time.Duration { return orDefault(b.Stop, DefaultStopBudget) }

func (b ChildBounds) HandOverBudget() time.Duration {
	return orDefault(b.HandOver, DefaultHandOverBudget)
}

func (b ChildBounds) SessionControlBound() time.Duration {
	return orDefault(b.SessionControl, DefaultSessionControlBound)
}

func (b ChildBounds) Retirement() time.Duration {
	return b.StopBudget() + supervisor.KillReapBound()
}

func orDefault(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

func (o *Options) bounds() ChildBounds {
	if o == nil {
		return ChildBounds{}
	}
	return o.Bounds
}

func (ap *ActivePlugin) StopBudget() time.Duration { return ap.bounds.StopBudget() }
