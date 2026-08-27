package app

import (
	"fmt"
	"log"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// logSink is the installed tee (nil when disabled). Run installs it
// as the first live act; stop closes it last.
func (a *App) installLogSink() {
	if a.logSink != nil {
		return
	}
	sink, err := logsink.Install(a.sinkConfig())
	if err != nil {
		// The stated destination must be honored loudly or rejected —
		// the WorkerBinary rule. Refuse to start on a bad destination.
		log.Fatalf("Startup failed: %v", err)
	}
	a.logSink = sink
}

// sinkConfig adapts the operator-facing LogsConfig (config.json's
// "logs" section) to the sink's own Config. Relative dirs resolve
// against the identity home — the same convention as data/.
func (a *App) sinkConfig() logsink.Config {
	cfg := a.configSnapshot()
	return logsink.Config{
		Dir:          cfg.Logs.Dir,
		MaxBackups:   cfg.Logs.MaxBackups,
		CompressDays: cfg.Logs.CompressDays,
	}
}

// listLogs adapts the sink's file listing for the dashboard viewer.
// A disabled sink (nil) is not an error: the empty list renders the
// honest "logging is off" state in the viewer.
func (a *App) listLogs() ([]dashboard.LogFileState, error) {
	if a.logSink == nil {
		return nil, nil
	}
	files, err := a.logSink.List()
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.LogFileState, len(files))
	for i, f := range files {
		out[i] = dashboard.LogFileState{Name: f.Name, Size: f.Size, ModAt: f.Modified}
	}
	return out, nil
}

// tailLogs adapts the sink's tail for the dashboard viewer.
func (a *App) tailLogs(name string) (*dashboard.LogTailState, error) {
	if a.logSink == nil {
		return nil, fmt.Errorf("logging is disabled")
	}
	lines, err := a.logSink.Tail(name, 400)
	if err != nil {
		return nil, err
	}
	return &dashboard.LogTailState{Name: name, Lines: lines}, nil
}
