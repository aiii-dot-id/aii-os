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
func (e *Engine) verbCuriosity(_ context.Context, args map[string]interface{}) (string, error) {
	action, _ := args["action"].(string)
	switch action {
	case "note":
		subject, _ := args["subject"].(string)
		if strings.TrimSpace(subject) == "" {
			return "", fmt.Errorf("a curiosity cue needs a subject — the interest in your own words (this is where later retrieval finds it, not the word \"curiosity\")")
		}
		why, _ := args["why"].(string)
		pointer, _ := args["pointer"].(string)
		kind, _ := args["kind"].(string)
		kind = strings.TrimSpace(kind)
		if kind == "" {
			kind = "invitation"
		}
		if kind != "invitation" && kind != "active" {
			return "", fmt.Errorf("kind is invitation or active")
		}
		if err := e.store.SetCuriosityCue(strings.TrimSpace(subject), strings.TrimSpace(why), strings.TrimSpace(pointer), kind); err != nil {
			return "", fmt.Errorf("set curiosity cue: %w", err)
		}
		return "Noted a curiosity — it resurfaces as an invitation when nothing urgent is owed. It is not a task, it joins no queue, and it closes nothing. (For durable memory of it, a plain note is still the searchable record.)", nil
	case "clear":
		if err := e.store.ClearCuriosityCue(); err != nil {
			return "", fmt.Errorf("clear curiosity cue: %w", err)
		}
		return "Curiosity cue cleared. Any note you wrote about it stays in memory; only the surfaced invitation is gone.", nil
	case "show", "":
		cue, err := e.store.CuriosityCue()
		if err != nil {
			return "", fmt.Errorf("read curiosity cue: %w", err)
		}
		if cue == nil {
			return "No curiosity set. Leave yourself one with work action=curiosity subject=... — an invitation, never an obligation.", nil
		}
		return store.RenderCuriosityCue(*cue), nil
	default:
		return "", fmt.Errorf("unknown curiosity action %q — note, show, clear", action)
	}
}
