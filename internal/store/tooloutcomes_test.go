package store

import (
	"database/sql"
	"errors"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store/rows"
)

func toolTiedSessions(t *testing.T, s *Store, count int) (time.Time, int64) {
	t.Helper()
	now := time.Now()
	tie := now.Add(-time.Second).UnixMilli()
	// Insertion order deliberately differs from stable-id order.
	for n := count - 1; n >= 0; n-- {
		id := fmt.Sprintf("ws_tie_%03d", n)
		if err := s.StartWorkSession(t.Context(), id, id); err != nil {
			t.Fatal(err)
		}
		if err := s.RecordToolStart(id, 1, "main", "m", "", "read", "{}"); err != nil {
			t.Fatal(err)
		}
		if err := s.RecordToolDoneMeasured(id, 1, "read", "{}", "missing", interaction.Failed, false, ToolMeasurement{ReasonCode: "NOT_FOUND"}); err != nil {
			t.Fatal(err)
		}
		if err := s.DeliverWorkSession(t.Context(), id, "unserved: missing", "", ""); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB().Exec(`UPDATE work_sessions SET delivered_at=?, started_at=? WHERE id=?`, tie, tie-1000, id); err != nil {
			t.Fatal(err)
		}
	}
	return now, tie
}

func TestToolOutcomeTiesStayBoundedAndLooseCallsAppearOnce(t *testing.T) {
	for _, cap := range []int{1, 8} {
		t.Run(fmt.Sprint(cap), func(t *testing.T) {
			s := testStore(t)
			now, tie := toolTiedSessions(t, s, 19)
			if err := s.RecordToolStart("loose", 1, "main", "m", "", "read", "{}"); err != nil {
				t.Fatal(err)
			}
			if err := s.RecordToolDoneMeasured("loose", 1, "read", "{}", "ok", interaction.Succeeded, false, ToolMeasurement{}); err != nil {
				t.Fatal(err)
			}
			stamp, err := interaction.NormalizeTime(time.UnixMilli(tie))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.DB().Exec(`UPDATE conversations SET recorded_at=? WHERE turn_id='loose'`, stamp); err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{}
			loose := 0
			for page := 0; page < 25; page++ {
				b, err := s.ToolOutcomesSince(now, cap, time.Hour)
				if err != nil {
					t.Fatal(err)
				}
				if len(b.Sessions) > cap {
					t.Fatalf("page exceeds admitted bound: %d > %d", len(b.Sessions), cap)
				}
				for _, session := range b.Sessions {
					if seen[session.ID] {
						t.Fatalf("session repeated: %s", session.ID)
					}
					if len(session.Tools) != 1 || session.Tools[0].NonSuccess() != 1 {
						t.Fatalf("session calls missing: %+v", session)
					}
					seen[session.ID] = true
				}
				for _, use := range b.Loose {
					loose += use.Succeeded
				}
				if b.ThroughAfter != "" && (b.Through != tie-1 || len(b.Loose) != 0) {
					t.Fatalf("partial millisecond was consumed: %+v", b)
				}
				if err := s.PublishToolOutcomeCursor(b); err != nil {
					t.Fatal(err)
				}
				if len(b.Sessions) == 0 {
					break
				}
			}
			if len(seen) != 19 || loose != 1 {
				t.Fatalf("lost/repeated material: sessions=%d loose=%d", len(seen), loose)
			}
		})
	}
}

