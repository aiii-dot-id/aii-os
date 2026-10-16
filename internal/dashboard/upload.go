package dashboard

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

type UploadReply struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
	Image bool   `json:"image"`
}

const uploadNameTries = 99

const uploadNameMax = 200

func uploadName(raw string) (string, string) {
	name := strings.TrimSpace(raw)
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSpace(name)
	switch {
	case name == "" || name == "." || name == "..":
		return "", "the file has no name"
	case len(name) > uploadNameMax:
		return "", fmt.Sprintf("the file's name is longer than %d bytes", uploadNameMax)
	case strings.ContainsAny(name, `<>:"|?*`):
		return "", "the file's name holds a character a folder on Windows does not take (one of < > : \" | ? *)"
	}
	if why := unreadable(name); why != "" {
		return "", "the file's name holds " + why
	}
	return strings.NewReplacer("[", "(", "]", ")").Replace(name), ""
}

func unreadable(text string) string {
	at := 0
	for _, c := range text {
		at++
		what := ""
		switch {
		case unicode.IsControl(c):
			what = "a control character"
		case c == '\u2028' || c == '\u2029':
			what = "a line separator"
		case c >= '\u202a' && c <= '\u202e', c >= '\u2066' && c <= '\u2069':
			what = "a character that turns the direction of the text after it"
		default:
			continue
		}
		return fmt.Sprintf("%s (U+%04X, character %d)", what, c, at)
	}
	return ""
}

func untellable(path string) string {
	switch {
	case !utf8.ValidString(path):
		return "bytes that are not text"
	case strings.ContainsAny(path, "[]"):
		return "a square bracket"
	}
	return unreadable(path)
}

func drawnText(text string) string {
	return strings.Map(func(c rune) rune {
		if unreadable(string(c)) != "" {
			return -1
		}
		return c
	}, strings.ToValidUTF8(text, "\uFFFD"))
}

func (p FilePolicy) typesSentence() string {
	var exts []string
	for ext := range drawnTypes {
		exts = append(exts, ext)
	}
	for _, t := range p.Types {
		exts = append(exts, strings.ToLower(t))
	}
	sort.Strings(exts)
	return strings.Join(exts, " ")
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	refuse := func(code int, text string) { http.Error(w, text, code) }
	if !s.tokenAuthorized(r) {
		refuse(http.StatusUnauthorized, "this dashboard needs its access token")
		return
	}
	if o := r.Header.Get("Origin"); o != "" && !sameSiteAs(o, r.Host) {
		refuse(http.StatusForbidden, "this request came from another site")
		return
	}
	if !privateConnection(r) {
		refuse(http.StatusForbidden, "this connection is not private, so the file was not taken: open the dashboard on the machine itself, or serve it over HTTPS")
		return
	}
	h := s.currentHandler()
	if h == nil || h.AdmitFile == nil || h.UploadDir == nil {
		refuse(http.StatusNotFound, "files are taken once the identity is running")
		return
	}
	name, why := uploadName(r.URL.Query().Get("name"))
	if why != "" {
		refuse(http.StatusBadRequest, why)
		return
	}
	policy := s.filePolicy()
	ext := strings.ToLower(filepath.Ext(name))
	if !policy.taken(ext) {
		kind := ext
		if kind == "" {
			kind = "a file with no extension"
		}
		refuse(http.StatusUnsupportedMediaType, fmt.Sprintf("%s is not a kind of file taken here. Taken: %s. Put the file in the identity's folder yourself, or add its extension to dashboard.files.types in config.json", kind, policy.typesSentence()))
		return
	}

	head := make([]byte, 512)
	n, err := io.ReadFull(r.Body, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		refuse(http.StatusBadRequest, "the file could not be read: "+err.Error())
		return
	}
	head = head[:n]
	if n == 0 {
		refuse(http.StatusBadRequest, "the file is empty")
		return
	}
	want, image := ImageTypes[ext]
	if image && http.DetectContentType(head) != want {
		refuse(http.StatusUnsupportedMediaType, fmt.Sprintf("%s is named as %s and its content is not that: the file was not taken", name, want))
		return
	}

	dir := h.UploadDir()
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	for try := 1; try <= uploadNameTries; try++ {
		final := name
		if try > 1 {
			final = fmt.Sprintf("%s (%d)%s", stem, try, filepath.Ext(name))
		}
		told := filepath.Join(dir, final)
		root, rel, ok := h.AdmitFile(told)
		if !ok {
			refuse(http.StatusForbidden, fmt.Sprintf("the identity's folder does not take a file at %s: the name or the place is one the host keeps", told))
			return
		}

		told = h.toldPath(root, rel)
		if what := untellable(told); what != "" {
			refuse(http.StatusConflict, fmt.Sprintf("the folder this file would be kept in, %s, holds %s in its path, and a file there cannot be told to the identity as it is written: nothing was kept. Rename the folder, or attach the file with no project in focus",
				drawnText(filepath.Dir(told)), what))
			return
		}
		written, code, why := writeUpload(root, rel, io.MultiReader(bytes.NewReader(head), r.Body), policy.MaxBytes)
		if code == http.StatusConflict {
			continue
		}
		if why != "" {
			refuse(code, why)
			return
		}

		logsink.Info("dashboard.decision", "the chat's \"+\" took %s (%d bytes)", told, written)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(UploadReply{Path: told, Name: final, Bytes: written, Image: image})
		return
	}
	refuse(http.StatusConflict, fmt.Sprintf("%s holds %d files named like %s already: rename the file and attach it again", dir, uploadNameTries, name))
}

func writeUpload(root, rel string, body io.Reader, max int64) (int64, int, string) {
	rz, err := os.OpenRoot(root)
	if err != nil {
		return 0, http.StatusInternalServerError, "the identity's folder could not be opened: " + err.Error()
	}
	defer rz.Close()
	if dir := filepath.Dir(rel); dir != "." {
		if err := rz.MkdirAll(dir, 0o700); err != nil {
			return 0, http.StatusInternalServerError, "the uploads folder could not be made: " + err.Error()
		}
	}
	f, err := rz.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return 0, http.StatusConflict, ""
	}
	if err != nil {
		return 0, http.StatusInternalServerError, "the file could not be created: " + err.Error()
	}
	n, copyErr := io.Copy(f, io.LimitReader(body, max+1))
	closeErr := f.Close()
	switch {
	case n > max:
		_ = rz.Remove(rel)
		return 0, http.StatusRequestEntityTooLarge, fmt.Sprintf("the file is larger than this dashboard takes (%d bytes, dashboard.files.max_bytes): nothing was kept", max)
	case copyErr != nil || closeErr != nil:
		_ = rz.Remove(rel)
		return 0, http.StatusBadRequest, fmt.Sprintf("the file did not arrive whole, so nothing was kept: %v", errors.Join(copyErr, closeErr))
	}
	return n, 0, ""
}
