// Package project manages the identity's projects — durable collections
// that live OUTSIDE AII OS (R62, James 2026-08-18): "just like a person
// has many projects and can switch between them with a focus on one at
// a time, so does an AI identity manage projects."
//
// A project is a directory under the identity's Ring 5 sandbox, carrying
// a small manifest (project.json). The ledger never records projects —
// the identity remembers them the way they remember anything, through
// notes and experiences. RING4 is FOCUS: the currently selected
// project's metadata seeds the working state; switching replaces it
// (with a transition marker), and the previous project's detail leaves
// RING4 entirely. The manifest's attributes are an open envelope — a
// project "can consist of many things"; new concerns become attributes
// (or files in the directory), never manifest schema.
package project

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Manifest is project.json — the durable metadata. Everything else a
// project consists of is simply files in its directory.
type Manifest struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	State       string                 `json:"state"`      // open | closed
	CreatedBy   string                 `json:"created_by"` // operator | identity
	CreatedAt   string                 `json:"created_at"`
	UpdatedAt   string                 `json:"updated_at"`
	Attributes  map[string]interface{} `json:"attributes,omitempty"`
	// Focus is the metadata that re-seeds RING4 working state when the
	// project is selected — "what was I doing here". A fuller working-
	// state snapshot is TBD by ruling; only this seed exists today.
	Focus string `json:"focus,omitempty"`
}

// Project is a manifest plus its directory identity.
type Project struct {
	ID  string // the directory name (slug) — stable, filesystem-safe
	Dir string // absolute path
	Manifest
}

// Manager owns the projects root (a directory under the Ring 5
// sandbox). All operations are manifest+directory operations — there
// is no database and no ledger surface.
type Manager struct {
	mu   sync.Mutex
	root string
}

const manifestName = "project.json"

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// NewManager creates the manager; the root is created on first use.
func NewManager(root string) *Manager { return &Manager{root: root} }

// Root returns the projects root directory.
func (m *Manager) Root() string { return m.root }

// slugify derives a filesystem-safe directory name from a project name.
func slugify(name string) string {
	s := slugRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "project"
	}
	if len(s) > 48 {
		s = s[:48]
	}
	return s
}

// Create makes a new project directory with its manifest. Name
// collisions get a numeric suffix — creation never fails on a name.
func (m *Manager) Create(name, description, createdBy string, attributes map[string]interface{}) (*Project, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("a project needs a name")
	}
	if createdBy != "operator" && createdBy != "identity" {
		return nil, fmt.Errorf("created_by must be operator or identity")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := os.MkdirAll(m.root, 0o755); err != nil {
		return nil, fmt.Errorf("projects root: %w", err)
	}
	slug := slugify(name)
	dir := filepath.Join(m.root, slug)
	for i := 2; ; i++ {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			break
		}
		dir = filepath.Join(m.root, fmt.Sprintf("%s-%d", slug, i))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	p := &Project{
		ID: filepath.Base(dir), Dir: dir,
		Manifest: Manifest{
			Name: strings.TrimSpace(name), Description: strings.TrimSpace(description),
			State: "open", CreatedBy: createdBy, CreatedAt: now, UpdatedAt: now,
			Attributes: attributes,
		},
	}
	if err := writeManifest(dir, &p.Manifest); err != nil {
		// A directory with no manifest is not a project, and leaving one
		// behind means the next Create picks a "-2" name around a thing
		// that was never born.
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return p, nil
}

// Load reads one project by ID (directory name).
func (m *Manager) Load(id string) (*Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loadLocked(id)
}

func (m *Manager) loadLocked(id string) (*Project, error) {
	id = filepath.Base(strings.TrimSpace(id)) // no traversal — IDs are directory names
	if id == "" || id == "." || id == ".." {
		return nil, fmt.Errorf("no such project %q", id)
	}
	dir := filepath.Join(m.root, id)
	b, err := os.ReadFile(filepath.Join(dir, manifestName))
	if err != nil {
		return nil, fmt.Errorf("no such project %q", id)
	}
	var mf Manifest
	if err := json.Unmarshal(b, &mf); err != nil {
		return nil, fmt.Errorf("project %q manifest unreadable: %w", id, err)
	}
	return &Project{ID: id, Dir: dir, Manifest: mf}, nil
}

// List scans the root for project directories. Open projects first,
// most recently updated first within each state.
func (m *Manager) List() ([]*Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entries, err := os.ReadDir(m.root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Project
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p, err := m.loadLocked(e.Name())
		if err != nil {
			// A directory with no readable manifest is not a project —
			// but it is not nothing either, and a bare continue told
			// nobody. R18: the omission is declared, with the route out
			// (repair the manifest, or remove the directory).
			log.Printf("projects: skipping %q — %v; repair its %s or remove the directory",
				e.Name(), err, manifestName)
			continue
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].State != out[j].State {
			return out[i].State == "open"
		}
		return out[i].UpdatedAt > out[j].UpdatedAt
	})
	return out, nil
}

