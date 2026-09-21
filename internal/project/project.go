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
package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// .
// .
type Manifest struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	State       string                 `json:"state"`
	CreatedBy   string                 `json:"created_by"`
	CreatedAt   string                 `json:"created_at"`
	UpdatedAt   string                 `json:"updated_at"`
	Attributes  map[string]interface{} `json:"attributes,omitempty"`
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	Contract Contract `json:"contract,omitempty"`
	// .
	// .
	Parent string `json:"parent,omitempty"`
	// .
	// .
	// .
	Focus string `json:"focus,omitempty"`
	// .
	// .
	// .
	// .
	Observations []AcceptanceObservation `json:"observations,omitempty"`
}

// .
// .
// .
// .
// .
// .
// .
// .
type Contract struct {
	// .
	Outcome string `json:"outcome,omitempty"`
	// .
	// .
	Acceptance []string `json:"acceptance,omitempty"`
	// .
	Constraints []string `json:"constraints,omitempty"`
}

// .
// .
func (c Contract) IsZero() bool {
	return c.Outcome == "" && len(c.Acceptance) == 0 && len(c.Constraints) == 0
}

// .
// .
// .
var contractKeys = map[string]string{
	"outcome":     "contract.outcome",
	"acceptance":  "contract.acceptance",
	"constraints": "contract.constraints",
	"parent":      "parent",
}

// .
// .
// .
// .
// .
func warnAuthorityInAttributes(id string, attributes map[string]interface{}) {
	for k := range attributes {
		if field, bad := contractKeys[strings.ToLower(strings.TrimSpace(k))]; bad {
			logsink.Warn("project.refusal", "%s: attribute %q duplicates the typed field %s — the typed field is the authority; clear the attribute to remove the ambiguity", id, k, field)
		}
	}
}

// .
func rejectAuthorityInAttributes(attributes map[string]interface{}) error {
	for k := range attributes {
		if field, bad := contractKeys[strings.ToLower(strings.TrimSpace(k))]; bad {
			return fmt.Errorf("attribute %q is authority-bearing and has a typed field: set %s instead. Attributes are for extensions, so a reader never has to guess which of two spellings is the real one", k, field)
		}
	}
	return nil
}

// .
type Project struct {
	ID  string
	Dir string
	Manifest
}

// .
// .
// .
type Manager struct {
	mu   sync.Mutex
	root string
}

const manifestName = "project.json"

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// .
func NewManager(root string) *Manager { return &Manager{root: root} }

// .
func (m *Manager) Root() string { return m.root }

// .
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

// .
// .
// .
// .
// .
// .
// .
// .
// .
func (m *Manager) Create(name, description, createdBy string, parent *string, contract *Contract, attributes map[string]interface{}) (*Project, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("a project needs a name")
	}
	if createdBy != "operator" && createdBy != "identity" {
		return nil, fmt.Errorf("created_by must be operator or identity")
	}
	if err := validateAcceptance(contract); err != nil {
		return nil, err
	}
	// .
	// .
	// .
	// .
	// .
	if err := rejectAuthorityInAttributes(attributes); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// .
	// .
	// .
	// .
	// .
	parentID := ""
	if parent != nil {
		parentID = strings.TrimSpace(*parent)
	}
	if parentID != "" {
		if _, err := m.loadLocked(parentID); err != nil {
			return nil, fmt.Errorf("parent project %q does not exist: %w", parentID, err)
		}
	}
	if err := ensureDurableDir(m.root, atomicfile.SyncDir); err != nil {
		return nil, fmt.Errorf("projects root: %w", err)
	}
	dir, err := claimDir(m.root, slugify(name), func(p string) error { return os.Mkdir(p, 0o755) })
	if err != nil {
		return nil, err
	}
	// .
	// .
	// .
	// .
	// .
	if err := atomicfile.SyncDir(m.root); err != nil {
		return nil, errors.Join(fmt.Errorf("publish project directory in %s: %w", m.root, err), m.undoCreate(dir))
	}
	now := time.Now().UTC().Format(time.RFC3339)
	p := &Project{
		ID: filepath.Base(dir), Dir: dir,
		Manifest: Manifest{
			Name: strings.TrimSpace(name), Description: strings.TrimSpace(description),
			State: "open", CreatedBy: createdBy, CreatedAt: now, UpdatedAt: now,
			Attributes: attributes,
			Parent:     parentID,
		},
	}
	if contract != nil {
		p.Manifest.Contract = *contract
	}
	if err := writeManifest(dir, &p.Manifest); err != nil {
		// .
		// .
		// .
		return nil, errors.Join(err, m.undoCreate(dir))
	}
	return p, nil
}

