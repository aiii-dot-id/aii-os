package app

import "testing"

// The page is driven where the journey's browser rig runs: it starts and
// ends a browser by process group, which is unix's. On Windows the trial
// ends at what the App reports and says so in its record.
func meaningTrialPage(t *testing.T, a *App, card, cue, expect string) map[string]any {
	t.Helper()
	return map[string]any{"driven": false, "why": "the browser rig of this journey runs on Linux and macOS"}
}
