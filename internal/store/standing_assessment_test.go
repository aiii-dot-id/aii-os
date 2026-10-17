package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// Real belief-upsert origins share one ledger sequence. This fixture guards
// the case a sequence-only cursor skipped while avoiding raw edge inserts.
func assessmentFixture(t *testing.T, n int) (*Store, func(ledger.EventType, int, map[string]any)) {
	t.Helper()
	s, mat := reportStore(t)
	refs := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("origin_%02d", i)
		provenance := "self"
		if i == 0 {
			provenance = "external"
		}
		payload := map[string]any{"id": id, "content": id, "provenance": provenance}
		if provenance == "external" {
			payload["source_url"] = "https://example.org/standing-test"
		}
		mat(ledger.EventExperienceCreate, 3, payload)
		refs = append(refs, map[string]any{"kind": "experience", "id": id})
	}
	mat(ledger.EventBeliefUpsert, 3, map[string]any{
		"id": "claim", "statement": "claim", "origin_refs": refs,
	})
	return s, mat
}

func assessmentOnly(r *StandingReport) StandingReport {
	copy := *r
	copy.Qualifying, copy.Excluded = nil, nil
	copy.NextDetailSeq, copy.NextDetailEdgeID = 0, ""
	return copy
}

func TestStandingAssessmentIsCursorInvariantAndDetailIsOptional(t *testing.T) {
	for _, contradiction := range []bool{false, true} {
		t.Run(fmt.Sprintf("contradiction=%v", contradiction), func(t *testing.T) {
			s, mat := assessmentFixture(t, 15)
			if contradiction {
				// Newer than all origins and beyond the first detail page.
				mat(ledger.EventEdgeCreate, 3, map[string]any{
					"id": "late_contradiction", "from_id": "exp4", "to_id": "claim", "edge_type": "CONTRADICTS",
				})
			}
			base, err := s.StandingReportFor("claim")
			if err != nil {
				t.Fatal(err)
			}
			wantStanding := "confirmed"
			if contradiction {
				wantStanding = "suspect"
			}
			if base.Standing != wantStanding || base.Eligible == contradiction || base.DistinctSources != 15 || base.QualifyingTotal != 15 {
				t.Fatalf("incomplete assessment: %+v", base)
			}
			if len(base.Qualifying)+len(base.Excluded) != 0 || base.NextDetailSeq != 0 || base.NextDetailEdgeID != "" {
				t.Fatalf("assessment-only path collected optional detail: %+v", base)
			}
			if got := mustStanding(t, s, "claim"); got != base.Standing {
				t.Fatalf("fast path=%s report=%s", got, base.Standing)
			}

			seen := map[string]bool{}
			cur := &StandingCursor{}
			for page := 0; ; page++ {
				if page > 3 {
					t.Fatal("continuation did not terminate")
				}
				r, err := s.StandingDetail(t.Context(), "claim", cur)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(assessmentOnly(r), assessmentOnly(base)) {
					t.Fatalf("page %d changed assessment at the same head: base=%+v page=%+v", page, base, r)
				}
				detail := append(append([]ReportEdge{}, r.Qualifying...), r.Excluded...)
				if len(detail) > StandingDetailMax {
					t.Fatalf("page returned %d edges, bound=%d", len(detail), StandingDetailMax)
				}
				for _, e := range detail {
					if seen[e.Edge.ID] {
						t.Fatalf("edge %q served twice", e.Edge.ID)
					}
					seen[e.Edge.ID] = true
				}
				if r.NextDetailEdgeID == "" {
					break
				}
				next := &StandingCursor{Seq: r.NextDetailSeq, EdgeID: r.NextDetailEdgeID}
				if next.Seq < cur.Seq || (next.Seq == cur.Seq && next.EdgeID <= cur.EdgeID) {
					t.Fatalf("cursor did not advance: %v -> %v", cur, next)
				}
				cur = next
			}
			wantEdges := 15
			if contradiction {
				wantEdges++
			}
			if len(seen) != wantEdges {
				t.Fatalf("walk reached %d/%d edges", len(seen), wantEdges)
			}
			// An exhausted cursor must still yield the WHOLE assessment.
			empty, err := s.StandingDetail(t.Context(), "claim", &StandingCursor{Seq: base.ReadHeadSeq + 1, EdgeID: "past_end"})
			if err != nil || !reflect.DeepEqual(assessmentOnly(empty), assessmentOnly(base)) || len(empty.Qualifying)+len(empty.Excluded) != 0 {
				t.Fatalf("cursor past end changed assessment: %+v, %v", empty, err)
			}
		})
	}
}

