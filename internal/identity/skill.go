package identity

import (
	"context"
	"fmt"
	"strings"
)

// .
// .
// .
// .
// .
// .
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
		return fmt.Sprintf("Skill proposal %s recorded (status: proposed). Your operator decides promotion; a replay harness will verify candidates once one exists — until then verified=none is the honest label.", id), nil

	case "list":
		props, err := e.store.ListSkillProposals(10)
		if err != nil {
			return "", err
		}
		if len(props) == 0 {
			return "No skill proposals recorded.", nil
		}
		// .
		words := strings.Fields(strings.ToLower(stringArg(args, "query")))
		var lines []string
		for _, p := range props {
			if !carriesWords(p.ID+" "+p.Status+" "+p.Verified+" "+p.Title+" "+p.Delta, words) {
				continue
			}
			lines = append(lines, fmt.Sprintf("- %s [%s, verified=%s] %s", p.ID, p.Status, p.Verified, p.Title))
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
