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
package sections

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// .
// .
// .
var validSlots = map[string]bool{
	"rail": true, "main-tabs": true, "panel": true, "dock": true, "overlay": true,
}

// .
// .
const maxDeclBytes = 16 * 1024

// .
// .
// .
// .
// .
// .
// .
// .
type Decl struct {
	// .
	// .
	// .
	ID string `json:"id"`
	// .
	Title string `json:"title"`
	// .
	Slot string `json:"slot"`
	// .
	// .
	// .
	// .
	Commands []string `json:"commands"`
	// .
	// .
	Topics []string `json:"topics"`
	// .
	// .
	Entry string `json:"entry"`
}

// .

// .
// .
// .
var ErrNotAsset = errors.New("sections: package kind is not asset")

// .
// .
// .
// .
var ErrAssetNotSection = errors.New("sections: asset package carries no section.json")

// .
type DeclError struct {
	Field  string
	Reason string
}

func (e *DeclError) Error() string {
	return fmt.Sprintf("sections: section.json %s: %s", e.Field, e.Reason)
}

// .
// .
// .
// .
type TamperError struct {
	Member string
	Want   string
	Got    string
}

func (e *TamperError) Error() string {
	if e.Got == "" {
		return fmt.Sprintf("sections: member %s could not be re-read for digest verification (want %s) — refusing to serve unverified bytes", e.Member, e.Want)
	}
	return fmt.Sprintf("sections: member %s digest %s does not match the verified %s — the package changed between verification and extraction; refusing", e.Member, e.Got, e.Want)
}

// .
// .
// .
func token(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case (c == '.' || c == '_' || c == '-') && i > 0:
		default:
			return false
		}
	}
	return true
}

// .
// .
// .
// .
func cleanEntryPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// .
// .
// .
func ParseDecl(raw []byte) (*Decl, error) {
	if len(raw) > maxDeclBytes {
		return nil, &DeclError{Field: "size", Reason: fmt.Sprintf("%d bytes exceeds the %d-byte declaration ceiling", len(raw), maxDeclBytes)}
	}
	var d Decl
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, &DeclError{Field: "json", Reason: err.Error()}
	}
	if !token(d.ID) {
		return nil, &DeclError{Field: "id", Reason: fmt.Sprintf("%q is not a valid section id (lowercase [a-z0-9._-], first byte alphanumeric, ≤64 bytes)", d.ID)}
	}
	if !validSlots[d.Slot] {
		return nil, &DeclError{Field: "slot", Reason: fmt.Sprintf("%q is not a known slot (rail, main-tabs, panel, dock, overlay) — refusing, never rendering nowhere", d.Slot)}
	}
	if d.Title == "" {
		d.Title = d.ID
	}
	if d.Entry == "" {
		d.Entry = "index.html"
	}
	if !cleanEntryPath(d.Entry) || !strings.HasSuffix(d.Entry, ".html") {
		return nil, &DeclError{Field: "entry", Reason: fmt.Sprintf("%q must be a clean install-root-relative .html path", d.Entry)}
	}
	seen := map[string]bool{}
	for _, c := range d.Commands {
		if !token(c) {
			return nil, &DeclError{Field: "commands", Reason: fmt.Sprintf("%q is not a valid command name", c)}
		}
		if seen["c:"+c] {
			return nil, &DeclError{Field: "commands", Reason: fmt.Sprintf("%q declared twice", c)}
		}
		seen["c:"+c] = true
	}
	for _, tp := range d.Topics {
		if !token(tp) {
			return nil, &DeclError{Field: "topics", Reason: fmt.Sprintf("%q is not a valid topic name", tp)}
		}
		if seen["t:"+tp] {
			return nil, &DeclError{Field: "topics", Reason: fmt.Sprintf("%q declared twice", tp)}
		}
		seen["t:"+tp] = true
	}
	return &d, nil
}

// .
// .
// .
// .
// .
type Section struct {
	Decl Decl
	// .
	Dir string
	// .
	// .
	// .
	Dev bool
	// .
	// .
	PackageID string
}

// .
// .
func (s *Section) Close() error {
	if s.Dev || s.Dir == "" {
		return nil
	}
	return removeCache(s.Dir)
}

// .
// .
// .
// .
type Registry struct {
	mu   sync.RWMutex
	byID map[string]*Section
	// .
	// .
	// .
	// .
	safeSource func() (string, bool)
}

// .
func NewRegistry() *Registry {
	return &Registry{byID: map[string]*Section{}}
}

// .
func (r *Registry) SetSafeSource(fn func() (string, bool)) {
	r.mu.Lock()
	r.safeSource = fn
	r.mu.Unlock()
}

// .
func (r *Registry) Safe() (string, bool) {
	r.mu.RLock()
	fn := r.safeSource
	r.mu.RUnlock()
	if fn == nil {
		return "", false
	}
	return fn()
}

// .
// .
// .
func (r *Registry) Register(sec *Section) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.byID[sec.Decl.ID]; dup {
		return fmt.Errorf("sections: id %q is already registered — refusing the duplicate (deactivate one)", sec.Decl.ID)
	}
	r.byID[sec.Decl.ID] = sec
	return nil
}

// .
func (r *Registry) Remove(id string) {
	r.mu.Lock()
	delete(r.byID, id)
	r.mu.Unlock()
}

// .
func (r *Registry) Get(id string) (*Section, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sec, ok := r.byID[id]
	return sec, ok
}

// .
// .
func (r *Registry) List() []*Section {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Section, 0, len(r.byID))
	for _, sec := range r.byID {
		out = append(out, sec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Decl.ID < out[j].Decl.ID })
	return out
}