// Update applies non-empty fields to the manifest. Attributes, when
// non-nil, replace the envelope whole (merge semantics are complexity;
// the caller reads then writes). Focus updates ride the same path.
// Update applies a stringly update where an empty string means "no
// change". Kept for the identity-facing tool, whose caller types plain
// strings and cannot express "clear this field" — a resident asking to
// update a project without naming a field is asking to change nothing.
// The dashboard path uses ApplyPatch, where nil means "not sent" and a
// pointer to the empty string means "clear it".
func (m *Manager) Update(id, name, description, focus string, attributes map[string]interface{}) (*Project, error) {
	return m.ApplyPatch(id,
		strPtrIfSet(name),
		strPtrIfSet(description),
		strPtrIfSet(focus),
		attributes)
}

// strPtrIfSet returns nil for the empty string, else a pointer to the
// trimmed value. It is the stringly→patch bridge: only fields the
// caller actually set are carried into the patch.
func strPtrIfSet(s string) *string {
	t := strings.TrimSpace(s)
	if t == "" {
		return nil
	}
	p := new(string)
	*p = t
	return p
}

// ApplyPatch updates a project with PATCH semantics: a nil pointer
// leaves the field untouched; a non-nil pointer (including one to the
// empty string) writes it. Name is deliberately NOT clearable — an
// unnamed project would put the empty-state vocabulary sin back into
// every surface that lists projects, and a project with no name
// cannot be told apart from a broken manifest.
func (m *Manager) ApplyPatch(id string, name, description, focus *string, attributes map[string]interface{}) (*Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.loadLocked(id)
	if err != nil {
		return nil, err
	}
	if name != nil {
		t := strings.TrimSpace(*name)
		if t == "" {
			return nil, fmt.Errorf("project name cannot be empty")
		}
		p.Name = t
	}
	if description != nil {
		p.Description = strings.TrimSpace(*description)
	}
	if focus != nil {
		p.Focus = strings.TrimSpace(*focus)
	}
	if attributes != nil {
		p.Attributes = attributes
	}
	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writeManifest(p.Dir, &p.Manifest); err != nil {
		return nil, err
	}
	return p, nil
}

// SetState opens or closes a project. Closing never deletes anything —
// the collection is durable; only the state changes.
func (m *Manager) SetState(id, state string) (*Project, error) {
	if state != "open" && state != "closed" {
		return nil, fmt.Errorf("state must be open or closed")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.loadLocked(id)
	if err != nil {
		return nil, err
	}
	if p.State == state {
		// A transition that is not a transition must not pretend to be
		// one: a repeated "close" rewrote the manifest and — through the
		// app adapter — appended a duplicate "closed" lineage fact each
		// time (D74). Refused here, lineage-on-close is once per real
		// open→closed transition by construction, for every caller.
		return nil, fmt.Errorf("project is already %s", state)
	}
	p.State = state
	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writeManifest(p.Dir, &p.Manifest); err != nil {
		return nil, err
	}
	return p, nil
}

// writeManifest replaces project.json atomically AND durably.
//
// It claimed atomicity and delivered only ordering. Three gaps, all
// real: a FIXED temp name, which two runtimes sharing a projects root
// would write over each other; NO FSYNC, so the rename could land while
// the bytes were still in page cache and a power loss left an empty
// manifest where a valid one used to be; and nothing to make the rename
// itself durable.
//
// CreateTemp gives the unique name. Sync before the rename is the one
// that matters: it makes the file real before anything points at it.
//
// The directory sync is best-effort ON PURPOSE. Windows refuses to open
// a directory for sync at all, and unlike the file sync its failure is
// not a correctness hazard: losing the rename leaves the PREVIOUS valid
// manifest in place, which is a consistent state. Losing file content
// under a completed rename would not be.
func writeManifest(dir string, mf *Manifest) error {
	b, err := json.MarshalIndent(mf, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, manifestName+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op once the rename succeeds; cleanup on every failure
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync %s: %w", filepath.Base(tmp), err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil { // CreateTemp makes it 0600
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, manifestName)); err != nil {
		return err
	}
	if d, derr := os.Open(dir); derr == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}
