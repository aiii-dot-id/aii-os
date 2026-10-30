package dashboard

import (
	"io"
	"net/http"
	"strconv"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

type DatabaseState struct {
	Preferred       string   `json:"preferred"`
	Active          string   `json:"active"`
	Notice          string   `json:"notice,omitempty"`
	Recovery        []string `json:"recovery,omitempty"`
	CanExport       bool     `json:"can_export"`
	OptimizeLevels  bool     `json:"optimize_levels"`
	LearnDictionary bool     `json:"learn_dictionary"`
	DictionaryBytes *int     `json:"dictionary_bytes,omitempty"`
}

func (s *Server) handleDatabaseExport(w http.ResponseWriter, r *http.Request) {
	if !s.downloadAdmitted(w, r) {
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
	serveDownload(w, file, size, "application/vnd.sqlite3", "aii-export.db", "database export")
}

func (s *Server) downloadAdmitted(w http.ResponseWriter, r *http.Request) bool {
	if !s.tokenAuthorized(r) {
		http.Error(w, "dashboard authentication required", http.StatusUnauthorized)
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" && !sameSiteAs(origin, r.Host) {
		http.Error(w, "request came from another site", http.StatusForbidden)
		return false
	}

	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		http.Error(w, "request came from another site", http.StatusForbidden)
		return false
	}
	return true
}

func serveDownload(w http.ResponseWriter, file io.Reader, size int64, contentType, name, what string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	if n, err := io.Copy(w, file); err != nil || n != size {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		logsink.Warn("dashboard.error", "%s download interrupted: %v", what, err)
	}
}
