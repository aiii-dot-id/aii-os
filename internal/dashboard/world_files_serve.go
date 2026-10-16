package dashboard

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type FilePolicy struct {
	MaxBytes int64
	Types    []string

	MaxPerMessage int
}

const DefaultFilesPerMessage = 10

const DefaultFileMaxBytes = 25 << 20

var ImageTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
}

var drawnTypes = func() map[string]string {
	types := map[string]string{".svg": "image/svg+xml"}
	for ext, ctype := range ImageTypes {
		types[ext] = ctype
	}
	return types
}()

var DefaultFileTypes = []string{
	".pdf", ".docx", ".xlsx", ".pptx", ".odt", ".ods", ".odp", ".rtf",
	".txt", ".md", ".csv", ".tsv", ".json", ".xml", ".yaml", ".yml", ".log",
}

func (s *Server) filePolicy() FilePolicy {
	var p FilePolicy
	if h := s.currentHandler(); h != nil && h.Files != nil {
		p = h.Files()
	}
	if p.MaxBytes <= 0 {
		p.MaxBytes = DefaultFileMaxBytes
	}
	if len(p.Types) == 0 {
		p.Types = DefaultFileTypes
	}
	if p.MaxPerMessage <= 0 {
		p.MaxPerMessage = DefaultFilesPerMessage
	}
	return p
}

func (s *Server) admittedAttachments(h *WSHandler, paths []string) (told []string, why string) {
	if h.AdmitFile == nil {
		return nil, "files are taken once the identity is running: the message was not sent"
	}
	if max := s.filePolicy().MaxPerMessage; len(paths) > max {
		return nil, fmt.Sprintf("one message carries at most %d files (dashboard.files.max_per_message) and this one has %d: the message was not sent", max, len(paths))
	}
	seen := map[string]bool{}
	for _, p := range paths {
		root, rel, ok := h.AdmitFile(p)
		if !ok || !regularIn(root, rel) {
			return nil, fmt.Sprintf("%s is not a file in the identity's folder any more: the message was not sent. Attach it again", shownName(p))
		}
		path := h.toldPath(root, rel)
		if what := untellable(path); what != "" {
			return nil, fmt.Sprintf("the path %s holds %s, so it cannot be told to the identity as it is written: the message was not sent. Rename what holds it, the file or its folder, and attach the file again", drawnText(path), what)
		}
		if !seen[path] {
			seen[path] = true
			told = append(told, path)
		}
	}
	return told, ""
}

func shownName(path string) string {
	name := path
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	if name = drawnText(name); len(name) > uploadNameMax {
		name = strings.ToValidUTF8(name[:uploadNameMax], "") + "…"
	}
	if name == "" {
		return "a file"
	}
	return name
}

func regularIn(root, rel string) bool {
	rz, err := os.OpenRoot(root)
	if err != nil {
		return false
	}
	defer rz.Close()
	st, err := rz.Stat(rel)
	return err == nil && st.Mode().IsRegular()
}

func (h *WSHandler) toldPath(root, rel string) string {
	if h.ToldPath != nil {
		return h.ToldPath(root, rel)
	}
	return rel
}

func (p FilePolicy) taken(ext string) bool {
	ext = strings.ToLower(ext)
	if _, drawn := drawnTypes[ext]; drawn {
		return true
	}
	for _, t := range p.Types {
		if strings.ToLower(t) == ext {
			return true
		}
	}
	return false
}

func (p FilePolicy) downloadTypes() map[string]string {
	types := make(map[string]string, len(p.Types)+len(drawnTypes))
	for ext := range drawnTypes {
		types[ext] = "application/octet-stream"
	}
	for _, t := range p.Types {
		types[strings.ToLower(t)] = "application/octet-stream"
	}
	return types
}

func (s *Server) handleWorldFile(w http.ResponseWriter, r *http.Request) {
	if !s.tokenAuthorized(r) {
		http.NotFound(w, r)
		return
	}
	h := s.currentHandler()
	if h == nil || h.AdmitFile == nil {
		http.NotFound(w, r)
		return
	}
	root, rel, ok := h.AdmitFile(r.URL.Query().Get("p"))
	if !ok || root == "" || rel == "" {
		http.NotFound(w, r)
		return
	}
	policy := s.filePolicy()
	if r.URL.Query().Get("download") == "1" {

		if ext := strings.ToLower(filepath.Ext(rel)); !policy.taken(ext) && fromThisSite(r) && regularIn(root, rel) {
			kind := ext
			if kind == "" {
				kind = "a file with no extension"
			}
			http.Error(w, fmt.Sprintf("%s is not a kind of file handed over from the chat: it stays in the identity's folder at %s. Handed over: %s (dashboard.files.types in config.json adds a kind)",
				kind, h.toldPath(root, rel), policy.typesSentence()), http.StatusUnsupportedMediaType)
			return
		}
		s.serveFileIn(w, r, root, rel, policy.downloadTypes(), policy.MaxBytes, true)
		return
	}
	s.serveFileIn(w, r, root, rel, drawnTypes, policy.MaxBytes, false)
}
