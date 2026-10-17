package store

import (
	"database/sql"
	"errors"
	"testing"
)

func TestQueuedChildProjectUsesItsRecordedParent(t *testing.T) {
	for _, tc := range []struct {
		name, parentProject, payload, want string
		terminal                           bool
	}{
		{"parent_project", "project-a", `{"parent_work_session_id":"parent"}`, "project-a", false},
		{"terminal_parent", "project-a", `{"parent_work_session_id":"parent"}`, "project-a", true},
		{"parent_without_project", "", `{"parent_work_session_id":"parent"}`, "", false},
		{"legacy_omitted_parent", "project-a", `{}`, "project-b", false},
		{"legacy_empty_parent", "project-a", `{"parent_work_session_id":""}`, "project-b", false},
		{"legacy_null_parent", "project-a", `{"parent_work_session_id":null}`, "project-b", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := wqStore(t)
			if err := s.SetActiveProject(tc.parentProject); err != nil {
				t.Fatal(err)
			}
			if err := s.StartWorkSession(t.Context(), "parent", "parent task"); err != nil {
				t.Fatal(err)
			}
			if tc.terminal {
				if err := s.DeliverWorkSession(t.Context(), "parent", "served: parent work completed", EvidenceCompletedLocally, ""); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.SetActiveProject("project-b"); err != nil {
				t.Fatal(err)
			}
			item := &WorkItem{Kind: SubagentWorkKind, Payload: tc.payload, DedupKey: "child", Source: "identity"}
			if live, added, err := s.EnqueueWorkWithSessionBelowLimit(item, 2, "child", SubagentDescription("child task")); err != nil || !added || live != 0 {
				t.Fatalf("enqueue: live=%d added=%v err=%v", live, added, err)
			}
			child, err := s.WorkSessionByID("child")
			if err != nil || child == nil || child.Project != tc.want {
				t.Fatalf("child=%+v err=%v want project=%q", child, err, tc.want)
			}
			var noticeProject string
			if err := s.DB().QueryRow(`SELECT project_id FROM conversations WHERE json_extract(details,'$.work_session_id')='child'`).Scan(&noticeProject); err != nil {
				t.Fatal(err)
			}
			if noticeProject != tc.want {
				t.Fatalf("notice project=%q want=%q", noticeProject, tc.want)
			}
			if s.ActiveProjectID() != "project-b" {
				t.Fatal("enqueue changed project focus")
			}
		})
	}
}

func TestQueuedChildParentRefusalAndRecordFailureAreAtomic(t *testing.T) {
	for _, tc := range []struct {
		name, payload          string
		missing, recordFailure bool
	}{
		{"missing_parent", `{"parent_work_session_id":"missing"}`, true, false},
		{"malformed_json", `{"parent_work_session_id":`, false, false},
		{"numeric_parent", `{"parent_work_session_id":123}`, false, false},
		{"boolean_parent", `{"parent_work_session_id":true}`, false, false},
		{"object_parent", `{"parent_work_session_id":{}}`, false, false},
		{"array_parent", `{"parent_work_session_id":[]}`, false, false},
		{"record_failure", `{"parent_work_session_id":"parent"}`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := wqStore(t)
			if err := s.SetActiveProject("project-a"); err != nil {
				t.Fatal(err)
			}
			if err := s.StartWorkSession(t.Context(), "parent", "parent task"); err != nil {
				t.Fatal(err)
			}
			if err := s.SetActiveProject("project-b"); err != nil {
				t.Fatal(err)
			}
			counts := func() [3]int {
				t.Helper()
				var got [3]int
				for i, table := range []string{"work_queue", "work_sessions", "conversations"} {
					if err := s.DB().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&got[i]); err != nil {
						t.Fatal(err)
					}
				}
				return got
			}
			before := counts()
			if tc.recordFailure {
				if _, err := s.DB().Exec(`CREATE TRIGGER refuse_work_notice BEFORE INSERT ON conversations BEGIN SELECT RAISE(ABORT, 'synthetic work notice failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			item := &WorkItem{Kind: SubagentWorkKind, Payload: tc.payload, DedupKey: "child", Source: "identity"}
			live, added, err := s.EnqueueWorkWithSessionBelowLimit(item, 2, "child", SubagentDescription("child task"))
			if err == nil || added || live != 0 {
				t.Fatalf("refusal: live=%d added=%v err=%v", live, added, err)
			}
			if tc.missing && !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("missing parent cause lost: %v", err)
			}
			if after := counts(); after != before {
				t.Fatalf("failed enqueue wrote state: before=%v after=%v", before, after)
			}
		})
	}
}

func TestQueuedChildCeilingAndGenericPayloadKeepTheirContracts(t *testing.T) {
	s := wqStore(t)
	if err := s.SetActiveProject("project-b"); err != nil {
		t.Fatal(err)
	}
	// The generic queue does not interpret another handler's payload.
	if _, added, err := s.EnqueueWorkWithSessionBelowLimit(&WorkItem{Kind: "fixture.work", Payload: `{"parent_work_session_id":123}`, DedupKey: "generic", Source: "fixture"}, 2, "generic", "generic work"); err != nil || !added {
		t.Fatalf("generic admission: added=%v err=%v", added, err)
	}
	if _, added, err := s.EnqueueWorkWithSessionBelowLimit(&WorkItem{Kind: "fixture.work", Payload: "not JSON", DedupKey: "malformed", Source: "fixture"}, 2, "malformed", "malformed work"); err == nil || added {
		t.Fatalf("existing JSON constraint lost: added=%v err=%v", added, err)
	}
	row, err := s.WorkSessionByID("generic")
	if err != nil || row == nil || row.Project != "project-b" {
		t.Fatalf("generic scope=%+v err=%v", row, err)
	}
	// A full queue keeps its established no-effect/no-error ceiling result.
	if _, added, err := s.EnqueueWorkWithSessionBelowLimit(&WorkItem{Kind: SubagentWorkKind, Payload: `{}`, DedupKey: "existing", Source: "identity"}, 1, "existing", SubagentDescription("existing")); err != nil || !added {
		t.Fatalf("first admission: %v %v", added, err)
	}
	item := &WorkItem{Kind: SubagentWorkKind, Payload: `{"parent_work_session_id":"missing"}`, DedupKey: "over-limit", Source: "identity"}
	if live, added, err := s.EnqueueWorkWithSessionBelowLimit(item, 1, "over-limit", SubagentDescription("extra")); err != nil || added || live != 1 {
		t.Fatalf("ceiling: live=%d added=%v err=%v", live, added, err)
	}
	var n int
	if err := s.DB().QueryRow(`SELECT (SELECT COUNT(*) FROM work_sessions WHERE id='over-limit') + (SELECT COUNT(*) FROM work_queue WHERE dedup_key='over-limit')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("ceiling wrote rows=%d err=%v", n, err)
	}
}
