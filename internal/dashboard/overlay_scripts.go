package dashboard

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

type ApprovedScript struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type OverlayScript struct {
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	Approved bool   `json:"approved"`
}

func isOverlayScript(p string) bool { return path.Ext(p) == ".js" }

func (s *Server) noServiceWorker(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Service-Worker") == "" {
			next.ServeHTTP(w, r)
			return
		}
		if s.serviceWorkers.own(refusalLinesAMinute, func(n uint64) {
			logsink.Warn("dashboard.refusal", "%d more request(s) for a service worker's script answered in the last minute with one that removes itself", n)
		}) {
			logsink.Warn("dashboard.refusal", "%s was asked for as a service worker's script: answered with one that removes itself, as this dashboard installs none", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = io.WriteString(w, workerEviction)
	})
}

const workerEviction = "self.addEventListener('install', () => self.skipWaiting());\n" +
	"self.addEventListener('activate', e => e.waitUntil(\n" +
	"  (self.caches ? caches.keys().then(keys => Promise.all(keys.map(k => caches.delete(k)))) : Promise.resolve())\n" +
	"    .catch(() => {})\n" +
	"    .then(() => self.registration.unregister())));\n"

func (s *Server) SetApprovedScripts(list []ApprovedScript) {
	approved := make(map[string]string, len(list))
	for _, a := range list {
		approved[strings.TrimPrefix(a.Path, "/")] = strings.ToLower(a.SHA256)
	}
	s.secMu.Lock()
	s.approvedScripts = approved

	s.overlayReported = nil
	s.secMu.Unlock()
}

func (s *Server) scriptApproved(rel string, data []byte) bool {
	sum := sha256.Sum256(data)
	s.secMu.RLock()
	want, ok := s.approvedScripts[rel]
	s.secMu.RUnlock()
	return ok && want == hex.EncodeToString(sum[:])
}

func heldOutcome(data []byte) string {
	sum := sha256.Sum256(data)
	return "held: a script awaiting your approval (Settings → Dashboard → Scripts), sha256 " + hex.EncodeToString(sum[:])[:12]
}

func (s *Server) overlayScripts() ([]OverlayScript, error) {
	s.secMu.RLock()
	dir := s.overlayDir
	s.secMu.RUnlock()
	out := []OverlayScript{}
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
	err = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !isOverlayScript(p) {
			return nil
		}
		data, ok := readOverlayFile(root, p)
		if !ok {
			return nil
		}
		sum := sha256.Sum256(data)
		out = append(out, OverlayScript{Path: p, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), Approved: s.scriptApproved(p, data)})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, err
}

func readOverlayFile(root *os.Root, rel string) ([]byte, bool) {
	f, err := root.Open(rel)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > maxOverlayBytes {
		return nil, false
	}
	data, err := readAssetBounded(f, maxOverlayBytes)
	return data, err == nil
}

const approvalNeedsToken = "approving a script requires this dashboard to ask for its access token, so that only a browser signed in with it can approve one, and it does not now. Turn that on in Settings, under Dashboard, with Ask for an access token"

func (s *Server) canApprove(r *http.Request) string {
	if !s.accessRequired() {
		return approvalNeedsToken
	}
	if !s.tokenAuthorized(r) {
		return "this browser is not signed in with this dashboard's access token"
	}
	return ""
}

func (s *Server) handleOverlayScripts(w http.ResponseWriter, r *http.Request) {
	if !s.tokenAuthorized(r) {
		sayRefused(w, http.StatusUnauthorized, "this dashboard needs its access token")
		return
	}
	list, err := s.overlayScripts()
	if err != nil {
		sayRefused(w, http.StatusInternalServerError, "the interface folder could not be read: "+err.Error())
		return
	}
	why := s.canApprove(r)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(w).Encode(map[string]any{"scripts": list, "can_approve": why == "", "why_not": why})
}

func (s *Server) handleOverlayScriptFile(w http.ResponseWriter, r *http.Request) {
	if !s.tokenAuthorized(r) {
		sayRefused(w, http.StatusUnauthorized, "this dashboard needs its access token")
		return
	}
	rel := r.URL.Query().Get("path")
	data, err := s.overlayScriptBytes(rel)
	if err != nil {
		sayRefused(w, http.StatusNotFound, err.Error())
		return
	}
	sum := sha256.Sum256(data)
	w.Header().Set("Content-Type", mime.FormatMediaType("text/plain", map[string]string{"charset": "utf-8"}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("X-Script-SHA256", hex.EncodeToString(sum[:]))
	_, _ = w.Write(data)
}

var errNotAnOverlayScript = errors.New("is not a script in the identity's interface folder")

func (s *Server) overlayScriptBytes(rel string) ([]byte, error) {
	rel = strings.TrimPrefix(rel, "/")
	if !fs.ValidPath(rel) || rel == "." || !isOverlayScript(rel) {
		return nil, fmt.Errorf("%q %w", rel, errNotAnOverlayScript)
	}
	s.secMu.RLock()
	dir := s.overlayDir
	s.secMu.RUnlock()
	if dir == "" {
		return nil, fmt.Errorf("%q %w", rel, errNotAnOverlayScript)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("%q %w", rel, errNotAnOverlayScript)
	}
	defer root.Close()
	data, ok := readOverlayFile(root, rel)
	if !ok {
		return nil, fmt.Errorf("%q %w", rel, errNotAnOverlayScript)
	}
	return data, nil
}

const maxApproveBody = 8 << 10

func (s *Server) handleOverlayScriptApprove(w http.ResponseWriter, r *http.Request) {
	if why := s.canApprove(r); why != "" {
		status := http.StatusForbidden
		if why != approvalNeedsToken {
			status = http.StatusUnauthorized
		}
		sayRefused(w, status, why)
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
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		sayRefused(w, http.StatusUnsupportedMediaType, `an approval is sent as JSON: {"path": …, "sha256": …}`)
		return
	}
	var req ApprovedScript
	body, err := io.ReadAll(io.LimitReader(r.Body, maxApproveBody+1))
	if err != nil || len(body) > maxApproveBody || json.Unmarshal(body, &req) != nil || req.Path == "" || req.SHA256 == "" {
		sayRefused(w, http.StatusBadRequest, `say which script, and the digest you were shown: {"path": …, "sha256": …}`)
		return
	}
	req.Path = strings.TrimPrefix(req.Path, "/")
	req.SHA256 = strings.ToLower(req.SHA256)
	data, err := s.overlayScriptBytes(req.Path)
	if err != nil {
		sayRefused(w, http.StatusNotFound, err.Error()+"; nothing was approved")
		return
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != req.SHA256 {
		sayRefused(w, http.StatusConflict, req.Path+" has changed since it was shown to you; look at it again before you approve it. Nothing was approved")
		return
	}
	h := s.currentHandler()
	if h == nil || h.ApproveOverlayScript == nil {
		sayRefused(w, http.StatusNotFound, "scripts cannot be approved here")
		return
	}
	if err := h.ApproveOverlayScript(req); err != nil {
		sayRefused(w, http.StatusConflict, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(w).Encode(map[string]any{"approved": req})
}