// .
const maxSlugSuffix = 1000

// .
// .
// .
// .
// .
// .
// .
// .
// .
func ensureDurableDir(root string, sync func(string) error) error {
	if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
		return err
	}
	err := os.Mkdir(root, 0o755)
	switch {
	case err == nil:
		// .
		return sync(filepath.Dir(root))
	case errors.Is(err, os.ErrExist):
		return nil
	default:
		return err
	}
}

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
func claimDir(root, slug string, mkdir func(string) error) (string, error) {
	dir := filepath.Join(root, slug)
	for i := 2; ; i++ {
		err := mkdir(dir)
		if err == nil {
			return dir, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("claim project directory under %s: %w", root, err)
		}
		if i > maxSlugSuffix {
			return "", fmt.Errorf("project slug %q: %d directories already carry it", slug, maxSlugSuffix)
		}
		dir = filepath.Join(root, fmt.Sprintf("%s-%d", slug, i))
	}
}

// .
// .
// .
// .
// .
// .
// .
// .
func (m *Manager) undoCreate(dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("undo %s: %w", dir, err)
	}
	if err := atomicfile.SyncDir(m.root); err != nil {
		return fmt.Errorf("sync %s after undo: %w", m.root, err)
	}
	return nil
}

// .
func (m *Manager) Load(id string) (*Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loadLocked(id)
}

func (m *Manager) loadLocked(id string) (*Project, error) {
	id = filepath.Base(strings.TrimSpace(id))
	if id == "" || id == "." || id == ".." {
		return nil, fmt.Errorf("no such project %q", id)
	}
	dir := filepath.Join(m.root, id)
	b, err := os.ReadFile(filepath.Join(dir, manifestName))
	if err != nil {
		return nil, fmt.Errorf("no such project %q", id)
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	var mf Manifest
	if err := json.Unmarshal(b, &mf); err != nil {
		return nil, fmt.Errorf("project %q manifest unreadable: %w", id, err)
	}
	// .
	// .
	// .
	// .
	// .
	if err := mf.validate(); err != nil {
		return nil, fmt.Errorf("project %q manifest invalid: %w", id, err)
	}
	warnAuthorityInAttributes(id, mf.Attributes)
	return &Project{ID: id, Dir: dir, Manifest: mf}, nil
}

// .
// .
// .
func (mf Manifest) validate() error {
	if strings.TrimSpace(mf.Name) == "" {
		return fmt.Errorf("a project needs a name")
	}
	if mf.State != "open" && mf.State != "closed" && mf.State != "archived" {
		return fmt.Errorf("state must be open, closed or archived (got %q)", mf.State)
	}
	if mf.CreatedBy != "" && mf.CreatedBy != "operator" && mf.CreatedBy != "identity" {
		return fmt.Errorf("created_by must be operator or identity (got %q)", mf.CreatedBy)
	}
	return nil
}

// .
// .
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
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p, err := m.loadLocked(e.Name())
		if err != nil {
			// .
			// .
			// .
			// .
			logsink.Warn("project.refusal", "skipping %q — %v; repair its %s or remove the directory",
				e.Name(), err, manifestName)
			continue
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].State != out[j].State {
			return stateRank(out[i].State) < stateRank(out[j].State)
		}
		return out[i].UpdatedAt > out[j].UpdatedAt
	})
	return out, nil
}

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
func (m *Manager) Update(id, name, description, focus string, parent *string, contract *Contract, attributes map[string]interface{}) (*Project, error) {
	return m.ApplyPatch(id,
		strPtrIfSet(name),
		strPtrIfSet(description),
		strPtrIfSet(focus),
		parent,
		contract,
		attributes)
}

// .
// .
// .
func strPtrIfSet(s string) *string {
	t := strings.TrimSpace(s)
	if t == "" {
		return nil
	}
	p := new(string)
	*p = t
	return p
}