func TestStandingAssessmentRequiredReadsFailClosedAfterFullPage(t *testing.T) {
	cases := []struct{ name, sql, message string }{
		{"retired state", "ALTER TABLE beliefs RENAME COLUMN archived TO archived_unreadable", "retired state"},
		{"provenance", "ALTER TABLE experiences RENAME COLUMN provenance TO provenance_unreadable", "provenance"},
		{"lifetime", "ALTER TABLE identity_lifetime RENAME COLUMN lifetime_ticks TO ticks_unreadable", "lifetime ticks"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := assessmentFixture(t, 15)
			if _, err := s.db.Exec(tc.sql); err != nil {
				t.Fatal(err)
			}
			for _, cur := range []*StandingCursor{nil, {}, {Seq: 1, EdgeID: "edge"}} {
				r, err := s.StandingDetail(t.Context(), "claim", cur)
				if err == nil || r != nil || !strings.Contains(err.Error(), tc.message) {
					t.Fatalf("required %s read certified a partial report: %+v err=%v", tc.name, r, err)
				}
			}
			if standing, err := s.StandingFor("claim"); err == nil || standing != "" {
				t.Fatalf("fast path certified unreadable metadata: %q %v", standing, err)
			}
		})
	}
	t.Run("provenance below three sources", func(t *testing.T) {
		s, _ := assessmentFixture(t, 1)
		if _, err := s.db.Exec("ALTER TABLE experiences RENAME COLUMN provenance TO provenance_unreadable"); err != nil {
			t.Fatal(err)
		}
		if r, err := s.StandingReportFor("claim"); err == nil || r != nil || !strings.Contains(err.Error(), "provenance") {
			t.Fatalf("below-threshold provenance failure was swallowed: %+v %v", r, err)
		}
	})
}

// Hook precisely between the edges and head reads. A real writer commits
// while the read transaction is pinned, so the derivation must retain the
// earlier head and graph. No sleeps or probabilistic scheduling are needed.
type standingInterleavedReader struct {
	standingReader
	beforeHead func()
}

func (r standingInterleavedReader) QueryRow(query string, args ...any) *sql.Row {
	if query == standingHeadSQL {
		r.beforeHead()
	}
	return r.standingReader.QueryRow(query, args...)
}

func TestStandingAssessmentSnapshotPinsHeadAndEvidence(t *testing.T) {
	s, mat := reportStore(t)
	before, err := s.StandingReportFor("claim")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.beginReadSnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if tx != nil {
			_ = tx.Close()
		}
	}()
	interleaved := standingInterleavedReader{
		standingReader: boundReader{ctx: t.Context(), s: tx},
		beforeHead: func() {
			mat(ledger.EventEdgeCreate, 3, map[string]any{
				"id": "concurrent_support", "from_id": "exp", "to_id": "claim", "edge_type": "SUPPORTS",
			})
		},
	}
	r, err := s.standingReportRead(interleaved, "claim", &StandingCursor{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if r.ReadHeadSeq != before.ReadHeadSeq || r.DistinctSources != 0 || r.QualifyingTotal != 0 {
		t.Fatalf("snapshot mixed old graph with new head: before=%+v during=%+v", before, r)
	}
	if err := tx.Close(); err != nil {
		t.Fatal(err)
	}
	// Prevent a second Close of this transaction; it has restored its connection.
	tx = nil
	after, err := s.StandingDetail(t.Context(), "claim", &StandingCursor{})
	if err != nil || after.ReadHeadSeq != before.ReadHeadSeq+1 || after.DistinctSources != 1 || after.QualifyingTotal != 1 {
		t.Fatalf("next snapshot did not see the committed graph: %+v %v", after, err)
	}
}

func TestStandingAssessmentPublicSnapshotsRemainCoherentDuringWrites(t *testing.T) {
	s, mat := reportStore(t)
	const writes = 24
	for i := 0; i < writes; i++ {
		id := fmt.Sprintf("concurrent_origin_%02d", i)
		mat(ledger.EventExperienceCreate, 3, map[string]any{
			"id": id, "content": id, "provenance": "self",
		})
	}
	head, err := s.MaxLedgerSeq()
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		<-start
		for i := 0; i < writes; i++ {
			body, err := json.Marshal(map[string]any{
				"id": fmt.Sprintf("concurrent_edge_%02d", i), "from_id": fmt.Sprintf("concurrent_origin_%02d", i), "to_id": "claim", "edge_type": "SUPPORTS",
			})
			if err == nil {
				err = s.Materialize(&ledger.Event{Seq: head + uint64(i) + 1, Type: ledger.EventEdgeCreate, Ring: 3,
					Timestamp: "2026-09-23T00:00:00Z", Payload: body})
			}
			if err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	close(start)
	var readErr error
	for i := 0; i < 48; i++ {
		r, err := s.StandingDetail(t.Context(), "claim", &StandingCursor{})
		if err != nil {
			readErr = err
			break
		}
		n := int(r.ReadHeadSeq - head)
		if r.ReadHeadSeq < head || n > writes || r.DistinctSources != n || r.QualifyingTotal != n {
			readErr = fmt.Errorf("head and graph disagree: base=%d report=%+v", head, r)
			break
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if readErr != nil {
		t.Fatal(readErr)
	}
	r, err := s.StandingReportFor("claim")
	if err != nil || r.ReadHeadSeq != head+writes || r.DistinctSources != writes {
		t.Fatalf("final committed assessment: %+v %v", r, err)
	}
}

func TestStandingAssessmentCancelledContextDoesNotReturnReport(t *testing.T) {
	s, _ := reportStore(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if r, err := s.StandingDetail(ctx, "claim", &StandingCursor{}); err == nil || r != nil {
		t.Fatalf("cancelled snapshot returned a report: %+v %v", r, err)
	}
}
