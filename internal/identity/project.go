package identity

import (
	"context"
	"fmt"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
// .
// .
// .
// .
// .
// .

// .
type ProjectInfo struct {
	ID          string
	Name        string
	Description string
	State       string
	Focus       string
	Dir         string
	// .
	// .
	// .
	Attributes map[string]interface{}
	// .
	// .
	// .
	// .
	// .
	Contract ProjectContract
	Parent   string
	// .
	// .
	// .
	Card         string
	ProgressLine string
}

// .
// .
// .
type ProjectContract struct {
	Outcome     string
	Acceptance  []string
	Constraints []string
}

// .
// .
// .
// .
// .
// .
// .
type ProjectPort interface {
	List() ([]ProjectInfo, error)
	Create(name, description string, parent *string, contract *ProjectContract, attributes map[string]interface{}) (ProjectInfo, error)
	// .
	// .
	// .
	Update(id, name, description, focus string, parent *string, contract *ProjectContract, attributes map[string]interface{}) (ProjectInfo, error)
	SetState(id, state string) (ProjectInfo, error)
	Select(id string) (ProjectInfo, error)
	// .
	// .
	Deselect() (string, error)
	// .
	// .
	// .
	RecordEvidence(id, item, class, ref, note string) (ProjectInfo, error)
	Waive(id, item, reason string) (ProjectInfo, error)
}

// .
// .
func (e *Engine) SetProjects(p ProjectPort) { e.projects = p }

func (e *Engine) verbProject(ctx context.Context, args map[string]interface{}) (string, error) {
	if e.projects == nil {
		return "", fmt.Errorf("projects are not wired on this runtime")
	}
	action, _ := args["action"].(string)
	id, _ := args["project"].(string)
	name, _ := args["name"].(string)
	description, _ := args["description"].(string)
	focus, _ := args["focus"].(string)
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	var parent *string
	if raw, ok := args["parent"]; ok {
		s, isStr := raw.(string)
		if !isStr {
			return "", fmt.Errorf("parent must be a project id string, got %T", raw)
		}
		t := strings.TrimSpace(s)
		parent = &t
	}
	// .
	// .
	// .
	// .
	// .
	var contract *ProjectContract
	outcome, hasOutcome := args["outcome"].(string)
	acceptance, hasAcceptance, err := stringList("acceptance", args["acceptance"])
	if err != nil {
		return "", err
	}
	constraints, hasConstraints, err := stringList("constraints", args["constraints"])
	if err != nil {
		return "", err
	}
	if hasOutcome || hasAcceptance || hasConstraints {
		contract = &ProjectContract{Outcome: strings.TrimSpace(outcome), Acceptance: acceptance, Constraints: constraints}
	}
	var attributes map[string]interface{}
	if raw, ok := args["attributes"]; ok && raw != nil {
		m, ok := raw.(map[string]interface{})
		if !ok {
			return "", fmt.Errorf("attributes must be an object, got %T", raw)
		}
		attributes = m
	}
	switch action {
	case "list", "":
		ps, err := e.projects.List()
		if err != nil {
			return "", err
		}
		if len(ps) == 0 {
			return "No projects yet. Create one with work action=project.create name=... — a project is a durable workroom you share with your operator.", nil
		}
		// .
		// .
		words := strings.Fields(strings.ToLower(stringArg(args, "query")))
		var out []string
		out = append(out, "Projects (open first):")
		shown := 0
		for _, p := range ps {
			var block []string
			line := fmt.Sprintf("  [%s, %s] %s", p.ID, p.State, p.Name)
			if p.Parent != "" {
				line = fmt.Sprintf("  [%s, %s, under %s] %s", p.ID, p.State, p.Parent, p.Name)
			}
			if p.Description != "" {
				line += " — " + p.Description
			}
			block = append(block, line)
			// .
			// .
			// .
			if p.Contract.Outcome != "" {
				block = append(block, "      pursuing: "+p.Contract.Outcome)
			}
			if len(p.Contract.Acceptance) > 0 {
				block = append(block, "      confirmed by: "+strings.Join(p.Contract.Acceptance, "; "))
			}
			if len(p.Contract.Constraints) > 0 {
				block = append(block, "      bounded by: "+strings.Join(p.Contract.Constraints, "; "))
			}
			if p.ProgressLine != "" {
				block = append(block, "      "+p.ProgressLine)
			}
			if !carriesWords(strings.Join(block, "\n"), words) {
				continue
			}
			shown++
			out = append(out, block...)
		}
		if shown == 0 {
			return fmt.Sprintf("No project mentions %q (%d recorded). Omit query to list them all.", stringArg(args, "query"), len(ps)), nil
		}
		return strings.Join(out, "\n"), nil
	case "create":
		p, err := e.projects.Create(name, description, parent, contract, attributes)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Project %q created [%s] at %s. Select it to work there.", p.Name, p.ID, p.Dir), nil
	case "update":
		p, err := e.projects.Update(id, name, description, focus, parent, contract, attributes)
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
	case "deselect":
		// .
		// .
		name, err := e.projects.Deselect()
		if err != nil {
			return "", err
		}
		if name == "" {
			return "No project was focused; nothing changed.", nil
		}
		return fmt.Sprintf("Focus dropped: %q is no longer your working project. It stays open, and any work session you hold is untouched.", name), nil

	case "select":
		p, err := e.projects.Select(id)
		if err != nil {
			return "", err
		}
		msg := fmt.Sprintf("Focus switched to %q [%s]. Its context is now your working state; the previous project's detail has left it.", p.Name, p.ID)
		if p.Focus != "" {
			msg += " Where you left off: " + p.Focus
		}
		if p.Card != "" {
			msg += "\n\n" + p.Card
		}
		return msg, nil
	case "evidence":
		item, _ := args["item"].(string)
		class, _ := args["class"].(string)
		ref, _ := args["ref"].(string)
		note, _ := args["note"].(string)
		class = strings.TrimSpace(class)
		if !store.IsEvidenceClass(class) {
			return "", fmt.Errorf("evidence needs a class — one of: not_run, rejected_before_effect, completed_locally, partial_or_mixed, external_effect_unknown, worker_report_only, locally_verified, host_receipted (to accept an item without evidence, use action=waive)")
		}
		if store.EvidenceVerifiedTier(class) {
			// .
			// .
			// .
			// .
			// .
			if subID, _ := ctx.Value(SubagentWorkSession{}).(string); subID != "" {
				return "", fmt.Errorf("a worker seat cannot mark a project acceptance item %q — only the primary seat verifies; record worker_report_only and let the primary seat verify from its own reads", class)
			}
			if strings.TrimSpace(ref) == "" {
				return "", fmt.Errorf("a %s observation needs ref= — the durable pointer to the check you read back; a verified item is not a bare claim", class)
			}
		}
		p, err := e.projects.RecordEvidence(id, item, class, ref, note)
		if err != nil {
			return "", err
		}
		return "Recorded evidence against the acceptance item. " + p.ProgressLine, nil
	case "waive":
		item, _ := args["item"].(string)
		reason, _ := args["reason"].(string)
		p, err := e.projects.Waive(id, item, reason)
		if err != nil {
			return "", err
		}
		return "Waived the acceptance item (explicit, attributable). " + p.ProgressLine, nil
	default:
		return "", fmt.Errorf("unknown project action %q — list, create, update, close, select, deselect, evidence, waive", action)
	}
}

// .
// .
// .
// .
// .
func stringList(field string, raw interface{}) ([]string, bool, error) {
	if raw == nil {
		return nil, false, nil
	}
	items, ok := raw.([]interface{})
	if !ok {
		return nil, false, fmt.Errorf("%s must be a list of strings, got %T. Nothing was written — send %s as an array, e.g. [\"the installer runs clean\"]", field, raw, field)
	}
	out := make([]string, 0, len(items))
	for i, it := range items {
		s, ok := it.(string)
		if !ok {
			return nil, false, fmt.Errorf("%s[%d] must be a string, got %T. Nothing was written", field, i, it)
		}
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out, true, nil
}
