package dashboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"sort"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/version"
)

type OverlayReplacement struct {
	Path  string    `json:"path"`
	Size  int64     `json:"size"`
	MTime time.Time `json:"mtime"`
}

type OverlaySetAside struct {
	Path string `json:"path"`
	To   string `json:"to,omitempty"`
}

const maxSetAsideBody = 64 << 10

func replacesShipped(rel string) bool {
	if rel == "custom.css" || rel == "custom.js" || !fs.ValidPath(rel) {
		return false
	}
	if _, ok := sectionServableTypes[path.Ext(rel)]; !ok {
		return false
	}
	st, err := fs.Stat(staticFS, "static/"+rel)
	return err == nil && st.Mode().IsRegular()
}

func shippedFrameFiles() []string {
	var out []string
	_ = fs.WalkDir(staticFS, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if rel := p[len("static/"):]; replacesShipped(rel) {
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func (s *Server) overlayReplacements() ([]OverlayReplacement, error) {
	s.secMu.RLock()
	dir := s.overlayDir
	s.secMu.RUnlock()
	out := []OverlayReplacement{}
	if dir == "" {
		return out, nil
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	defer root.Close()
	for _, rel := range shippedFrameFiles() {
		st, ok := servedReplacement(root, rel)
		if !ok {
			continue
		}

		if isOverlayScript(rel) {
			if data, read := readOverlayFile(root, rel); !read || !s.scriptApproved(rel, data) {
				continue
			}
		}
		out = append(out, OverlayReplacement{Path: rel, Size: st.Size(), MTime: st.ModTime().UTC()})
	}
	return out, nil
}

func servedReplacement(root *os.Root, rel string) (os.FileInfo, bool) {
	st, err := root.Stat(rel)
	if err != nil || !st.Mode().IsRegular() || st.Size() > maxOverlayBytes {
		return nil, false
	}
	return st, true
}

func setAsideSuffix() string {
	v := version.Authored()
	if !version.Valid(v) {
		v = "unversioned"
	}
	return ".set-aside-" + v
}

var errNotAReplacement = errors.New("does not name a shipped dashboard file that an overlay file replaces")

func (s *Server) setAside(rel string) (string, error) {
	if !replacesShipped(rel) {
		return "", fmt.Errorf("%q %w", rel, errNotAReplacement)
	}
	s.secMu.RLock()
	dir := s.overlayDir
	s.secMu.RUnlock()
	if dir == "" {
		return "", nil
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	defer root.Close()
	if _, ok := servedReplacement(root, rel); !ok {
		return "", nil
	}
	base := rel + setAsideSuffix()
	to := base
	for n := 2; ; n++ {
		if _, err := root.Lstat(to); errors.Is(err, fs.ErrNotExist) {
			break
		} else if err != nil {
			return "", err
		}
		if n > 100 {
			return "", fmt.Errorf("%s: a hundred set-aside copies are already there", rel)
		}
		to = fmt.Sprintf("%s-%d", base, n)
	}
	if err := root.Rename(rel, to); err != nil {
		return "", err
	}
	logsink.Info("dashboard.decision", "set aside %s as %s: it replaced a shipped dashboard file", rel, to)
	return to, nil
}

func (s *Server) handleOverlayReplacements(w http.ResponseWriter, r *http.Request) {
	if !s.tokenAuthorized(r) {
		sayRefused(w, http.StatusUnauthorized, "this dashboard needs its access token")
		return
	}
	files, err := s.overlayReplacements()
	if err != nil {
		sayRefused(w, http.StatusInternalServerError, "the dashboard's customizations could not be read: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(w).Encode(map[string]any{"files": files})
}

func (s *Server) handleOverlaySetAside(w http.ResponseWriter, r *http.Request) {
	if !s.tokenAuthorized(r) {
		sayRefused(w, http.StatusUnauthorized, "this dashboard needs its access token")
		return
	}
	if !sameSiteAs(r.Header.Get("Origin"), r.Host) {
		sayRefused(w, http.StatusForbidden, "this request came from another site")
		return
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		sayRefused(w, http.StatusForbidden, "this request came from another site")
		return
	}
	var req struct {
		Paths []string `json:"paths"`
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxSetAsideBody+1))
	if err != nil || len(body) > maxSetAsideBody || json.Unmarshal(body, &req) != nil || len(req.Paths) == 0 {
		sayRefused(w, http.StatusBadRequest, `say which files to set aside: {"paths": [...]}`)
		return
	}
	for _, p := range req.Paths {
		if !replacesShipped(p) {
			sayRefused(w, http.StatusBadRequest, fmt.Sprintf("%q %v; nothing was set aside", p, errNotAReplacement))
			return
		}
	}
	done := make([]OverlaySetAside, 0, len(req.Paths))
	for _, p := range req.Paths {
		to, err := s.setAside(p)
		if err != nil {
			logsink.Warn("dashboard.error", "set aside %s: %v", p, err)
			sayRefused(w, http.StatusInternalServerError, fmt.Sprintf("%s could not be set aside: %v", p, err))
			return
		}
		done = append(done, OverlaySetAside{Path: p, To: to})
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(w).Encode(map[string]any{"set_aside": done})
}
