package identity

import (
	"context"
	"fmt"
	"strings"
)

// --- project: the identity's durable workrooms (R62) ---
//
// A project is a collection OUTSIDE AII OS — a directory of files, like
// a person's projects live in the world, not in their soul. The ledger
// never records projects; the identity remembers them through note and
// commit as with anything else. The engine sees projects only through
// this port: the substrate (app) owns the directories, the focus
// switch, and the transcript record of it.

// ProjectInfo is the engine's view of one project.
type ProjectInfo struct {
	ID          string
	Name        string
	Description string
	State       string
	Focus       string
	Dir         string
}

// ProjectPort is implemented by the substrate. Select switches focus:
// the chosen project's context enters RING4 working state and the
// previous one leaves it (the substrate records the transition).
type ProjectPort interface {
	List() ([]ProjectInfo, error)
	Create(name, description string) (ProjectInfo, error)
	Update(id, name, description, focus string) (ProjectInfo, error)
	SetState(id, state string) (ProjectInfo, error)
	Select(id string) (ProjectInfo, error)
}

// SetProjects wires the substrate's project manager. Nil = not wired
// (tests, minimal runtimes) — the verb answers honestly.
func (e *Engine) SetProjects(p ProjectPort) { e.projects = p }

func (e *Engine) verbProject(_ context.Context, args map[string]interface{}) (string, error) {
	if e.projects == nil {
		return "", fmt.Errorf("projects are not wired on this runtime")
	}
	action, _ := args["action"].(string)
	id, _ := args["project"].(string)
	name, _ := args["name"].(string)
	description, _ := args["description"].(string)
	focus, _ := args["focus"].(string)

	switch action {
	case "list", "":
		ps, err := e.projects.List()
		if err != nil {
			return "", err
		}
		if len(ps) == 0 {
			return "No projects yet. Create one with project(action=create, name=...) — a project is a durable workroom you share with your operator.", nil
		}
		var out []string
		out = append(out, "Projects (open first):")
		for _, p := range ps {
			line := fmt.Sprintf("  [%s, %s] %s", p.ID, p.State, p.Name)
			if p.Description != "" {
				line += " — " + p.Description
			}
			out = append(out, line)
		}
		return strings.Join(out, "\n"), nil
	case "create":
		p, err := e.projects.Create(name, description)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Project %q created [%s] at %s. Select it to work there.", p.Name, p.ID, p.Dir), nil
	case "update":
		p, err := e.projects.Update(id, name, description, focus)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Project %q updated.", p.Name), nil
	case "close":
		p, err := e.projects.SetState(id, "closed")
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Project %q closed. Nothing is deleted — the collection stays; only the state changed.", p.Name), nil
	case "select":
		p, err := e.projects.Select(id)
		if err != nil {
			return "", err
		}
		msg := fmt.Sprintf("Focus switched to %q [%s]. Its context is now your working state; the previous project's detail has left it.", p.Name, p.ID)
		if p.Focus != "" {
			msg += " Where you left off: " + p.Focus
		}
		return msg, nil
	default:
		return "", fmt.Errorf("unknown project action %q — list, create, update, close, select", action)
	}
}
