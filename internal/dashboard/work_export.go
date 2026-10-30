package dashboard

import (
	"errors"
	"net/http"
	"time"
)

const WorkExportRoute = "/work/export"

const (
	RefusedHeader     = "Aii-Refused"
	WorkExportNotHere = "not-this-machine"
)

const workExportNotHereWords = "the work record is read only by a program on this machine (aii work export): this request came through a relay or from another device"

const PageKeyHeader = pageKeyHeader

func (s *Server) serveWorkExport(w http.ResponseWriter, r *http.Request) {

	if r.Context().Value(carriedKey{}) != nil || !remoteIsLoopback(r.RemoteAddr) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set(RefusedHeader, WorkExportNotHere)
		http.Error(w, workExportNotHereWords, http.StatusForbidden)
		return
	}
	h, done, ok := s.operatorRead(w, r)
	if !ok {
		return
	}
	defer done()
	if h == nil || h.WorkExport == nil {
		http.Error(w, "the work record is not readable now", http.StatusServiceUnavailable)
		return
	}
	q := r.URL.Query()
	var since time.Time
	if raw := q.Get("since"); raw != "" {
		var err error
		if since, err = time.Parse(time.RFC3339, raw); err != nil {
			http.Error(w, "since is not an RFC 3339 time: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	out := &firstByte{w: w}
	if err := h.WorkExport(r.Context(), since, q.Get("session"), q.Get("project"), out); err != nil && !out.wrote {
		http.Error(w, "the work record could not be read: "+err.Error(), statusOfExport(err))
	}
}

func statusOfExport(err error) int {
	var unknown interface{ UnknownSession() string }
	if errors.As(err, &unknown) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

type firstByte struct {
	w     http.ResponseWriter
	wrote bool
}

func (f *firstByte) Write(p []byte) (int, error) {
	if !f.wrote {
		f.w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
		f.wrote = true
	}
	return f.w.Write(p)
}
