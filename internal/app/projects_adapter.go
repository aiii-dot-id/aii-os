package app

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/project"
)

// projectsAdapter implements identity.ProjectPort over project.Manager.
// The substrate owns the focus switch: store stamp (turns and work
// sessions carry the focused project id) and the transcript record of
// the transition (R62: "I was working on X. Now I am working on Y" —
// that marker is core FRAMING; richer RING4 working-state management is
// the plugin era's job).
type projectsAdapter struct{ a *App }

func info(p *project.Project) identity.ProjectInfo {
	return identity.ProjectInfo{
		ID: p.ID, Name: p.Name, Description: p.Description,
		State: p.State, Focus: p.Focus, Dir: p.Dir,
	}
}

func (x projectsAdapter) List() ([]identity.ProjectInfo, error) {
	ps, err := x.a.projects.List()
	if err != nil {
		return nil, err
	}
	out := make([]identity.ProjectInfo, 0, len(ps))
	for _, p := range ps {
		out = append(out, info(p))
	}
	return out, nil
}

func (x projectsAdapter) Create(name, description string) (identity.ProjectInfo, error) {
	p, err := x.a.projects.Create(name, description, "identity", nil)
	if err != nil {
		return identity.ProjectInfo{}, err
	}
	return info(p), nil
}

func (x projectsAdapter) Update(id, name, description, focus string) (identity.ProjectInfo, error) {
	p, err := x.a.projects.Update(id, name, description, focus, nil)
	if err != nil {
		return identity.ProjectInfo{}, err
	}
	return info(p), nil
}

func (x projectsAdapter) SetState(id, state string) (identity.ProjectInfo, error) {
	p, err := x.a.projects.SetState(id, state)
	if err != nil {
		return identity.ProjectInfo{}, err
	}
	if state == "closed" && x.a.store.ActiveProjectID() == p.ID {
		if err := x.a.store.SetActiveProject(""); err != nil {
			return identity.ProjectInfo{}, fmt.Errorf("focus persistence failed, project not closed: %w", err)
		} // closing the focused project clears focus
	}
	if state == "closed" {
		appendLineage(p)
	}
	return info(p), nil
}

// appendLineage writes one MECHANICAL line into the project's own
// directory at close (evaluate layer, 2026-08-26; R62: project truth is
// files in the project dir). Facts only — when, and the focus the work
// closed under; the identity enriches lineage.md with meaning, or
// doesn't, and either way the trail of attempts stops being memory-only.
// Fail-soft: a project that cannot take the line still closes.
func appendLineage(p *project.Project) {
	line := "- closed " + time.Now().UTC().Format("2006-01-02") + ": "
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

// Select switches focus — shared by the identity's verb and the
// operator's project bubbles.
func (x projectsAdapter) Select(id string) (identity.ProjectInfo, error) {
	return x.a.selectProject(id)
}

// descriptionDeref is the create-path bridge for PATCH semantics: a
// create with no description and one with an explicit empty
// description are the same thing — a project born with no description.
func descriptionDeref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// namePatch is the update-path bridge: Name is a plain string on the
// wire (it is not clearable), so absent is indistinguishable from
// empty and both mean "no change" — exactly the stringly rule.
func namePatch(name string) *string {
	if name == "" {
		return nil
	}
	return &name
}

// selectProject is the ONE focus-switch path. Previous detail leaves
// RING4 (the work-state block re-derives from the new focus); the
// transition is recorded in the transcript so both hands see it.
func (a *App) selectProject(id string) (identity.ProjectInfo, error) {
	p, err := a.projects.Load(id)
	if err != nil {
		return identity.ProjectInfo{}, err
	}
	if p.State != "open" {
		return identity.ProjectInfo{}, fmt.Errorf("project %q is closed — reopen it first", p.Name)
	}
	prev := a.store.ActiveProjectID()
	if prev == p.ID {
		return info(p), nil // already focused — no transition to record
	}
	if err := a.store.SetActiveProject(p.ID); err != nil {
		// Focus did not move: an unpersisted focus is a restart lie —
		// the operator believes the transition happened, the next boot
		// disagrees. Fail the action; the operator sees the refusal.
		return identity.ProjectInfo{}, fmt.Errorf("focus persistence failed, project not selected: %w", err)
	}
	transition := fmt.Sprintf("Project focus: now working in %q [%s]", p.Name, p.ID)
	if prev != "" {
		if pp, err := a.projects.Load(prev); err == nil {
			transition = fmt.Sprintf("Project focus: was working in %q, now working in %q [%s]", pp.Name, p.Name, p.ID)
		}
	}
	if err := a.store.AddConversationTurn("system", transition); err != nil {
		log.Printf("Warning: project transition not recorded in transcript: %v", err)
	}
	return info(p), nil
}
