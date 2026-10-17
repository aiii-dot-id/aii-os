package store

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

// clockRow is one conversation row written at a sequence and a wall time
// the test chooses, as a clock set back or forward would stamp it. A tool
// row has a call (outcome "") or a result; any other kind is a message.
type clockRow struct {
	seq           int64
	kind, turn    string
	tool, outcome string
	at            time.Time
}

// clockStore writes rows into a fresh store on disk, closes it and opens
// it again, as a boot after the run that wrote them would.
func clockStore(t *testing.T, rows []clockRow) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "aii.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		stamp, err := interaction.NormalizeTime(r.at)
		if err != nil {
			t.Fatal(err)
		}
		if r.kind == "message" {
			if _, err := s.DB().Exec(`INSERT INTO conversations(id,session_id,role,content,turn_seq,created_at,kind,turn_id,recorded_at) VALUES(?,'default','operator','hello',?,?,'message',?,?)`,
				r.turn+"_message", r.seq, stamp, r.turn, stamp); err != nil {
				t.Fatal(err)
			}
			continue
		}
		id := toolExecutionID(r.turn, 1)
		details := `{"actor":"main","tool":"` + r.tool + `","emission_ordinal":1}`
		related := any(nil)
		if r.kind == "tool_result" {
			related = "tool_call_" + id
		}
		if _, err := s.DB().Exec(`INSERT INTO conversations(id,session_id,role,content,turn_seq,created_at,kind,turn_id,related_id,source_kind,source_id,recorded_at,outcome,details)
			VALUES(?,'default','system','→ tool',?,?,?,?,?,'tool',?,?,?,?)`,
			r.kind+"_"+id, r.seq, stamp, r.kind, r.turn, related, id, stamp, r.outcome, details); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestAToolWindowIsDecidedByTimeWhateverTheClockDid(t *testing.T) {
	now := time.Now()
	t.Run("clock set back: a recent unfinished write is named", func(t *testing.T) {
		s := clockStore(t, []clockRow{
			{seq: 1, kind: "tool_call", turn: "turn-w", tool: "write", at: now.Add(-time.Hour)},
			{seq: 2, kind: "message", turn: "turn-m", at: now.Add(-2 * time.Hour)},
		})
		names, err := s.AbandonUnfinishedToolCalls()
		if err != nil || !slices.Equal(names, []string{"write"}) {
			t.Fatalf("the boot named %v (%v), want [write]", names, err)
		}
		if open, err := s.ToolCallStarted("turn-w", 1); err != nil || open {
			t.Fatalf("the named call is still open: %v %v", open, err)
		}
		var outcome string
		if err := s.DB().QueryRow(`SELECT outcome FROM conversations WHERE kind='tool_result' AND turn_id='turn-w'`).Scan(&outcome); err != nil || outcome != "unknown" {
			t.Fatalf("the abandoned call's durable outcome is %q (%v), want unknown", outcome, err)
		}
		if again, err := s.AbandonUnfinishedToolCalls(); err != nil || len(again) != 0 {
			t.Fatalf("a second boot named %v (%v), want nothing", again, err)
		}
	})
	t.Run("clock set forward: a call stamped ahead is named", func(t *testing.T) {
		s := clockStore(t, []clockRow{
			{seq: 1, kind: "tool_call", turn: "turn-f", tool: "edit", at: now.Add(time.Hour)},
			{seq: 2, kind: "message", turn: "turn-m", at: now.Add(-time.Hour)},
		})
		if names, err := s.AbandonUnfinishedToolCalls(); err != nil || !slices.Equal(names, []string{"edit"}) {
			t.Fatalf("the boot named %v (%v), want [edit]", names, err)
		}
	})
	t.Run("results with stamps falling as sequence rises are all counted", func(t *testing.T) {
		s := clockStore(t, []clockRow{
			{seq: 1, kind: "tool_call", turn: "turn-1", tool: "read", at: now.Add(-time.Hour)},
			{seq: 2, kind: "tool_result", turn: "turn-1", tool: "read", outcome: "failed", at: now.Add(-time.Hour)},
			{seq: 3, kind: "tool_call", turn: "turn-2", tool: "read", at: now.Add(-3 * time.Hour)},
			{seq: 4, kind: "tool_result", turn: "turn-2", tool: "read", outcome: "succeeded", at: now.Add(-3 * time.Hour)},
		})
		if done, failed, truncated, err := s.ToolResultStats(48 * time.Hour); err != nil || done != 2 || failed != 1 || truncated != 0 {
			t.Fatalf("48 hours of results = (%d,%d,%d) %v, want (2,1,0)", done, failed, truncated, err)
		}
	})
	t.Run("an old row recorded after recent ones stays outside", func(t *testing.T) {
		s := clockStore(t, []clockRow{
			{seq: 1, kind: "tool_call", turn: "turn-r", tool: "read", at: now.Add(-time.Hour)},
			{seq: 2, kind: "tool_result", turn: "turn-r", tool: "read", outcome: "succeeded", at: now.Add(-time.Hour)},
			{seq: 3, kind: "tool_call", turn: "turn-o", tool: "write", at: now.Add(-17 * 24 * time.Hour)},
			{seq: 4, kind: "tool_call", turn: "turn-x", tool: "read", at: now.Add(-17 * 24 * time.Hour)},
			{seq: 5, kind: "tool_result", turn: "turn-x", tool: "read", outcome: "failed", at: now.Add(-17 * 24 * time.Hour)},
		})
		if names, err := s.AbandonUnfinishedToolCalls(); err != nil || len(names) != 0 {
			t.Fatalf("the boot named %v (%v): a call stamped 17 days back is outside the 14-day window", names, err)
		}
		if done, failed, _, err := s.ToolResultStats(unfinishedWindow); err != nil || done != 1 || failed != 0 {
			t.Fatalf("14 days of results = (%d,%d) %v, want (1,0)", done, failed, err)
		}
	})
	t.Run("the cutoff itself is inside", func(t *testing.T) {
		cutoff := now.Add(-48 * time.Hour)
		s := clockStore(t, []clockRow{
			{seq: 1, kind: "tool_call", turn: "turn-in", tool: "read", at: cutoff},
			{seq: 2, kind: "tool_result", turn: "turn-in", tool: "read", outcome: "failed", at: cutoff},
			{seq: 3, kind: "tool_call", turn: "turn-out", tool: "read", at: cutoff.Add(-time.Microsecond)},
			{seq: 4, kind: "tool_result", turn: "turn-out", tool: "read", outcome: "succeeded", at: cutoff.Add(-time.Microsecond)},
		})
		if done, failed, _, err := toolResultStatsSince(s.DB(), cutoff); err != nil || done != 1 || failed != 1 {
			t.Fatalf("results from the cutoff = (%d,%d) %v, want (1,1): the one at the cutoff, not the one a microsecond before", done, failed, err)
		}
	})
}
