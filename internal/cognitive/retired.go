package cognitive

import (
	"fmt"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store/rows"
)

type RetiredSource interface {
	RetiredBeliefs(after uint64, limit int) ([]rows.RetiredBelief, int, error)
}

const retiredShown = 8

func retiredBlock(retired []rows.RetiredBelief, more int, listed map[string]bool) []string {
	if len(retired) == 0 {
		return nil
	}
	for _, r := range retired {
		listed[r.ID] = true
	}
	lines := []string{"Beliefs retired since the oldest experience above (an experience recorded before a belief was retired is not evidence for it now; one recorded after may be):"}
	for _, r := range retired {
		replaced := "nothing replaced it"
		if r.Replacement.ID != "" {
			replaced = "replaced by " + describeTensionEnd(r.Replacement, r.Replacement.ID, listed)
		}
		lines = append(lines, fmt.Sprintf("  [%s] %q — retired %s, %s", r.ID, excerpt(r.Statement), stamp(r.RetiredAt), replaced))
	}
	if more > 0 {
		lines = append(lines, fmt.Sprintf("  (and %d earlier retirement(s) not shown here)", more))
	}
	return lines
}

func (c *ConsolidateFacility) retiredSince(experiences []rows.Experience) ([]rows.RetiredBelief, int) {
	if len(experiences) == 0 {
		return nil, 0
	}
	oldest := experiences[0].CreatedSeq
	for _, e := range experiences[1:] {
		oldest = min(oldest, e.CreatedSeq)
	}
	retired, more, err := c.store.RetiredBeliefs(oldest, retiredShown)
	if err != nil {
		logsink.Warn("consolidate.error", "%v — the pass runs without the retired-belief view", err)
		return nil, 0
	}
	return retired, more
}

func stamp(ts string) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return ts
	}
	return t.UTC().Format("2006-01-02T15:04:05Z")
}
