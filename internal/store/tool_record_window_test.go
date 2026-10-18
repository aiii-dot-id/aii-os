package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

// toolLog writes n tool calls into s's log as RecordToolStart and
// RecordToolDoneMeasured write them, the i-th recorded (n-i) spacings and an hour
// before now, so no call sits on a window's edge. A call in open has no
// result; every tenth result failed and every seventh was truncated.
func toolLog(tb testing.TB, s *Store, n int, spacing time.Duration, open map[int]bool) {
	tb.Helper()
	tx, err := s.DB().Begin()
	if err != nil {
		tb.Fatal(err)
	}
	defer tx.Rollback()
	insert, err := tx.Prepare(`INSERT INTO conversations(id,session_id,role,content,turn_seq,created_at,kind,turn_id,related_id,source_kind,source_id,recorded_at,outcome,details)
		VALUES(?,'default','system','→ read({})',?,?,?,?,?,'tool',?,?,?,?)`)
	if err != nil {
		tb.Fatal(err)
	}
	defer insert.Close()
	var seq int64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(turn_seq),0) FROM conversations`).Scan(&seq); err != nil {
		tb.Fatal(err)
	}
	now := time.Now()
	for i := 0; i < n; i++ {
		stamp, err := interaction.NormalizeTime(now.Add(-time.Duration(n-i)*spacing - time.Hour))
		if err != nil {
			tb.Fatal(err)
		}
		turn := fmt.Sprintf("turn-%d", i)
		id := toolExecutionID(turn, 1)
		seq++
		if _, err := insert.Exec("tool_call_"+id, seq, stamp, "tool_call", turn, nil, id, stamp, "", `{"actor":"main","tool":"read","emission_ordinal":1}`); err != nil {
			tb.Fatal(err)
		}
		if open[i] {
			continue
		}
		outcome, details := "succeeded", `{"actor":"main","tool":"read","emission_ordinal":1}`
		if i%10 == 0 {
			outcome = "failed"
		}
		if i%7 == 0 {
			details = `{"actor":"main","tool":"read","emission_ordinal":1,"truncated":true}`
		}
		seq++
		if _, err := insert.Exec("tool_result_"+id, seq, stamp, "tool_result", turn, "tool_call_"+id, id, stamp, outcome, details); err != nil {
			tb.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		tb.Fatal(err)
	}
}

func TestTheBootLooksBackFourteenDays(t *testing.T) {
	s := testStore(t)
	// 40 calls 12 hours apart: the oldest 20 days back.
	toolLog(t, s, 40, 12*time.Hour, map[int]bool{5: true, 30: true})
	names, err := s.AbandonUnfinishedToolCalls()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 {
		t.Fatalf("the boot named %v; want the one call left open 5 days ago, not the one 17 days ago", names)
	}
	if open, err := s.ToolCallStarted("turn-30", 1); err != nil || open {
		t.Fatalf("the call the boot named is still open: %v %v", open, err)
	}
	// 48 hours hold the last three calls, 37 to 39: each succeeded whole.
	done, failed, truncated, err := s.ToolResultStats(48 * time.Hour)
	if err != nil || done != 3 || failed != 0 || truncated != 0 {
		t.Fatalf("48 hours of results = (%d,%d,%d) %v, want (3,0,0)", done, failed, truncated, err)
	}
	// 14 days hold calls 13 to 39: 26 results, since turn-30 has none, and
	// the fixture's rule says which failed and which were truncated.
	done, failed, truncated, err = s.ToolResultStats(unfinishedWindow)
	if err != nil {
		t.Fatal(err)
	}
	var wantFailed, wantTruncated int
	for i := 13; i < 40; i++ {
		if i == 30 {
			continue
		}
		if i%10 == 0 {
			wantFailed++
		}
		if i%7 == 0 {
			wantTruncated++
		}
	}
	if done != 26 || failed != wantFailed || truncated != wantTruncated {
		t.Fatalf("14 days of results = (%d,%d,%d), want (26,%d,%d)", done, failed, truncated, wantFailed, wantTruncated)
	}

	plan := func(query string, args ...any) []string {
		rows, err := s.DB().Query("EXPLAIN QUERY PLAN "+query, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			out = append(out, detail)
		}
		return out
	}
	for name, q := range map[string]string{"open calls": openCallsSinceSQL, "stats": toolResultStatsSQL} {
		steps := plan(q, 1, "x")
		ranged := false
		for _, step := range steps {
			if strings.HasPrefix(step, "SCAN") {
				t.Errorf("%s scans the log:\n%s", name, strings.Join(steps, "\n"))
			}
			if strings.Contains(step, "idx_conversations_kind_seq (kind=? AND turn_seq>?)") {
				ranged = true
			}
		}
		if !ranged {
			t.Errorf("%s is not a range of idx_conversations_kind_seq:\n%s", name, strings.Join(steps, "\n"))
		}
	}
	window := plan(windowFloorSQL, "x")
	if len(window) != 1 || !strings.Contains(window[0], "COVERING INDEX idx_conversations_recorded_seq (recorded_at>?)") {
		t.Errorf("the window's floor is not a read of idx_conversations_recorded_seq alone: %q", window)
	}
}

// retiredToolEventsDDL is the table as releases through 0.1.13 declared
// it, for the benchmark's "before".
const retiredToolEventsDDL = `CREATE TABLE tool_events (execution_id TEXT PRIMARY KEY, turn_id TEXT NOT NULL, ordinal INTEGER NOT NULL,
	actor TEXT NOT NULL, model TEXT NOT NULL DEFAULT '', provider_call_id TEXT NOT NULL DEFAULT '', tool TEXT NOT NULL,
	args_record TEXT NOT NULL, state TEXT NOT NULL CHECK (state IN ('started','done','abandoned')),
	failed INTEGER NOT NULL DEFAULT 0, truncated INTEGER NOT NULL DEFAULT 0, result_record TEXT NOT NULL DEFAULT '',
	started_ms INTEGER NOT NULL, finished_ms INTEGER NOT NULL DEFAULT 0, UNIQUE (turn_id, ordinal)) STRICT;