// .
// .
// .
// .
// .
// .
func (m *Manager) ApplyPatch(id string, name, description, focus, parent *string, contract *Contract, attributes map[string]interface{}) (*Project, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := rejectAuthorityInAttributes(attributes); err != nil {
		return nil, err
	}
	p, err := m.loadLocked(id)
	if err != nil {
		return nil, err
	}
	if parent != nil {
		t := strings.TrimSpace(*parent)
		if err := m.checkParentLocked(id, t); err != nil {
			return nil, err
		}
		p.Parent = t
	}
	if contract != nil {
		if err := validateAcceptance(contract); err != nil {
			return nil, err
		}
		p.Contract = *contract
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

// .
// .
// .
// .
func (m *Manager) checkParentLocked(id, parent string) error {
	if parent == "" {
		return nil
	}
	if parent == id {
		return fmt.Errorf("a project cannot be its own parent")
	}
	if _, err := m.loadLocked(parent); err != nil {
		return fmt.Errorf("parent project %q does not exist: %w", parent, err)
	}
	// .
	// .
	// .
	seen := map[string]bool{id: true}
	for at := parent; at != ""; {
		if seen[at] {
			return fmt.Errorf("making %q the parent of %q would create a cycle in the project hierarchy", parent, id)
		}
		seen[at] = true
		up, err := m.loadLocked(at)
		if err != nil {
			return nil
		}
		at = strings.TrimSpace(up.Parent)
	}
	return nil
}

// .
// .
func (m *Manager) SetState(id, state string) (*Project, error) {
	if state != "open" && state != "closed" && state != "archived" {
		return nil, fmt.Errorf("state must be open, closed or archived")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.loadLocked(id)
	if err != nil {
		return nil, err
	}
	if p.State == state {
		// .
		// .
		// .
		// .
		// .
		return nil, fmt.Errorf("project is already %s", state)
	}
	// .
	// .
	// .
	if state == "closed" && p.State == "archived" {
		return nil, fmt.Errorf("an archived project is unarchived first")
	}
	// .
	// .
	// .
	// .
	if state == "closed" {
		if prog := DeriveContractProgress(p.Contract, p.Manifest.Observations); !prog.ClosureAllowed {
			return nil, fmt.Errorf("project close refused: %s", prog.ClosureReason)
		}
	}
	p.State = state
	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writeManifest(p.Dir, &p.Manifest); err != nil {
		return nil, err
	}
	return p, nil
}

// .
// .
func stateRank(state string) int {
	switch state {
	case "open":
		return 0
	case "closed":
		return 1
	}
	return 2
}

// .
// .
// .
// .
// .
func (m *Manager) Delete(id string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.loadLocked(id)
	if err != nil {
		return "", err
	}
	if p.State != "archived" {
		return "", fmt.Errorf("project %q is %s — archive it first; deleting is the second step", p.Name, p.State)
	}
	trash := filepath.Join(m.root, ".trash")
	if err := os.MkdirAll(trash, 0o700); err != nil {
		return "", err
	}
	dest := filepath.Join(trash, p.ID+"-"+time.Now().UTC().Format("20060102-150405"))
	if err := os.Rename(p.Dir, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// .
// .
// .
func (m *Manager) Progress(id string) (ContractProgress, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.loadLocked(id)
	if err != nil {
		return ContractProgress{}, err
	}
	return DeriveContractProgress(p.Contract, p.Manifest.Observations), nil
}

// .
// .
// .
// .
// .
func (m *Manager) RecordObservation(id string, item int, class, ref, note, by string) (*Project, error) {
	if err := checkEvidenceClass(class); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.recordLocked(id, item, strings.TrimSpace(class), ref, note, by)
}

// .
// .
// .
func (m *Manager) WaiveItem(id string, item int, by, reason string) (*Project, error) {
	if strings.TrimSpace(reason) == "" {
		return nil, fmt.Errorf("a waiver needs a reason — waivers are explicit and attributable")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.recordLocked(id, item, ObservationWaived, "", reason, by)
}

func (m *Manager) recordLocked(id string, item int, class, ref, note, by string) (*Project, error) {
	p, err := m.loadLocked(id)
	if err != nil {
		return nil, err
	}
	if item < 0 || item >= len(p.Contract.Acceptance) {
		return nil, fmt.Errorf("acceptance item %d out of range — the project has %d", item, len(p.Contract.Acceptance))
	}
	if isVerifiedClass(class) && strings.TrimSpace(ref) == "" {
		return nil, fmt.Errorf("a %s observation needs a ref — the durable pointer to the check that was read back", class)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	p.Manifest.Observations = append(p.Manifest.Observations, AcceptanceObservation{
		Item: item, ItemText: p.Contract.Acceptance[item], Class: class,
		Ref: strings.TrimSpace(ref), Note: strings.TrimSpace(note), By: strings.TrimSpace(by), At: now,
	})
	p.UpdatedAt = now
	if err := writeManifest(p.Dir, &p.Manifest); err != nil {
		return nil, err
	}
	return p, nil
}

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
	defer os.Remove(tmp)
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
	if err := os.Chmod(tmp, 0o644); err != nil {
		return err
	}
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
	published, err := atomicfile.Replace(tmp, filepath.Join(dir, manifestName))
	if err != nil {
		if published {
			return fmt.Errorf("manifest published but not durable in %s: %w", dir, err)
		}
		return err
	}
	return nil
}
