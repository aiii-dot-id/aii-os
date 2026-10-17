package app

import (
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// insertToolResult writes a call row and its result, as the runtime does:
// appendToolOutcome reads its parent call, and the schema's foreign key
// (related_id REFERENCES conversations(id)) refuses a result whose call
// was never written.
func insertToolResult(t *testing.T, s *store.Store, seq int64, turn, tool, outcome, reason string, at time.Time) {
	t.Helper()
	stamp, err := interaction.NormalizeTime(at)
	if err != nil {
		t.Fatal(err)
	}
	id := "exec-" + turn
	details := `{"actor":"main","emission_ordinal":1,"tool":"` + tool + `"`
	if reason != "" {
		details += `,"reason_code":"` + reason + `"`
	}
	details += `}`
	if _, err := s.DB().Exec(`INSERT INTO conversations(id,session_id,role,content,turn_seq,created_at,kind,turn_id,source_kind,source_id,recorded_at,outcome,details)
		VALUES(?,'default','system','→ tool',?,?,?,?,'tool',?,?,'',?)`,
		"tool_call_"+id, seq, stamp, "tool_call", turn, id, stamp, details); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT INTO conversations(id,session_id,role,content,turn_seq,created_at,kind,turn_id,related_id,source_kind,source_id,recorded_at,outcome,details)
		VALUES(?,'default','system','→ tool',?,?,?,?,?,'tool',?,?,?,?)`,
		"tool_result_"+id, seq+1, stamp, "tool_result", turn, "tool_call_"+id, id, stamp, outcome, details); err != nil {
		t.Fatal(err)
	}
}

// THE DIGEST REACHES THE PROMPT, NAMING TOOLS AND REFUSALS. The count the
// prompt already carried is a total: a resident could read "7 failed" and
// still not know that one tool accounted for most of it, or that calls a
// gate stopped before anything ran were never counted at all. This pins
// the clause that names them, in the line the resident already reads.
func TestToolDigestLineNamesToolsAndRefusals(t *testing.T) {
	s, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// One completed turn, so the rhythm block renders at all.
	if err := s.InsertTurnMetric(store.TurnMetric{TsMs: time.Now().UnixMilli(), Calls: 4, ReadOnly: 2}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	insertToolResult(t, s, 1, "t1", "shell", "refused", "SHELL_WALL", now.Add(-time.Hour))
	insertToolResult(t, s, 3, "t2", "shell", "refused", "SHELL_WALL", now.Add(-2*time.Hour))
	insertToolResult(t, s, 5, "t3", "grep", "failed", "", now.Add(-3*time.Hour))
	insertToolResult(t, s, 7, "t4", "read", "succeeded", "", now.Add(-4*time.Hour))

	ws, err := (&App{store: s}).buildTurnFacts(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	// The clause EXACTLY: shell's refusals with their reason lead (most
	// non-successes first), grep's failure follows, and read — which only
	// succeeded — is not named, because the clause is not a tool census.
	// (An earlier version of this test asserted the absence of the
	// substring "read" and failed on "50% read-only" in the rhythm line:
	// the oracle was wrong, not the code.)
	const want = "By tool, calls that did not simply work: shell 2 refused (SHELL_WALL×2); grep 1 failed."
	if !strings.Contains(ws, want) {
		t.Fatalf("the digest clause is missing or wrong; want %q:\n%s", want, ws)
	}
	// The finding the digest exists for: the refusal count is INVISIBLE to
	// the completion total, which reads (1,1) here — one success, one
	// failure, and two refusals it never sees.
	if !strings.Contains(ws, "Of the last 2 tool calls, 1 failed and 0 were truncated.") {
		t.Fatalf("the completion total changed shape; the contrast is the finding:\n%s", ws)
	}
}

// A CLEAN WINDOW RENDERS NO CLAUSE. Same doctrine as every other measured
// line here: it earns its place or it is noise a resident learns to skip.
func TestToolDigestLineIsSilentWhenNothingBroke(t *testing.T) {
	s, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.InsertTurnMetric(store.TurnMetric{TsMs: time.Now().UnixMilli(), Calls: 2, ReadOnly: 1}); err != nil {
		t.Fatal(err)
	}
	insertToolResult(t, s, 1, "t1", "read", "succeeded", "", time.Now().Add(-time.Hour))

	ws, err := (&App{store: s}).buildTurnFacts(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ws, "By tool") {
		t.Fatalf("a window with no non-success rendered a digest clause:\n%s", ws)
	}
}
