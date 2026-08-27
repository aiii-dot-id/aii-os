// Package sections is the UI-section lane of the R66 frame plan (UP2,
// docs/UI_FRAME.md §3): a verified kind=asset package that carries a
// `section.json` in its install-root activates as a SECTION — static
// files the dashboard serves into a sandboxed iframe, never code the
// host runs. This package owns the section declaration schema, the
// verified extraction (the loadVerifiedMember discipline applied to
// EVERY extracted file), and the registry the dashboard reads.
//
// It is deliberately NOT part of internal/pluginhost: the plugin
// harness is an execution wall (wazero, broker, supervisor) and a
// section executes nothing host-side — its whole lifecycle is
// verify → extract → register → serve. Coupling the two would drag the
// execution stack into a static-file lane and force the dashboard to
// import the harness to read a list. The shape here mirrors
// internal/facility instead: the app assembles the registry once at
// startup, the dashboard consumes it through narrow read methods.
//
// The security posture (UI_FRAME.md §1): sections add NO authority
// surface. The frame's allowlists are UX; the server gates the WS
// commands land on are the wall — a malicious section is exactly as
// powerful as forged localhost traffic, an adversary the H2 gates
// already refuse.
package sections

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Slots are the five v0 mount points (UI_FRAME.md §5). Unknown slot
// names REFUSE at declaration parse — a typo'd slot must fail the
// activation loudly, never render nowhere silently.
var validSlots = map[string]bool{
	"rail": true, "main-tabs": true, "panel": true, "dock": true, "overlay": true,
}

// maxDeclBytes bounds section.json — a declaration is a dozen lines,
// and an unbounded parse of package-supplied JSON is a bomb lane.
const maxDeclBytes = 16 * 1024

// Decl is the section declaration — OUR schema (UI_FRAME.md §4 as
// corrected by UP2): the allowlist lives in the section's own
// section.json inside the verified install-root, NOT in the C
// manifest's operator_projection (that field is a per-OPERATION
// confirmation projection in the C schema, and the asset manifest
// grammar forbids it outright — internal/packagefmt manifest.go,
// validateAssetManifest). Unknown FIELDS are tolerated (schema-tolerant
// rendering, §5); unknown slot names are not.
type Decl struct {
	// ID names the section: the /sections/<id>/ URL segment and the
	// layout-file key. Lowercase token charset so it is URL- and
	// filename-sane everywhere the five platforms serve it.
	ID string `json:"id"`
	// Title is the operator-visible name (defaults to ID).
	Title string `json:"title"`
	// Slot is one of the five v0 slots.
	Slot string `json:"slot"`
	// Commands is the section's declared command allowlist. The frame
	// refuses act() calls outside it — and outside the frame's own
	// wired-command registry (double allowlist, UX layer); the server
	// gates remain the wall.
	Commands []string `json:"commands"`
	// Topics is the declared subscription allowlist: the frame relays
	// only these projections to the section's port.
	Topics []string `json:"topics"`
	// Entry is the iframe document, install-root-relative
	// (default index.html).
	Entry string `json:"entry"`
}

// Typed refusals — every failure names its requirement (R39 pattern).

// ErrNotAsset marks a verified package whose kind is not asset: not a
// refusal, a lane signal — the caller falls through to the plugin
// activation lane.
var ErrNotAsset = errors.New("sections: package kind is not asset")

// ErrAssetNotSection marks a verified asset package with no
// section.json in its install-root: not a section, and NOT an error —
// assets have other futures (prompt packs, skills). Callers log and
// skip.
var ErrAssetNotSection = errors.New("sections: asset package carries no section.json")

// DeclError is a section.json that fails the declaration schema.
type DeclError struct {
	Field  string
	Reason string
}

func (e *DeclError) Error() string {
	return fmt.Sprintf("sections: section.json %s: %s", e.Field, e.Reason)
}

// TamperError reports the verified-bytes-are-served-bytes invariant
// failing: an extracted member does not hash to the digest the
// verified Result recorded (the loadVerifiedMember discipline,
// applied here to every file a section serves).
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

// token validates the lowercase id/command/topic charset. Bounded and
// URL-sane; the first byte must be alphanumeric so an id can never
// begin with a separator.
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

// cleanEntryPath validates an install-root-relative file path for the
// declaration: forward slashes only, no empty/dot/dot-dot segments, no
// leading slash. The extractor fences the archive's own paths the same
// way — declarations and members meet the same bar.
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

// ParseDecl parses and validates one section.json. Unknown fields are
// tolerated (a section carrying future fields renders today — §5);
// everything the frame ACTS on is validated strictly.
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

// Section is one activated section: a validated declaration and the
// directory its files are served from. For a verified section Dir is a
// host-owned extraction cache (every file digest-checked on the way
// in); for a dev section Dir is the operator-named source directory,
// unverified BY DESIGN and marked so the frame can say it loudly.
type Section struct {
	Decl Decl
	// Dir is the serving root.
	Dir string
	// Dev marks a dev-serve section (config plugins.dev_section):
	// unverified, banner-marked in the frame, cache-disabled, and
	// refused entirely under SAFE.
	Dev bool
	// PackageID is the verified manifest id ("" for dev sections) —
	// telemetry for logs, never authority.
	PackageID string
}

// Close removes a verified section's extraction cache. Dev sections
// serve from the operator's own directory — never removed.
func (s *Section) Close() error {
	if s.Dev || s.Dir == "" {
		return nil
	}
	return removeCache(s.Dir)
}

// Registry is the section read-seam the dashboard consumes (the
// facility-set pattern: assembled by the app at startup, read through
// narrow methods afterwards). Registration is guarded — activation and
// deactivation may race dashboard reads.
type Registry struct {
	mu   sync.RWMutex
	byID map[string]*Section
	// safeSource answers the mode lattice (wired to App.SafeMode, the
	// same source the tool registry's SAFE suspension reads — reuse,
	// don't invent). Under SAFE the registry lists nothing and the
	// dashboard refuses dev-section files.
	safeSource func() (string, bool)
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{byID: map[string]*Section{}}
}

// SetSafeSource wires the mode lattice in. nil = never SAFE (tests).
func (r *Registry) SetSafeSource(fn func() (string, bool)) {
	r.mu.Lock()
	r.safeSource = fn
	r.mu.Unlock()
}

// Safe reports the current SAFE state (reason, entered).
func (r *Registry) Safe() (string, bool) {
	r.mu.RLock()
	fn := r.safeSource
	r.mu.RUnlock()
	if fn == nil {
		return "", false
	}
	return fn()
}

// Register admits one section. A duplicate id is a refusal, never a
// silent replacement — two packages claiming one id is an operator
// problem the operator must see.
func (r *Registry) Register(sec *Section) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.byID[sec.Decl.ID]; dup {
		return fmt.Errorf("sections: id %q is already registered — refusing the duplicate (deactivate one)", sec.Decl.ID)
	}
	r.byID[sec.Decl.ID] = sec
	return nil
}

// Remove deregisters one section (idempotent). The caller owns Close.
func (r *Registry) Remove(id string) {
	r.mu.Lock()
	delete(r.byID, id)
	r.mu.Unlock()
}

// Get resolves one registered section by id.
func (r *Registry) Get(id string) (*Section, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sec, ok := r.byID[id]
	return sec, ok
}

// List snapshots the registered sections, sorted by id (stable
// operator output, the facility-set precedent).
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
