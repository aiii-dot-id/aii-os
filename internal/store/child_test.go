package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// queueChild gives session id the row a spawn gives it, the run's, keyed by
// the session id, in state (DONE once its run has ended). That row, not the
// words of the description, is what makes the session a child.
func queueChild(t testing.TB, s *Store, id, state string) {
	t.Helper()
	if _, err := s.DB().Exec(`INSERT INTO work_queue (id, kind, dedup_key, state) VALUES (?, ?, ?, ?)`, "q_"+id, SubagentWorkKind, id, state); err != nil {
		t.Fatal(err)
	}
}

func TestTheQueueNotTheWordsSaysWhoIsAChild(t *testing.T) {
	readers := []string{"main seat", "tool-start seat", "standing plan", "live children", "unharvested", "recent delivered", "boundary actor", "notice actor", "sweep note", "by id", "by project"}
	for _, c := range []struct {
		name, description string
		queued            bool
		want              string
	}{
		{"queued child", SubagentDescription("tidy notes"), true, "child"},
		{"the words alone", SubagentDescription("tidy notes"), false, "main"},
		{"ordinary", "tidy notes", false, "main"},
		{"queued under plain words", "tidy notes", true, "child"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := classifySession(t, c.description, c.queued)
			for _, r := range readers {
				if got[r] != c.want {
					t.Errorf("%s: %s, want %s", r, got[r], c.want)
				}
			}
		})
	}
}

// classifySession makes one session X in a fresh store, the way its case
// says, and asks each reader what X is.
func classifySession(t *testing.T, description string, queued bool) map[string]string {
	const x = "ws_x"
	class := func(child bool) string {
		if child {
			return "child"
		}
		return "main"
	}
	listed := func(list []WorkSession, err error) bool {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		for _, ws := range list {
			if ws.ID == x {
				return true
			}
		}
		return false
	}
	start := func(s *Store) {
		t.Helper()
		if queued {
			// As a spawn does: the row and the session in one transaction.
			if _, kept, err := s.EnqueueWorkWithSessionBelowLimit(&WorkItem{Kind: SubagentWorkKind, Payload: "{}", DedupKey: x, Source: "identity"}, 4, x, description); err != nil || !kept {
				t.Fatalf("spawn: kept=%t err=%v", kept, err)
			}
			// Its run has ended, so its row no longer holds it live.
			item, err := s.ClaimWork([]string{SubagentWorkKind}, time.Now().UnixMilli())
			if err != nil || item == nil || item.DedupKey != x {
				t.Fatalf("claim: %+v %v", item, err)
			}
			if err := s.CompleteWork(item.ID); err != nil {
				t.Fatal(err)
			}
			return
		}
		if err := s.StartWorkSession(t.Context(), x, description); err != nil {
			t.Fatal(err)
		}
	}
	got := map[string]string{}

	s := testStore(t)
	var actors []string
	s.SetWorkObserver(func(ev WorkEvent) {
		if ev.ID == x {
			actors = append(actors, ev.Actor)
		}
	})
	start(s)
	focus := "tidy"
	if err := s.UpdateWorkPlan(t.Context(), x, &focus, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	ws, err := s.ActiveWorkSession()
	if err != nil {
		t.Fatal(err)
	}
	got["main seat"] = class(ws == nil || ws.ID != x)
	if err := s.RecordToolStart("turn_x", 1, "main", "m", "", "ls", "{}"); err != nil {
		t.Fatal(err)
	}
	var seat string
	if err := s.DB().QueryRow(`SELECT COALESCE(json_extract(details,'$.work_session_id'),'') FROM conversations WHERE kind='tool_call'`).Scan(&seat); err != nil {
		t.Fatal(err)
	}
	got["tool-start seat"] = class(seat != x)
	plan, ok, err := s.RecentPlan()
	if err != nil {
		t.Fatal(err)
	}
	got["standing plan"] = class(!ok || plan.ID != x)
	got["live children"] = class(listed(s.LiveSubagentSessions()))
	byID, err := s.WorkSessionByID(x)
	if err != nil || byID == nil {
		t.Fatalf("by id: %+v %v", byID, err)
	}
	got["by id"] = class(byID.Child)
	byProject, _, err := s.WorkSessionsByProject("", 8)
	if err != nil || len(byProject) != 1 {
		t.Fatalf("by project: %+v %v", byProject, err)
	}
	got["by project"] = class(byProject[0].Child)
	if err := s.DeliverWorkSession(t.Context(), x, "served: tidied", EvidenceWorkerReportOnly, ""); err != nil {
		t.Fatal(err)
	}
	got["unharvested"] = class(listed(s.UnharvestedDeliveries(8)))
	got["recent delivered"] = class(listed(s.RecentDeliveredSubagents(8)))
	if err := s.MarkHarvested(x, 1); err != nil {
		t.Fatal(err)
	}
	// Every boundary X crossed names one actor.
	if len(actors) < 2 {
		t.Fatalf("boundaries heard: %v", actors)
	}
	for _, a := range actors[1:] {
		if a != actors[0] {
			t.Fatalf("one session, two actors: %v", actors)
		}
	}
	got["boundary actor"] = map[string]string{"subagent": "child", "main": "main"}[actors[0]]
	rows, err := s.DB().Query(`SELECT DISTINCT json_extract(details,'$.actor') FROM conversations WHERE kind='notice' AND json_extract(details,'$.work_session_id')=?`, x)
	if err != nil {
		t.Fatal(err)
	}
	var notices []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatal(err)
		}
		notices = append(notices, a)
	}
	rows.Close()
	if len(notices) != 1 {
		t.Fatalf("X's work notices name actors %v, want one", notices)
	}
	got["notice actor"] = map[string]string{"subagent": "child", "main": "main"}[notices[0]]

	// The boot sweep closes X (active, nothing live in the queue), and its
	// note says X only when no harvest will.
	s2 := testStore(t)
	start(s2)
	swept, err := s2.SweepOrphanWorkSessions()
	if err != nil || len(swept) != 1 || swept[0].ID != x {
		t.Fatalf("sweep = %+v, %v", swept, err)
	}
	got["sweep note"] = class(!strings.Contains(SweptSessionNote(swept), x))
	return got
}

