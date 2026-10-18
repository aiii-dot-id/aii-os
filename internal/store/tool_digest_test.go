package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

// digestRow is one tool_result written with the fields the digest reads:
// the tool and the reason code live in the row's details, the outcome in
// its own column. Refused is a real outcome the schema declares and the
// old stats SQL dropped, so the fixture must be able to write one.
type digestRow struct {
	tool     string
	outcome  string
	reason   string
	at       time.Time
	noTool   bool
	noReason bool
}

// digestStore writes the rows as results of distinct turns and returns the
// store, so each row is one call's completion.
func digestStore(t *testing.T, rows []digestRow) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "aii.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	seq := int64(0)
	for i, r := range rows {
		stamp, err := interaction.NormalizeTime(r.at)
		if err != nil {
			t.Fatal(err)
		}
		turn := "turn-" + string(rune('a'+i))
		id := toolExecutionID(turn, 1)
		details := `{"actor":"main","emission_ordinal":1`
		if !r.noTool {
			details += `,"tool":"` + r.tool + `"`
		}
		if !r.noReason && r.reason != "" {
			details += `,"reason_code":"` + r.reason + `"`
		}
		details += `}`
		// THE CALL LANDS FIRST, as the production path always does:
		// appendToolOutcome reads its parent call and errors when it is
		// absent. A result naming a call that was never written is a
		// shape the runtime cannot produce, and the schema's foreign key
		// (related_id REFERENCES conversations(id)) refuses it — which is
		// how this fixture's first version was caught.
		seq++
		if _, err := s.DB().Exec(`INSERT INTO conversations(id,session_id,role,content,turn_seq,created_at,kind,turn_id,source_kind,source_id,recorded_at,outcome,details)
			VALUES(?,'default','system','→ tool',?,?,?,?,'tool',?,?,'',?)`,
			"tool_call_"+id, seq, stamp, "tool_call", turn, id, stamp, details); err != nil {
			t.Fatal(err)
		}
		seq++
		if _, err := s.DB().Exec(`INSERT INTO conversations(id,session_id,role,content,turn_seq,created_at,kind,turn_id,related_id,source_kind,source_id,recorded_at,outcome,details)
			VALUES(?,'default','system','→ tool',?,?,?,?,?,'tool',?,?,?,?)`,
			"tool_result_"+id, seq, stamp, "tool_result", turn, "tool_call_"+id, id, stamp, r.outcome, details); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// A REFUSAL IS A CALL THAT DID NOT WORK (the learning loop's first level).
// A gate that refuses a call before anything runs records outcome
// 'refused' — a value the schema declares and ToolResultStats' own SQL
// excludes. So the line built to make failure ambient was silent about
// every refused call, and a resident could not see that one tool was
// being stopped repeatedly. The digest counts it.
func TestARefusedCallIsNotSilent(t *testing.T) {
	now := time.Now()
	s := digestStore(t, []digestRow{
		{tool: "shell", outcome: "refused", reason: "SHELL_WALL", at: now.Add(-time.Hour)},
		{tool: "shell", outcome: "refused", reason: "SHELL_WALL", at: now.Add(-2 * time.Hour)},
		{tool: "grep", outcome: "refused", reason: "PATH_REFUSED", at: now.Add(-3 * time.Hour)},
		{tool: "read", outcome: "succeeded", at: now.Add(-4 * time.Hour)},
	})
	// The old read, for the contrast the finding rests on: three refusals
	// happened and the total reports none of them.
	if done, failed, _, err := s.ToolResultStats(48 * time.Hour); err != nil || done != 1 || failed != 0 {
		t.Fatalf("the completion total = (%d,%d) %v; want (1,0) — it counts the one success and no refusal", done, failed, err)
	}
	got, err := s.ToolOutcomeDigest(48*time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("the digest named %d tools (%+v); want shell and grep, and not read — a tool with no non-success is noise", len(got), got)
	}
	shell := got[0]
	if shell.Tool != "shell" || shell.Refused != 2 || shell.NonSuccess() != 2 {
		t.Fatalf("the first entry is %+v; want shell with 2 refusals, leading on non-successes", shell)
	}
	if len(shell.Reasons) != 1 || shell.Reasons[0].Reason != "SHELL_WALL" || shell.Reasons[0].Count != 2 {
		t.Fatalf("shell's reasons are %+v; want SHELL_WALL×2", shell.Reasons)
	}
	if got[1].Tool != "grep" || got[1].Refused != 1 {
		t.Fatalf("the second entry is %+v; want grep with 1 refusal", got[1])
	}
}

// THE ORDER IS DETERMINISTIC AND THE WORST LEADS. A line whose order moved
// between turns would read as noise, and the shape a resident most needs
// to see is the tool that keeps stopping it.
func TestTheDigestOrdersByNonSuccessThenName(t *testing.T) {
	now := time.Now()
	s := digestStore(t, []digestRow{
		{tool: "zebra", outcome: "failed", at: now.Add(-time.Hour)},
		{tool: "alpha", outcome: "refused", reason: "X", at: now.Add(-2 * time.Hour)},
		{tool: "beta", outcome: "failed", at: now.Add(-3 * time.Hour)},
		{tool: "beta", outcome: "failed", at: now.Add(-4 * time.Hour)},
		{tool: "alpha", outcome: "failed", at: now.Add(-5 * time.Hour)},
	})
	got, err := s.ToolOutcomeDigest(48*time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	// alpha and beta both hold 2 non-successes — alpha's are a refusal and
	// a failure, beta's are two failures, and the digest counts them the
	// same — so the tie breaks by name ascending, then zebra's 1.
	want := []string{"alpha", "beta", "zebra"}
	if len(got) != len(want) {
		t.Fatalf("the digest named %+v; want %v", got, want)
	}
	for i, name := range want {
		if got[i].Tool != name {
			t.Fatalf("position %d is %q; want %q (full order %+v)", i, got[i].Tool, name, got)
		}
	}
}

// A WINDOW STILL DECIDES, and the cap is a display bound only.
func TestTheDigestHonoursTheWindowAndTheCap(t *testing.T) {
	now := time.Now()
	rows := []digestRow{
		{tool: "a", outcome: "failed", at: now.Add(-time.Hour)},
		{tool: "b", outcome: "failed", at: now.Add(-2 * time.Hour)},
		{tool: "c", outcome: "failed", at: now.Add(-3 * time.Hour)},
		{tool: "ancient", outcome: "failed", at: now.Add(-17 * 24 * time.Hour)},
	}
	s := digestStore(t, rows)
	got, err := s.ToolOutcomeDigest(48*time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range got {
		if o.Tool == "ancient" {
			t.Fatalf("a call 17 days old is inside the 48-hour window: %+v", got)
		}
	}
	capped, err := s.ToolOutcomeDigest(48*time.Hour, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(capped) != 2 {
		t.Fatalf("the cap of 2 returned %d entries: %+v", len(capped), capped)
	}
}

// A RESULT WITH NO TOOL NAME IS NOT ATTRIBUTED TO ONE. Guessing would be
// the substrate inventing a measurement, which is the one thing a line of
// measured numbers must never do.
func TestAResultWithNoToolIsNotAttributed(t *testing.T) {
	now := time.Now()
	s := digestStore(t, []digestRow{
		{noTool: true, outcome: "failed", at: now.Add(-time.Hour)},
		{tool: "read", outcome: "failed", at: now.Add(-2 * time.Hour)},
	})
	got, err := s.ToolOutcomeDigest(48*time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Tool != "read" {
		t.Fatalf("the digest named %+v; want only read — an unnamed result belongs to no tool", got)
	}
}

// EVERY OUTCOME THE SCHEMA DECLARES IS COUNTED, not only the two the old
// total read. unknown is a call a boot found unfinished, and cancelled one
// whose turn ended first; both are calls that did not simply work.
func TestEveryDeclaredOutcomeIsCounted(t *testing.T) {
	now := time.Now()
	s := digestStore(t, []digestRow{
		{tool: "shell", outcome: "failed", at: now.Add(-time.Hour)},
		{tool: "shell", outcome: "refused", reason: "SHELL_WALL", at: now.Add(-2 * time.Hour)},
		{tool: "shell", outcome: "cancelled", at: now.Add(-3 * time.Hour)},
		{tool: "shell", outcome: "unknown", at: now.Add(-4 * time.Hour)},
		{tool: "shell", outcome: "succeeded", at: now.Add(-5 * time.Hour)},
	})
	got, err := s.ToolOutcomeDigest(48*time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("the digest named %+v; want one entry for shell", got)
	}
	o := got[0]
	if o.Failed != 1 || o.Refused != 1 || o.Cancelled != 1 || o.Unknown != 1 || o.Succeeded != 1 {
		t.Fatalf("shell = %+v; want one of each outcome counted", o)
	}
	if o.NonSuccess() != 4 {
		t.Fatalf("shell's non-successes = %d; want 4 — success is not one", o.NonSuccess())
	}
}

// AN EMPTY LOG IS AN EMPTY DIGEST, not an error and not a zero-row line.
func TestAnEmptyLogYieldsNothing(t *testing.T) {
	s := digestStore(t, nil)
	got, err := s.ToolOutcomeDigest(48*time.Hour, 0)
	if err != nil {
		t.Fatalf("an empty log errored: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("an empty log yielded %+v; want nothing", got)
	}
}
