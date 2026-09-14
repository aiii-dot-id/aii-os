package app

import (
	"fmt"
	"log"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// .
// .
func (a *App) installLogSink() {
	if a.logSink != nil {
		return
	}
	sink, err := logsink.Install(a.sinkConfig())
	if err != nil {
		// .
		// .
		log.Fatalf("Startup failed: %v", err)
	}
	a.logSink = sink
}

// .
// .
// .
func (a *App) sinkConfig() logsink.Config {
	cfg := a.configSnapshot()
	return logsink.Config{
		Dir:          cfg.Logs.Dir,
		MaxBackups:   cfg.Logs.MaxBackups,
		CompressDays: cfg.Logs.CompressDays,
	}
}

// .
// .
// .
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

// .
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
