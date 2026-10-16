package app

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

type DashboardRecordConfig struct {
	PageRows int `json:"page_rows,omitempty"`

	FirstViewBytes int `json:"first_view_bytes,omitempty"`
}

type DashboardSentRequestsConfig struct {
	Kept *int `json:"kept,omitempty"`

	MaxBytes int `json:"max_bytes,omitempty"`
}

func (c DashboardSentRequestsConfig) limits() (kept, maxBytes int) {
	kept = dashboard.DefaultSentKept
	if c.Kept != nil {
		kept = min(max(*c.Kept, dashboard.MinSentKept), dashboard.MaxSentKept)
	}
	maxBytes = c.MaxBytes
	switch {
	case maxBytes <= 0:
		maxBytes = dashboard.DefaultSentMaxBytes
	case maxBytes < dashboard.MinSentMaxBytes:
		maxBytes = dashboard.MinSentMaxBytes
	case maxBytes > dashboard.MaxSentMaxBytes:
		maxBytes = dashboard.MaxSentMaxBytes
	}
	return kept, maxBytes
}

func (a *App) sentLimits() (kept, maxBytes int) {
	if a.cfg == nil {
		return DashboardSentRequestsConfig{}.limits()
	}
	return a.configSnapshot().Dashboard.SentRequests.limits()
}

type recordSource struct{ a *App }

var errNoRecordStore = errors.New("the database is not open")

func (r recordSource) store() (*store.Store, error) {
	if v := r.a.databaseView.Load(); v != nil && v.store != nil {
		return v.store, nil
	}
	return nil, errNoRecordStore
}

func (r recordSource) RecordPage(_ context.Context, before uint64, typ, words string, limit, headBytes int) ([]dashboard.RecordRow, error) {
	st, err := r.store()
	if err != nil {
		return nil, err
	}
	rows, err := st.LedgerMirrorPage(before, typ, words, limit, headBytes)
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.RecordRow, len(rows))
	for i, row := range rows {
		out[i] = dashboard.RecordRow{Seq: row.Seq, Time: row.Timestamp, Type: row.Type, Ring: row.Ring, Bytes: row.PayloadBytes, Head: row.Head}
	}
	return out, nil
}

func (r recordSource) RecordPayload(_ context.Context, seq uint64, offset, length int64) ([]byte, int64, error) {
	st, err := r.store()
	if err != nil {
		return nil, 0, err
	}
	return st.LedgerMirrorPayload(seq, offset, length)
}

func (r recordSource) RecordTypes(context.Context) ([]string, error) {
	st, err := r.store()
	if err != nil {
		return nil, err
	}
	return st.LedgerMirrorTypes()
}

func (a *App) recordSettings() dashboard.RecordSettings {
	cfg := a.configSnapshot()
	dir := identityHomeFromConfig(cfg.SourcePath)
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return dashboard.RecordSettings{PageRows: cfg.Dashboard.Record.PageRows, FirstViewBytes: cfg.Dashboard.Record.FirstViewBytes, Dir: dir}
}

func (a *App) wireRecordHooks(h *dashboard.WSHandler) {
	h.Record = recordSource{a}
	h.RecordSettings = a.recordSettings
	h.Sent = a.sentRequests
}
