package app

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/project"
)

// .
// .
// .
// .
// .
// .
type projectsAdapter struct{ a *App }

func info(p *project.Project) identity.ProjectInfo {
	prog := project.DeriveContractProgress(p.Contract, p.Observations)
	return identity.ProjectInfo{
		ID: p.ID, Name: p.Name, Description: p.Description,
		State: p.State, Focus: p.Focus, Dir: p.Dir,
		Attributes: p.Attributes,
		Parent:     p.Parent,
		Contract: identity.ProjectContract{
			Outcome:     p.Contract.Outcome,
			Acceptance:  p.Contract.Acceptance,
			Constraints: p.Contract.Constraints,
		},
		Card:         project.RenderProjectCard(p.Name, p.Focus, p.Contract, prog),
		ProgressLine: project.RenderProgressLine(prog),
	}
}

func (x projectsAdapter) RecordEvidence(id, item, class, ref, note string) (identity.ProjectInfo, error) {
	p, err := x.a.projects.RecordObservationByText(id, item, class, ref, note, "identity")
	if err != nil {
		return identity.ProjectInfo{}, err
	}
	x.a.pushProjects()
	return info(p), nil
}

func (x projectsAdapter) Waive(id, item, reason string) (identity.ProjectInfo, error) {
	p, err := x.a.projects.WaiveItemByText(id, item, "identity", reason)
	if err != nil {
		return identity.ProjectInfo{}, err
	}
	x.a.pushProjects()
	return info(p), nil
}

// .
// .
// .
func projectDashboardProgress(p *project.Project) *dashboard.ContractProgress {
	prog := project.DeriveContractProgress(p.Contract, p.Observations)
	items := make([]dashboard.AcceptanceItem, 0, len(prog.Items))
	for _, it := range prog.Items {
		items = append(items, dashboard.AcceptanceItem{
			Index: it.Index, Text: it.Text, State: it.State,
			Class: it.Class, Ref: it.Ref, Stale: it.Stale,
		})
	}
	return &dashboard.ContractProgress{
		HasCriteria: prog.HasCriteria, Items: items, NextIndex: prog.NextIndex,
		Counts: prog.Counts, ClosureAllowed: prog.ClosureAllowed, ClosureReason: prog.ClosureReason,
	}
}

func (x projectsAdapter) List() ([]identity.ProjectInfo, error) {
	ps, err := x.a.projects.List()
	if err != nil {
		return nil, err
	}
	out := make([]identity.ProjectInfo, 0, len(ps))
	for _, p := range ps {
		if p.State == "archived" {
			continue
		}
		out = append(out, info(p))
	}
	return out, nil
}

func (x projectsAdapter) Create(name, description string, parent *string, contract *identity.ProjectContract, attributes map[string]interface{}) (identity.ProjectInfo, error) {
	var c *project.Contract
	if contract != nil {
		c = &project.Contract{Outcome: contract.Outcome, Acceptance: contract.Acceptance, Constraints: contract.Constraints}
	}
	p, err := x.a.projects.Create(name, description, "identity", parent, c, attributes)
	if err != nil {
		return identity.ProjectInfo{}, err
	}
	x.a.pushProjects()
	return info(p), nil
}

func (x projectsAdapter) Update(id, name, description, focus string, parent *string, contract *identity.ProjectContract, attributes map[string]interface{}) (identity.ProjectInfo, error) {
	var c *project.Contract
	if contract != nil {
		c = &project.Contract{
			Outcome:     contract.Outcome,
			Acceptance:  contract.Acceptance,
			Constraints: contract.Constraints,
		}
	}
	p, err := x.a.projects.Update(id, name, description, focus, parent, c, attributes)
	if err != nil {
		return identity.ProjectInfo{}, err
	}
	x.a.pushProjects()
	return info(p), nil
}

func (x projectsAdapter) SetState(id, state string) (identity.ProjectInfo, error) {
	p, err := x.a.closeOrReopen(id, state)
	if err != nil {
		return identity.ProjectInfo{}, err
	}
	x.a.pushProjects()
	return info(p), nil
}

// .
// .
// .
// .
// .
// .
func (a *App) closeOrReopen(id, state string) (*project.Project, error) {
	// .
	// .
	// .
	// .
	// .
	a.projectMu.Lock()
	defer a.projectMu.Unlock()

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
	refocus := ""
	if state != "open" && a.store.ActiveProjectID() == id {
		if err := a.store.SetActiveProject(""); err != nil {
			return nil, fmt.Errorf("focus not cleared, so the project was NOT %s: %w", state, err)
		}
		refocus = id
	}
	p, err := a.projects.SetState(id, state)
	if err != nil {
		if refocus != "" {
			// .
			// .
			// .
			// .
			if rerr := a.store.SetActiveProject(refocus); rerr != nil {
				return nil, fmt.Errorf("%w (focus was cleared for the close and could not be restored: %v)", err, rerr)
			}
		}
		return nil, err
	}
	if state != "open" {
		appendLineage(p, state)
	}
	return p, nil
}

