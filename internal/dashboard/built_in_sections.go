package dashboard

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/sections"
)

type builtInTree struct {
	own, frame fs.FS
	shared     []string
}

func (t builtInTree) overlaid(name string) bool {
	if f, err := t.own.Open(name); err == nil {
		f.Close()
		return false
	}
	for _, p := range t.shared {
		if p == name {
			return true
		}
	}
	return false
}

func (t builtInTree) Open(name string) (fs.File, error) {
	if f, err := t.own.Open(name); err == nil {
		return f, nil
	}
	for _, p := range t.shared {
		if p == name {
			return t.frame.Open(name)
		}
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

//go:embed built_in_digests/status.txt
var builtInStatusDigests string

//go:embed built_in_digests/about.txt
var builtInAboutDigests string

func (t builtInTree) files() ([]string, error) {
	paths := append([]string(nil), t.shared...)
	err := fs.WalkDir(t.own, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			paths = append(paths, p)
		}
		return err
	})
	sort.Strings(paths)
	return paths, err
}

func (t builtInTree) digests() (string, error) {
	paths, err := t.files()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, p := range paths {
		f, err := t.Open(p)
		if err != nil {
			return "", err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(h.Sum(nil)), p)
	}
	return b.String(), nil
}

func (t builtInTree) verified(id, want string) bool {
	got, err := t.digests()
	if err == nil && got == want {
		return true
	}
	if err == nil {
		err = fmt.Errorf("a file differs from its digest")
	}
	logsink.Warn("dashboard.error", "built-in section %q was not registered: %v", id, err)
	return false
}

var builtInStatusFiles = []string{
	"views/settings/status.js", "views/settings/pass-now.js", "views/settings/status.css", "util.js", "shell/page-note.js",
	"components/button/button.js", "components/button/button.css",
	"components/os-note/os-note.js", "components/os-note/os-note.css",
	"strings/status.js", "strings/components.js", "theme.css", "tokens/tokens.css", "layout.css", "custom.css",
	"built-in/page-keys.js", "built-in/keep.js", "shell/keep.js",
}

var builtInAboutFiles = []string{
	"views/identity.js", "views/memory.js", "views/identity/who.js", "views/identity/between.js",
	"views/identity/experiences.js", "views/identity/beliefs.js", "views/identity/beliefs-show.js",
	"views/identity/relationships.js", "views/identity/trail.js", "views/identity/when.js", "views/identity/record-read.js",
	"views/identity/who.css", "views/identity/experiences.css", "views/identity/beliefs.css",
	"components/belief-row/belief-row.js", "components/belief-row/belief-row.css",
	"components/why-trail/why-trail.js", "components/why-trail/why-trail.css",
	"components/in-words/in-words.js", "components/in-words/in-words.css",
	"components/filter/filter.css", "components/button/button.css", "components/chip/chip.css",
	"util.js", "strings/identity.js", "strings/home.js", "strings/chat.js", "strings/components.js", "strings/beliefs-show.js",
	"theme.css", "tokens/tokens.css", "layout.css", "custom.css", "built-in/page-keys.js", "built-in/keep.js", "shell/keep.js",
}

func builtInTreeOf(id string, shared []string) (builtInTree, error) {
	frame, err := fs.Sub(staticFS, "static")
	if err != nil {
		return builtInTree{}, err
	}
	own, err := fs.Sub(staticFS, "static/built-in/"+id)
	if err != nil {
		return builtInTree{}, err
	}
	return builtInTree{own: own, frame: frame, shared: shared}, nil
}

func builtInStatusTree() (builtInTree, error) { return builtInTreeOf("status", builtInStatusFiles) }
func builtInAboutTree() (builtInTree, error)  { return builtInTreeOf("about", builtInAboutFiles) }

func BuiltInSections() []*sections.Section {
	var out []*sections.Section
	if t, err := builtInStatusTree(); err == nil && t.verified("status", builtInStatusDigests) {
		out = append(out, &sections.Section{
			Decl: sections.Decl{ID: "status", Title: "Status", Slot: "view", Entry: "index.html",
				Topics:   []string{"connection", "route", "turn", "status", "continuity", "config", "providers", "tools", "work", "asks", "overlays", "projects"},
				Commands: []string{"pass.now", "spend.read", "page.key", "place.keep", "place.kept"}},
			BuiltIn: true, Views: []string{"status"}, FS: t,
		})
	}
	if t, err := builtInAboutTree(); err == nil && t.verified("about", builtInAboutDigests) {
		out = append(out, &sections.Section{
			Decl: sections.Decl{ID: "about", Title: "About", Slot: "view", Entry: "index.html",
				Topics:   []string{"connection", "route", "status", "continuity", "identity", "work"},
				Commands: []string{"record.read", "record.payload", "recall.search", "belief.trail", "meaning.source", "page.go", "clipboard.write", "visit", "reply.quote", "page.key", "place.keep", "place.kept"}},
			BuiltIn: true, Views: []string{"identity", "memory", "between", "experiences"}, FS: t,
		})
	}
	return out
}

const sectionCapabilityMark = "~"

func (s *Server) sectionCapability(id string) string {
	verifier := ""
	if p := s.auth.Load(); p != nil {
		verifier = p.hash
	}
	mac := hmac.New(sha256.New, s.sectionKey)
	fmt.Fprintf(mac, "built-in section\x00%s\x00%s", id, verifier)
	return sectionCapabilityMark + hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) sectionCapabilityHolds(id, got string) bool {
	return hmac.Equal([]byte(got), []byte(s.sectionCapability(id)))
}

func pathToLog(p string) string {
	if rest, ok := strings.CutPrefix(p, "/built-in-sections/"); ok {
		if id, after, ok := strings.Cut(rest, "/"); ok && strings.HasPrefix(after, sectionCapabilityMark) {
			_, file, _ := strings.Cut(after, "/")
			return "/built-in-sections/" + id + "/" + sectionCapabilityMark + "/" + file
		}
	}
	return p
}

func newSectionKey() []byte {
	key := make([]byte, 32)
	rand.Read(key)
	return key
}
