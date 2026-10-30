package app

import (
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

var (
	stopSetting = cognitionSetting{key: "plugins.runtime.stop_ms", min: 1, max: 600_000, unit: "milliseconds", zero: true,
		def: fixed(int(pluginhost.DefaultStopBudget / time.Millisecond)),
		num: func(c *Config) *int { return &c.Plugins.Runtime.StopMS }}

	handOverSetting = cognitionSetting{key: "plugins.runtime.hand_over_ms", min: 1, max: 3_600_000, unit: "milliseconds", zero: true,
		def: fixed(int(pluginhost.DefaultHandOverBudget / time.Millisecond)),
		num: func(c *Config) *int { return &c.Plugins.Runtime.HandOverMS }}

	sessionControlSetting = cognitionSetting{key: "plugins.runtime.session_control_ms", min: 1, max: 60_000, unit: "milliseconds", zero: true,
		def: fixed(int(pluginhost.DefaultSessionControlBound / time.Millisecond)),
		num: func(c *Config) *int { return &c.Plugins.Runtime.SessionControlMS }}

	restartAttemptsSetting = cognitionSetting{key: "plugins.runtime.restart_attempts", min: 1, max: 100, unit: "attempts in ten minutes", zero: true,
		def: fixed(pluginhost.DefaultRestartAttempts),
		num: func(c *Config) *int { return &c.Plugins.Runtime.RestartAttempts }}
)

func isChildBoundKey(key string) bool {
	switch key {
	case stopSetting.key, handOverSetting.key, sessionControlSetting.key, restartAttemptsSetting.key:
		return true
	}
	return false
}

func childBoundsOf(c Config) pluginhost.ChildBounds {
	return pluginhost.ChildBounds{
		Stop:            time.Duration(stopSetting.n(c)) * time.Millisecond,
		HandOver:        time.Duration(handOverSetting.n(c)) * time.Millisecond,
		SessionControl:  time.Duration(sessionControlSetting.n(c)) * time.Millisecond,
		RestartAttempts: restartAttemptsSetting.n(c),
	}
}

func (a *App) childBounds() pluginhost.ChildBounds { return childBoundsOf(a.boundsConfig()) }