// .
// .
// .
// .
// .
func (a *App) deleteProject(id string) error {
	a.projectMu.Lock()
	defer a.projectMu.Unlock()
	if a.store.ActiveProjectID() == id {
		if err := a.store.SetActiveProject(""); err != nil {
			return fmt.Errorf("focus not cleared, so the project was NOT deleted: %w", err)
		}
	}
	dest, err := a.projects.Delete(id)
	if err != nil {
		return err
	}
	log.Printf("projects: %s deleted — its directory is kept at %s", id, dest)
	return nil
}

// .
// .
// .
// .
// .
// .
func appendLineage(p *project.Project, state string) {
	line := "- " + state + " " + time.Now().UTC().Format("2006-01-02") + ": "
	if f := strings.TrimSpace(p.Focus); f != "" {
		if i := strings.IndexByte(f, '\n'); i > 0 {
			f = f[:i]
		}
		line += f
	} else {
		line += "(no focus recorded)"
	}
	fh, err := os.OpenFile(filepath.Join(p.Dir, "lineage.md"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("projects: lineage append: %v", err)
		return
	}
	if _, err := fh.WriteString(line + "\n"); err != nil {
		log.Printf("projects: lineage write: %v", err)
	}
	fh.Close()
}

// .
// .
// .
func (x projectsAdapter) Deselect() (string, error) {
	name, err := x.a.deselectProject()
	if err != nil {
		return "", err
	}
	x.a.pushProjects()
	return name, nil
}

func (x projectsAdapter) Select(id string) (identity.ProjectInfo, error) {
	pi, err := x.a.selectProject(id)
	if err != nil {
		return identity.ProjectInfo{}, err
	}
	x.a.pushProjects()
	return pi, nil
}

// .
// .
// .
// .
func (a *App) pushProjects() {
	if a.dashboard != nil {
		a.dashboard.BroadcastProjects()
	}
}

// .
// .
// .
func descriptionDeref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// .
// .
// .
// .
func derefAttrs(p *map[string]interface{}) map[string]interface{} {
	if p == nil {
		return nil
	}
	return *p
}

// .
// .
// .
func namePatch(name string) *string {
	if name == "" {
		return nil
	}
	return &name
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
func (a *App) activeOpenProject() (*project.Project, string) {
	id := a.store.ActiveProjectID()
	if id == "" || a.projects == nil {
		return nil, ""
	}
	p, err := a.projects.Load(id)
	if err != nil {
		return nil, fmt.Sprintf("project %q does not load (%v)", id, err)
	}
	if p.State != "open" {
		return nil, fmt.Sprintf("project %q is %s, not open", id, p.State)
	}
	return p, ""
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
func (a *App) deselectProject() (string, error) {
	a.projectMu.Lock()
	defer a.projectMu.Unlock()
	prev := a.store.ActiveProjectID()
	if prev == "" {
		return "", nil
	}
	name := prev
	if pp, err := a.projects.Load(prev); err == nil {
		name = pp.Name
	}
	transition := fmt.Sprintf("Project focus: was working in %q, now working outside any project", name)
	// .
	// .
	// .
	if err := a.store.SetActiveProjectAndRecord("", transition); err != nil {
		return "", fmt.Errorf("focus persistence failed, project not deselected: %w", err)
	}
	return name, nil
}

func (a *App) selectProject(id string) (identity.ProjectInfo, error) {
	a.projectMu.Lock()
	defer a.projectMu.Unlock()
	p, err := a.projects.Load(id)
	if err != nil {
		return identity.ProjectInfo{}, err
	}
	if p.State != "open" {
		return identity.ProjectInfo{}, fmt.Errorf("project %q is closed — reopen it first", p.Name)
	}
	prev := a.store.ActiveProjectID()
	if prev == p.ID {
		return info(p), nil
	}
	transition := fmt.Sprintf("Project focus: now working in %q [%s]", p.Name, p.ID)
	if prev != "" {
		if pp, err := a.projects.Load(prev); err == nil {
			transition = fmt.Sprintf("Project focus: was working in %q, now working in %q [%s]", pp.Name, p.Name, p.ID)
		}
	}
	// .
	// .
	// .
	if err := a.store.SetActiveProjectAndRecord(p.ID, transition); err != nil {
		return identity.ProjectInfo{}, fmt.Errorf("focus persistence failed, project not selected: %w", err)
	}
	return info(p), nil
}

// .
// .
// .
func projectContractFromDashboard(c *dashboard.ProjectContract) *project.Contract {
	if c == nil {
		return nil
	}
	return &project.Contract{
		Outcome:     c.Outcome,
		Acceptance:  c.Acceptance,
		Constraints: c.Constraints,
	}
}
