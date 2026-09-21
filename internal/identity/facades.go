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
func withAction(args map[string]interface{}, action string) map[string]interface{} {
	out := make(map[string]interface{}, len(args)+1)
	for k, v := range args {
		out[k] = v
	}
	out["action"] = action
	return out
}

// .
// .
func (e *Engine) workAbsorbed(ctx context.Context, action string, args map[string]interface{}) (out string, handled bool, err error) {
	switch {
	case action == "measure":
		out, err = e.verbMeasure(ctx, args)
	case action == "alarm.set":
		out, err = e.verbTimer(ctx, withAction(args, "set"))
	case action == "alarm.cancel":
		out, err = e.verbTimer(ctx, withAction(args, "cancel"))
	case action == "alarm.list" || action == "alarm.query":
		err = fmt.Errorf("work does not read alarms: recall source=alarms lists them (query words filter id, tag, message and status)")
	case action == "project.list":
		err = fmt.Errorf("work does not read projects: recall source=projects lists them with their contracts")
	case strings.HasPrefix(action, "project."):
		mode := strings.TrimPrefix(action, "project.")
		switch mode {
		case "create", "update", "close", "select", "deselect", "evidence", "waive":
			out, err = e.verbProject(ctx, withAction(args, mode))
		default:
			err = fmt.Errorf("unknown work action %q — the project modes are project.create, project.update, project.close, project.select, project.deselect, project.evidence and project.waive; recall source=projects reads them", action)
		}
	case action == "voice.mode":
		out, err = e.verbVoiceMode(ctx, args)
	case action == "backup.take":
		out, err = e.verbContinuity(ctx, withAction(args, continuityTake))
	case action == "backup.verify":
		out, err = e.verbContinuity(ctx, withAction(args, continuityVerify))
	case strings.HasPrefix(action, "backup."):
		err = fmt.Errorf("unknown work action %q — a snapshot of you is taken with backup.take and proved with backup.verify; recall source=continuity reads what you have. A restore, and the escrow of your keys, are your operator's to do", action)
	case action == "curiosity":
		out, err = e.verbCuriosity(ctx, withAction(args, "note"))
	case action == "curiosity.clear":
		out, err = e.verbCuriosity(ctx, withAction(args, "clear"))
	case action == "curiosity.show":
		err = fmt.Errorf("work does not read the cue: recall source=curiosity shows it")
	default:
		return "", false, nil
	}
	return out, true, err
}

// .
// .
// .
// .
func (e *Engine) recallStanding(ctx context.Context, source, query string) (string, error) {
	switch source {
	case "alarms":
		return e.verbTimer(ctx, map[string]interface{}{"action": "query", "query": query})
	case "projects":
		return e.verbProject(ctx, map[string]interface{}{"action": "list", "query": query})
	case "skills":
		return e.verbSkill(ctx, map[string]interface{}{"action": "list", "query": query})
	case "curiosity":
		return e.verbCuriosity(ctx, map[string]interface{}{"action": "show"})
	case "continuity":
		return e.verbContinuity(ctx, map[string]interface{}{"action": continuityRead, "query": query})
	}
	return "", fmt.Errorf("recall source %q is not a standing source", source)
}

// .
// .
func carriesWords(text string, words []string) bool {
	if len(words) == 0 {
		return true
	}
	lower := strings.ToLower(text)
	for _, w := range words {
		if !strings.Contains(lower, strings.ToLower(w)) {
			return false
		}
	}
	return true
}