// THE READ-ONLY MOUNT'S OLD PROJECTION HAS NO QUEUE, AND NO CHILDREN. Every
// reader that asks whether a session is a child still answers there, as the
// main seat did before the others asked the queue: nothing is a child.
func TestReadOnlyProjectionWithoutWorkQueueHasNoChildren(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"ws_active", "ws_delivered"} {
		if err := s.StartWorkSession(t.Context(), id, SubagentDescription("old words")); err != nil {
			t.Fatal(err)
		}
	}
	focus := "the old plan"
	if err := s.UpdateWorkPlan(t.Context(), "ws_delivered", &focus, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.DeliverWorkSession(t.Context(), "ws_delivered", "served: done", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`DROP TABLE work_queue`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if ws, err := ro.ActiveWorkSession(); err != nil || ws == nil || ws.ID != "ws_active" {
		t.Fatalf("main seat = %+v, %v", ws, err)
	}
	if ws, ok, err := ro.RecentPlan(); err != nil || !ok || ws.ID != "ws_delivered" {
		t.Fatalf("standing plan = %+v %t, %v", ws, ok, err)
	}
	for name, read := range map[string]func() ([]WorkSession, error){
		"live":        ro.LiveSubagentSessions,
		"unharvested": func() ([]WorkSession, error) { return ro.UnharvestedDeliveries(8) },
		"delivered":   func() ([]WorkSession, error) { return ro.RecentDeliveredSubagents(8) },
	} {
		if list, err := read(); err != nil || len(list) != 0 {
			t.Errorf("%s children = %+v, %v; want none and no error", name, list, err)
		}
	}
}
