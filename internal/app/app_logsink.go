package app

import (
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"path/filepath"
)

func (a *App) installLogSink() error {
	if a.logSink != nil {
		return nil
	}
	sink, err := logsink.Install(a.sinkConfig())
	if err != nil {

		return err
	}
	a.logSink = sink

	cfg := a.configSnapshot()
	home := identityHomeFromConfig(cfg.SourcePath)

	logsink.SetCaptureDir(filepath.Join(home, LogControlDirName, logsink.CaptureDirName))
	applyLogLevels(cfg, LogControlPathIn(home))
	applyLogTaps(cfg, LogControlPathIn(home))
	return nil
}

func (a *App) sinkConfig() logsink.Config {
	cfg := a.configSnapshot()
	return logsink.Config{
		Dir:          cfg.Logs.Dir,
		MaxBackups:   cfg.Logs.MaxBackups,
		CompressDays: cfg.Logs.CompressDays,
		MaxDays:      cfg.Logs.MaxDays,
		MaxBytes:     cfg.Logs.MaxBytes,

		MaxTotalBytes: cfg.Logs.MaxTotalBytes,
	}
}

func validLogMaxTotalBytes(total, maxBytes int64) error {
	roll := maxBytes
	if roll <= 0 {
		roll = logsink.DefaultMaxBytes
	}
	if total < 0 || (total > 0 && total < 2*roll) {
		return fmt.Errorf("logs.max_total_bytes must be zero (take the default, logs.max_bytes times max_backups+1) or at least twice logs.max_bytes (%d)", 2*roll)
	}
	return nil
}

func validLogMaxBytes(n int64) error {
	if n != 0 && (n < logsink.MinMaxBytes || n > logsink.MaxMaxBytes) {
		return fmt.Errorf("logs.max_bytes must be zero (take the default, %d) or between %d and %d", int64(logsink.DefaultMaxBytes), int64(logsink.MinMaxBytes), int64(logsink.MaxMaxBytes))
	}
	return nil
}

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
