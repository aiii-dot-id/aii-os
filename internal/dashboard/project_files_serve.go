package dashboard

import (
	"fmt"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

const projectFileMaxBytes = 5 << 20

var projectServableTypes = map[string]string{
	".txt":  "text/plain; charset=utf-8",
	".md":   "text/plain; charset=utf-8",
	".csv":  "text/plain; charset=utf-8",
	".json": "application/json; charset=utf-8",
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".svg":  "image/svg+xml",
}

func (s *Server) handleProjectFile(w http.ResponseWriter, r *http.Request) {
	s.serveProjectFile(w, r, r.PathValue("id"), r.PathValue("path"))
}

func (s *Server) serveProjectFile(w http.ResponseWriter, r *http.Request, id, rel string) {

	if !s.tokenAuthorized(r) {
		http.NotFound(w, r)
		return
	}
	h := s.currentHandler()
	if h == nil || h.GetProjectRoot == nil {
		http.NotFound(w, r)
		return
	}
	root, ok := h.GetProjectRoot(id)
	if !ok || root == "" {
		http.NotFound(w, r)
		return
	}
	s.serveFileIn(w, r, root, path.Clean(rel), projectServableTypes, projectFileMaxBytes, false)
}

func fromThisSite(r *http.Request) bool {
	site := r.Header.Get("Sec-Fetch-Site")
	return site == "" || site == "same-origin" || site == "none"
}

func (s *Server) serveFileIn(w http.ResponseWriter, r *http.Request, root, rel string, types map[string]string, maxBytes int64, download bool) {

	if !fromThisSite(r) {
		logsink.Info("dashboard.refusal", "a file of the identity was asked for from another site's page (Sec-Fetch-Site: %s): refused, whatever the path", r.Header.Get("Sec-Fetch-Site"))
		http.Error(w, "this dashboard does not hand its files to a page of another site, or to a link pressed on one: open the dashboard and find the file there", http.StatusForbidden)
		return
	}
	rz, err := os.OpenRoot(root)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer rz.Close()

	if st, err := rz.Stat(rel); err != nil || !st.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	f, err := rz.Open(rel)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	name := filepath.Base(filepath.FromSlash(rel))
	ctype, ok := types[strings.ToLower(filepath.Ext(name))]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if st.Size() > maxBytes {
		http.Error(w, fmt.Sprintf("file exceeds serving cap (%d bytes > %d)", st.Size(), maxBytes), http.StatusRequestEntityTooLarge)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	if download {

		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	} else {
		w.Header().Set("Content-Disposition", "inline")
	}

	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	http.ServeContent(w, r, name, time.Time{}, f)
}
