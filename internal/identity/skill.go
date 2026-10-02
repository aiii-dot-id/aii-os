package identity

import (
	"context"
	"fmt"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

type skillStore interface {
	ProposeSkill(title, delta, evidence string) (string, error)
	ListSkillProposals(limit int) ([]store.SkillProposal, error)
}

const SkillProposalFate = "Nothing promotes a proposal into your doctrine, and no operator view lists proposals."

func (e *Engine) verbSkill(_ context.Context, args map[string]interface{}) (string, error) {
	action, _ := args["action"].(string)
	switch action {
	case "propose":
		title, _ := args["title"].(string)
		delta, _ := args["delta"].(string)
		evidence, _ := args["evidence"].(string)
		id, err := e.store.ProposeSkill(title, delta, evidence)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Skill proposal %s recorded in your own store, not your signed ledger. %s recall source=skills reads them back.", id, SkillProposalFate), nil

	case "list":
		props, err := e.store.ListSkillProposals(10)
		if err != nil {
			return "", err
		}
		if len(props) == 0 {
			return "No skill proposals recorded.", nil
		}

		words := strings.Fields(strings.ToLower(stringArg(args, "query")))
		var lines []string
		for _, p := range props {
			if !carriesWords(p.ID+" "+p.Title+" "+p.Delta, words) {
				continue
			}
			lines = append(lines, fmt.Sprintf("- %s %s", p.ID, p.Title))
		}
		if len(lines) == 0 {
			return fmt.Sprintf("No skill proposal mentions %q (%d recorded).", stringArg(args, "query"), len(props)), nil
		}
		heading := fmt.Sprintf("Skill proposals (%d, newest first):", len(props))
		if len(lines) != len(props) {
			heading = fmt.Sprintf("Skill proposals (%d of %d, newest first):", len(lines), len(props))
		}
		return heading + "\n" + strings.Join(lines, "\n"), nil

	default:
		return "", fmt.Errorf("skill: unknown action %q (propose, list)", action)
	}
}
