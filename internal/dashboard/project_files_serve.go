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
// .
// .
// .

package dashboard

import (
	"fmt"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

// .
const projectFileMaxBytes = 5 << 20

// .
// .
// .
// .
// .
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

// .
func (s *Server) handleProjectFile(w http.ResponseWriter, r *http.Request) {
	s.serveProjectFile(w, r, r.PathValue("id"), r.PathValue("path"))
}

// .
// .
func (s *Server) serveProjectFile(w http.ResponseWriter, r *http.Request, id, rel string) {
	// .
	// .
	// .
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
	rz, err := os.OpenRoot(root)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer rz.Close()
	f, err := rz.Open(path.Clean(rel))
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
	ctype, ok := projectServableTypes[strings.ToLower(path.Ext(rel))]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if st.Size() > projectFileMaxBytes {
		http.Error(w, fmt.Sprintf("file exceeds serving cap (%d bytes > %d)", st.Size(), projectFileMaxBytes), http.StatusRequestEntityTooLarge)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "inline")
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
	// .
	// .
	// .
	// .
	// .
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	http.ServeContent(w, r, path.Base(rel), time.Time{}, f)
}