func TestToolOutcomeBoundarySurvivesReopen(t *testing.T) {
	s := testStore(t)
	now, _ := toolTiedSessions(t, s, 10)
	first, err := s.ToolOutcomesSince(now, 8, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PublishToolOutcomeCursor(first); err != nil {
		t.Fatal(err)
	}
	var sequence int
	var name, path string
	if err := s.DB().QueryRow(`PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	s.db = db
	next, err := s.ToolOutcomesSince(now, 8, time.Hour)
	if err != nil || len(next.Sessions) != 2 || next.Sessions[0].ID != "ws_tie_008" {
		t.Fatalf("reopen lost continuation: %+v, %v", next, err)
	}
}

func TestToolOutcomeReadWaitsForDeliveryMillisecondToClose(t *testing.T) {
	s := testStore(t)
	now := time.Now().Truncate(time.Millisecond)
	deliver := func(id string) {
		t.Helper()
		if err := s.StartWorkSession(t.Context(), id, "same millisecond"); err != nil {
			t.Fatal(err)
		}
		if err := s.DeliverWorkSession(t.Context(), id, "served: done", "", ""); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB().Exec(`UPDATE work_sessions SET delivered_at=? WHERE id=?`, now.UnixMilli(), id); err != nil {
			t.Fatal(err)
		}
	}
	deliver("ws_first")
	first, err := s.ToolOutcomesSince(now, 8, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Sessions) != 0 || first.Through >= now.UnixMilli() {
		t.Fatalf("read consumed an open delivery millisecond: %+v", first)
	}
	if err := s.PublishToolOutcomeCursor(first); err != nil {
		t.Fatal(err)
	}
	deliver("ws_later")
	next, err := s.ToolOutcomesSince(now.Add(time.Millisecond), 8, time.Hour)
	if err != nil || len(next.Sessions) != 2 || next.Sessions[0].ID != "ws_first" || next.Sessions[1].ID != "ws_later" {
		t.Fatalf("later arrival in same millisecond was skipped: %+v, %v", next, err)
	}
}

func TestToolOutcomeBoundaryCASAndOldReaderProgress(t *testing.T) {
	s := testStore(t)
	now, tie := toolTiedSessions(t, s, 10)
	stale, err := s.ToolOutcomesSince(now, 1, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := s.ToolOutcomesSince(now, 8, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PublishToolOutcomeCursor(newer); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishToolOutcomeCursor(stale); !errors.Is(err, ErrCursorMoved) {
		t.Fatalf("stale cursor won: %v", err)
	}
	// An old binary understands only this integer and leaves the companion.
	if err := setRuntimeMeta(s.w(), ToolOutcomeCursorKey, fmt.Sprint(tie)); err != nil {
		t.Fatal(err)
	}
	next, err := s.ToolOutcomesSince(now, 8, time.Hour)
	if err != nil || len(next.Sessions) != 0 || next.FromAfter != "" {
		t.Fatalf("stale companion overrode old-reader progress: %+v, %v", next, err)
	}
	if err := setRuntimeMeta(s.w(), toolOutcomeBoundaryKey, "malformed"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ToolOutcomesSince(now, 8, time.Hour); !errors.Is(err, ErrToolOutcomeBoundary) {
		t.Fatalf("malformed continuation guessed through: %v", err)
	}
}

func TestToolOutcomeBoundaryPublicationIsAtomic(t *testing.T) {
	s := testStore(t)
	now, _ := toolTiedSessions(t, s, 10)
	b, err := s.ToolOutcomesSince(now, 8, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`CREATE TRIGGER fail_tool_boundary BEFORE INSERT ON runtime_meta WHEN NEW.key='consolidate.tool_outcomes_boundary' BEGIN SELECT RAISE(ABORT,'injected boundary failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishToolOutcomeCursor(b); err == nil {
		t.Fatal("injected companion failure did not refuse publication")
	}
	_, _, raw, err := s.toolCursorStateLocked()
	if err != nil || raw != b.CursorRaw {
		t.Fatalf("partial publication moved the integer: %q, %v", raw, err)
	}
}

func TestToolOutcomeConcurrentBoundaryPublicationHasOneWinner(t *testing.T) {
	s := testStore(t)
	now, _ := toolTiedSessions(t, s, 10)
	first, err := s.ToolOutcomesSince(now, 1, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.ToolOutcomesSince(now, 8, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, b := range []rows.ToolOutcomeBatch{first, second} {
		go func() { <-start; results <- s.PublishToolOutcomeCursor(b) }()
	}
	close(start)
	winners, stale := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			winners++
		} else if errors.Is(err, ErrCursorMoved) {
			stale++
		} else {
			t.Fatal(err)
		}
	}
	if winners != 1 || stale != 1 {
		t.Fatalf("cursor winners=%d stale=%d", winners, stale)
	}
}

func TestToolObservationRecoversExactBoundaryAfterStaleLanding(t *testing.T) {
	s, mat := reportStore(t)
	now, _ := toolTiedSessions(t, s, 19)
	old, err := s.ToolOutcomesSince(now, 1, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.ToolOutcomesSince(now, 8, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	land := func(b rows.ToolOutcomeBatch, hash string) {
		mat(ledger.EventExperienceCreate, 3, map[string]any{"id": b.ObservationID(hash), "content": "tools observed", "category": "observation", "provenance": "self"})
	}
	land(first, "first")
	if err := s.PublishToolOutcomeCursor(first); err != nil {
		t.Fatal(err)
	}
	second, err := s.ToolOutcomesSince(now, 8, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	land(second, "second")
	if err := s.PublishToolOutcomeCursor(second); err != nil {
		t.Fatal(err)
	}
	// A stale product can land before its CAS loses. Its newer ledger
	// sequence must not undo the farther observed boundary during recovery.
	land(old, "stale")
	if err := s.PublishToolOutcomeCursor(old); !errors.Is(err, ErrCursorMoved) {
		t.Fatalf("stale landing cursor: %v", err)
	}
	if _, err := s.DB().Exec(`DELETE FROM runtime_meta WHERE key IN (?,?)`, ToolOutcomeCursorKey, toolOutcomeBoundaryKey); err != nil {
		t.Fatal(err)
	}
	next, err := s.ToolOutcomesSince(now, 8, time.Hour)
	if err != nil || len(next.Sessions) != 3 || next.Sessions[0].ID != "ws_tie_016" {
		t.Fatalf("observation did not recover furthest boundary: %+v, %v", next, err)
	}
	if next.Last == nil || next.Last.FromAfter != "ws_tie_007" || next.Last.ThroughAfter != "ws_tie_015" {
		t.Fatalf("last exact window lost: %+v", next.Last)
	}
	if len(next.Since) != 1 || next.Since[0].Then != 8 || next.Since[0].Now != 3 {
		t.Fatalf("last page counts include other tied sessions: %+v", next.Since)
	}
}

func TestToolObservationBoundaryFormatKeepsLegacyFields(t *testing.T) {
	b := rows.ToolOutcomeBatch{From: 100, Through: 100, FromAfter: "ws_001", ThroughAfter: "ws_008"}
	id := b.ObservationID("abcd")
	o, ok := rows.ParseToolObservationID(id)
	if !ok || o.From != 100 || o.Through != 100 || o.FromAfter != b.FromAfter || o.ThroughAfter != b.ThroughAfter {
		t.Fatalf("partial observation: %s %+v %v", id, o, ok)
	}
	if got := (rows.ToolOutcomeBatch{From: 100, Through: 200}).ObservationID("abcd"); got != rows.ToolObservationID(100, 200, nil, "abcd") {
		t.Fatalf("legacy id changed: %s", got)
	}
	for _, tail := range []string{"b1-zz-00-h", "b1-01-h", "b1-ff-00-h"} {
		if _, ok := rows.ParseToolObservationID(rows.ToolObservationID(100, 100, nil, tail)); ok {
			t.Fatalf("malformed/backward boundary accepted: %s", tail)
		}
	}
}

// Diagnostic measurement, not a time threshold: the compatibility repair
// scans only this indexed prefix and retains one candidate while finding
// the furthest observed boundary, even if a stale product landed later.
func TestToolObservationReservedRangeReadCost(t *testing.T) {
	for _, count := range []int{1000, 10000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			s, _ := reportStore(t)
			tx, err := s.DB().Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			stmt, err := tx.Prepare(`INSERT INTO experiences(id,content,category,provenance,created_seq,created_at) VALUES (?,'synthetic','observation','self',1,'2026-10-01T00:00:00Z')`)
			if err != nil {
				t.Fatal(err)
			}
			for n := 0; n < count; n++ {
				b := rows.ToolOutcomeBatch{From: 100, Through: 100, ThroughAfter: fmt.Sprintf("ws_%08d", n)}
				if _, err := stmt.Exec(b.ObservationID("cost")); err != nil {
					t.Fatal(err)
				}
			}
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			start := time.Now()
			o, ok, err := s.NewestToolObservation()
			elapsed := time.Since(start)
			runtime.ReadMemStats(&after)
			if err != nil || !ok || o.ThroughAfter != fmt.Sprintf("ws_%08d", count-1) {
				t.Fatalf("range maximum: %+v %v %v", o, ok, err)
			}
			t.Logf("reserved observations=%d duration=%s allocated_bytes=%d", count, elapsed, after.TotalAlloc-before.TotalAlloc)
		})
	}
}

func TestOneRecurringToolPairIsOneOrigin(t *testing.T) {
	wall := rows.ToolPairKey("shell", "refused", "SHELL_WALL")
	for _, tc := range []struct {
		name    string
		pairs   [3][]string
		origins int
		verdict string
	}{
		{"one pair, three windows", [3][]string{{wall}, {wall}, {wall}}, 1, "new"},
		{"three pairs", [3][]string{{wall}, {rows.ToolPairKey("read", "failed", "NOT_FOUND")}, {rows.ToolPairKey("edit", "failed", "EDIT_NO_MATCH")}}, 3, "confirmed"},
		{"pairs shared through a third", [3][]string{{wall}, {wall, rows.ToolPairKey("read", "failed", "")}, {rows.ToolPairKey("read", "failed", "")}}, 1, "new"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, mat := reportStore(t)
			for i, pairs := range tc.pairs {
				id := rows.ToolObservationID(int64(i*10), int64(i*10+5), pairs, "abcd")
				mat(ledger.EventExperienceCreate, 3, map[string]any{"id": id, "content": "an observation of my tool calls", "category": "observation", "provenance": "self"})
				mat(ledger.EventEdgeCreate, 3, map[string]any{"id": "e" + string(rune('0'+i)), "from_id": id, "to_id": "claim", "edge_type": "SUPPORTS"})
			}
			r, err := s.StandingReportFor("claim")
			if err != nil {
				t.Fatal(err)
			}
			if r.DistinctSources != 3 || r.DistinctOrigins != tc.origins || r.Standing != tc.verdict {
				t.Fatalf("%d source(s) on %d origin(s), %q; want 3 on %d, %q", r.DistinctSources, r.DistinctOrigins, r.Standing, tc.origins, tc.verdict)
			}
		})
	}

	// An id that names more pairs than it can is joined to every tool
	// observation: what it left out cannot count twice.
	var many []string
	for i := 0; i <= rows.ToolObservationPairsMax; i++ {
		many = append(many, rows.ToolPairKey("tool", "failed", string(rune('a'+i))))
	}
	s, mat := reportStore(t)
	ids := []string{
		rows.ToolObservationID(0, 5, many, "abcd"),
		rows.ToolObservationID(10, 15, []string{rows.ToolPairKey("other", "failed", "")}, "abcd"),
		rows.ToolObservationID(20, 25, []string{rows.ToolPairKey("third", "failed", "")}, "abcd"),
	}
	if o, ok := rows.ParseToolObservationID(ids[0]); !ok || !o.Many || len(o.Pairs) != rows.ToolObservationPairsMax {
		t.Fatalf("an id over the pair bound reads as %+v %v", o, ok)
	}
	for i, id := range ids {
		mat(ledger.EventExperienceCreate, 3, map[string]any{"id": id, "content": "an observation", "category": "observation", "provenance": "self"})
		mat(ledger.EventEdgeCreate, 3, map[string]any{"id": "m" + string(rune('0'+i)), "from_id": id, "to_id": "claim", "edge_type": "SUPPORTS"})
	}
	r, err := s.StandingReportFor("claim")
	if err != nil {
		t.Fatal(err)
	}
	if r.DistinctOrigins != 1 {
		t.Fatalf("an observation that names more pairs than it can left %d origins, want 1", r.DistinctOrigins)
	}
}

// A tool observation's id carries its window and its pairs, read back
// whole; anything else is not one.
func TestAToolObservationIDCarriesItsWindowAndPairs(t *testing.T) {
	id := rows.ToolObservationID(1761580800000, 1761584400000, []string{"b", "a", "a"}, "0123456789abcdef")
	o, ok := rows.ParseToolObservationID(id)
	if !ok || o.From != 1761580800000 || o.Through != 1761584400000 || len(o.Pairs) != 2 || o.Pairs[0] != "a" || o.Many {
		t.Fatalf("%s reads as %+v %v", id, o, ok)
	}
	for _, bad := range []string{"exp_outcome_abc", rows.ToolObservationPrefix + "x_y_z_w", rows.ToolObservationPrefix + "0000000000009_0000000000001_a_t"} {
		if _, ok := rows.ParseToolObservationID(bad); ok {
			t.Errorf("%q read as a tool observation", bad)
		}
	}
}
