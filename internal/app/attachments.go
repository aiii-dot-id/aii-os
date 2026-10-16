package app

import (
	"context"
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func (a *App) recordAttachments(ctx context.Context, messageID string) ([]string, error) {
	return a.recordBeside(ctx, messageID, dashboard.Attachments(ctx), store.ReasonAttached, "the note that %s was attached")
}

func (a *App) recordBeside(ctx context.Context, messageID string, facts []string, reason, what string) ([]string, error) {
	if len(facts) == 0 || messageID == "" || a.engine == nil {
		return nil, nil
	}
	var recorded []string
	var failed error
	for _, fact := range facts {
		if _, err := a.engine.RecordRelatedConversationRef(ctx, string(interaction.System), fact, messageID,
			interaction.Details{ReasonCode: reason}); err != nil {
			logsink.Warn("turn.error", what+" was not recorded: %v", fact, err)
			if failed == nil {
				failed = fmt.Errorf(what+" could not be recorded: %w", fact, err)
			}
			continue
		}
		recorded = append(recorded, fact)
	}
	return recorded, failed
}

func (a *App) recordCameWith(ctx context.Context, messageID string) error {
	_, err := a.recordAttachments(ctx, messageID)
	return err
}

func attachedLines(paths []string) string {
	lines := ""
	for _, path := range paths {
		lines += "\n[your operator attached a file: " + path + "]"
	}
	return lines
}

func withAttached(content string, paths []string) string {
	if len(paths) == 0 {
		return content
	}
	return content + "\n" + attachedLines(paths)
}

func safeAttached(turns []identity.SafeTurn) map[string][]string {
	var attached map[string][]string
	for _, t := range turns {
		r := t.Record
		if r.Role != interaction.System || r.Kind != interaction.Notice || r.Details.ReasonCode != store.ReasonAttached || r.RelatedID == "" {
			continue
		}
		if attached == nil {
			attached = map[string][]string{}
		}
		attached[r.RelatedID] = append(attached[r.RelatedID], r.Content)
	}
	return attached
}
