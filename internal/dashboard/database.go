package dashboard

import (
	"io"
	"net/http"
	"strconv"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// .
// .
type DatabaseState struct {
	Preferred string   `json:"preferred"`
	Active    string   `json:"active"`
	Notice    string   `json:"notice,omitempty"`
	Recovery  []string `json:"recovery,omitempty"`
	CanExport bool     `json:"can_export"`
}

func (s *Server) handleDatabaseExport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if !s.tokenAuthorized(r) {
		http.Error(w, "dashboard authentication required", http.StatusUnauthorized)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && !sameSiteAs(origin, r.Host) {
		http.Error(w, "request came from another site", http.StatusForbidden)
		return
	}
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
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		http.Error(w, "request came from another site", http.StatusForbidden)
		return
	}
	h := s.currentHandler()
	if h == nil || h.DatabaseExport == nil {
		http.Error(w, "database export is unavailable", http.StatusServiceUnavailable)
		return
	}
	file, size, err := h.DatabaseExport(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", `attachment; filename="aii-export.db"`)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	if n, err := io.Copy(w, file); err != nil || n != size {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		logsink.Warn("store.error", "database export download interrupted: %v", err)
	}
}