CREATE INDEX tool_events_started_idx ON tool_events (started_ms);
CREATE INDEX tool_events_turn_idx ON tool_events (turn_id);`

// BenchmarkTheToolRecordReads is the boot's open-call read and the
// prompt's 48-hour stats at 200k lifetime calls, one every 86.4 s (1,000
// a day, so 14k in the window), two left open: through the retired table
// holding its 14 days, through the whole log, and through the log's
// windows.
func BenchmarkTheToolRecordReads(b *testing.B) {
	s := testStore(b)
	const n = 200_000
	toolLog(b, s, n, 86400*time.Millisecond, map[int]bool{n - 50: true, n - 3: true})
	if _, err := s.DB().Exec(retiredToolEventsDDL); err != nil {
		b.Fatal(err)
	}
	tx, err := s.DB().Begin()
	if err != nil {
		b.Fatal(err)
	}
	start := time.Now().Add(-unfinishedWindow).UnixMilli()
	for i := 0; i < 14_000; i++ {
		state := "done"
		if i == 13_950 || i == 13_997 {
			state = "started"
		}
		if _, err := tx.Exec(`INSERT INTO tool_events(execution_id,turn_id,ordinal,actor,tool,args_record,state,failed,truncated,started_ms) VALUES(?,?,1,'main','read','[2 chars]',?,?,?,?)`,
			fmt.Sprintf("tex_%d", i), fmt.Sprintf("turn-%d", i), state, i%10/9, i%7/6, start+int64(i)*86400); err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	count := func(b *testing.B, q string, args ...any) int {
		rows, err := s.DB().Query(q, args...)
		if err != nil {
			b.Fatal(err)
		}
		defer rows.Close()
		k := 0
		for rows.Next() {
			k++
		}
		if err := rows.Err(); err != nil {
			b.Fatal(err)
		}
		return k
	}
	from := func(b *testing.B, window time.Duration) (int64, string) {
		floor, stamp, ok, err := windowSince(s.DB(), time.Now().Add(-window))
		if err != nil || !ok {
			b.Fatal(ok, err)
		}
		return floor, stamp
	}
	b.Run("open calls/retired table", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if k := count(b, `SELECT tool,turn_id,ordinal FROM tool_events WHERE state='started' ORDER BY started_ms`); k != 2 {
				b.Fatal(k)
			}
		}
	})
	b.Run("open calls/log, unbounded", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if k := count(b, openCallsSinceSQL, 0, ""); k != 2 {
				b.Fatal(k)
			}
		}
	})
	b.Run("open calls/log, 14 days", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			floor, stamp := from(b, unfinishedWindow)
			if k := count(b, openCallsSinceSQL, floor, stamp); k != 2 {
				b.Fatal(k)
			}
		}
	})
	cutoff := time.Now().Add(-48 * time.Hour).UnixMilli()
	b.Run("stats/retired table", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			var done, failed, truncated int
			if err := s.DB().QueryRow(`SELECT COUNT(*), COALESCE(SUM(failed),0), COALESCE(SUM(truncated),0) FROM tool_events WHERE state='done' AND started_ms >= ?`, cutoff).Scan(&done, &failed, &truncated); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("stats/log, 48 hours", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			var done, failed, truncated int
			floor, stamp := from(b, 48*time.Hour)
			if err := s.DB().QueryRow(toolResultStatsSQL, floor, stamp).Scan(&done, &failed, &truncated); err != nil && !errors.Is(err, sql.ErrNoRows) {
				b.Fatal(err)
			}
			if done < 1900 {
				b.Fatal(done)
			}
		}
	})
}
