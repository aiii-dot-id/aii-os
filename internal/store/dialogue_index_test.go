package store

import (
	"sort"
	"strings"
	"testing"
)

// seedDialogue writes a small conversation: the dialogue, a system row of
// the dialogue's own kind (the window never carries it; the counts must
// agree on that), an interruption notice about a reply, and a tool call.
func seedDialogue(t *testing.T, s *Store) {
	t.Helper()
	for _, stmt := range []string{
		`INSERT INTO conversations(id,session_id,role,content,turn_seq,created_at,kind) VALUES
		   ('c1','s','operator','hello',1,'2026-10-06T00:00:01Z','message'),
		   ('c2','s','resident','hi',2,'2026-10-06T00:00:02Z','message'),
		   ('c3','s','system','a system row of the dialogue''s kind',3,'2026-10-06T00:00:03Z','message'),
		   ('c4','s','operator','and then',4,'2026-10-06T00:00:04Z','legacy')`,
		`INSERT INTO conversations(id,session_id,role,content,turn_seq,created_at,kind,related_id,details) VALUES
		   ('n1','s','system','[the reply above is incomplete]',5,'2026-10-06T00:00:05Z','notice','c2','{"reason_code":"interrupted"}')`,
		`INSERT INTO conversations(id,session_id,role,content,turn_seq,created_at,kind,turn_id,source_kind,source_id,recorded_at,details) VALUES
		   ('t1','s','system','→ read()',6,'2026-10-06T00:00:06Z','tool_call','turn-1','tool','call-1','2026-10-06T00:00:06Z','{}')`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

// plan is the planner's lines for one statement.
func plan(t *testing.T, s *Store, query string, args ...any) string {
	t.Helper()
	rows, err := s.db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("explain %s: %v", query, err)
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
	return strings.Join(out, "\n")
}

const dialogueIndex = "idx_conversations_dialogue"

// theTurnsReads are the statements ConversationWindow and GetStats run,
// as they run them: the two counts, the page, and the two notice lookups.
func theTurnsReads(s *Store) (count, stats, page, interruptions, unanswered string) {
	count = `SELECT COUNT(*) FROM conversations WHERE role != 'system' AND ` + s.dialogueSelection()
	stats = `SELECT COUNT(*) FROM conversations WHERE role != 'system' AND ` + s.dialogueSelection()
	page = `SELECT id, session_id, role, content, turn_seq, created_at, turn_id, details
		 FROM conversations WHERE role != 'system' AND ` + s.dialogueSelection() + ` ORDER BY turn_seq DESC LIMIT 20`
	interruptions = `SELECT related_id, content FROM conversations
		 WHERE role='system' AND kind='notice' AND source_id IS NULL
		   AND json_extract(details,'$.reason_code')='interrupted'
		   AND related_id IN ('c2')
		 ORDER BY turn_seq`
	unanswered = `SELECT n.related_id, n.content FROM conversations n
		 WHERE n.role='system' AND n.kind='notice' AND n.source_id IS NULL
		   AND json_extract(n.details,'$.reason_code')='unanswered'
		   AND n.related_id IN ('c2')
		   AND COALESCE((SELECT c.turn_id FROM conversations c
		       WHERE c.turn_seq > n.turn_seq AND c.role != 'system'
		         AND c.kind IN ('message','legacy') AND c.source_id IS NULL
		         AND c.turn_id != '' AND c.turn_id != n.turn_id
		       ORDER BY c.turn_seq LIMIT 1), '') = ''
		 ORDER BY n.turn_seq`
	return
}

func TestTheDialogueIndexServesTheTurnsReadsAndNoOther(t *testing.T) {
	s := testStore(t)
	seedDialogue(t, s)
	count, stats, page, interruptions, unanswered := theTurnsReads(s)
	for name, q := range map[string]string{"the turn's count": count, "the status count": stats, "the page": page} {
		if p := plan(t, s, q); !strings.Contains(p, dialogueIndex) {
			t.Errorf("%s does not read %s:\n%s", name, dialogueIndex, p)
		}
	}
	// The notice lookups ask for the system's rows, which the index does
	// not hold: the planner must not take it for them. The unanswered
	// lookup's inner read of the dialogue may.
	if p := plan(t, s, interruptions); strings.Contains(p, dialogueIndex) {
		t.Errorf("the interruption lookup took %s, which cannot hold a system row:\n%s", dialogueIndex, p)
	}
	for _, line := range strings.Split(plan(t, s, unanswered), "\n") {
		if strings.Contains(line, " n ") && strings.Contains(line, dialogueIndex) {
			t.Errorf("the unanswered lookup's outer read took %s, which cannot hold a system row:\n%s", dialogueIndex, line)
		}
	}
}

// THE COUNTS AGREE. The turn's count never held the system's rows; the
// status count did, so a system row of the dialogue's kind made the two
// disagree by one. One definition now.
func TestTheStatusCountIsTheTurnsCount(t *testing.T) {
	s := testStore(t)
	seedDialogue(t, s)
	stats, err := s.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	turns, total, err := s.ConversationWindow(20, "")
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.ConversationTurnCount()
	if err != nil {
		t.Fatal(err)
	}
	if stats.ConversationCount != 3 || total != 3 || n != 3 || len(turns) != 3 {
		t.Fatalf("status count %d, window total %d (%d turns), turn count %d; want 3 everywhere: the three dialogue rows, not the system's",
			stats.ConversationCount, total, len(turns), n)
	}
}

// A STORE THAT PREDATES THE INDEX GETS IT AT ITS NEXT OPEN, built by the
// reconciler from the declaration, with its rows as they were: no step by
// hand on any identity's store, and the reads it serves answer the same.
func TestReconcileBuildsTheDialogueIndexOnAStoreThatLacksIt(t *testing.T) {
	raw, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	current := string(raw)
	decl := declaredIndexStatements(current)[dialogueIndex].SQL
	if decl == "" || !strings.Contains(current, decl) {
		t.Fatalf("the schema does not declare %s as one statement the reconciler reads", dialogueIndex)
	}
	older := strings.Replace(current, decl, "", 1)
	if strings.Contains(older, dialogueIndex) {
		t.Fatal("the older schema still names the index")
	}
	s, _ := driftDB(t, older)
	defer s.db.Close()
	seedDialogue(t, s)
	indexed := func() int {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, dialogueIndex).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if indexed() != 0 {
		t.Fatal("the older store already holds the index")
	}
	count, _, page, _, _ := theTurnsReads(s)
	if p := plan(t, s, count); strings.Contains(p, dialogueIndex) {
		t.Fatalf("the older store's count reads an index it does not have:\n%s", p)
	}
	before, err := s.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	rep, err := s.reconcileSchema(current)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !strings.Contains(strings.Join(rep.Indexes, "\n"), dialogueIndex+" (created)") {
		t.Fatalf("the reconciler did not report the index created: %v", rep.Indexes)
	}
	if indexed() != 1 {
		t.Fatal("the index is not there after the reconcile")
	}
	after, err := s.GetStats()
	if err != nil {
		t.Fatal(err)
	}
	if after.ConversationCount != before.ConversationCount || after.ConversationCount != 3 {
		t.Fatalf("the count changed with the index: %d before, %d after", before.ConversationCount, after.ConversationCount)
	}
	for name, q := range map[string]string{"the count": count, "the page": page} {
		if p := plan(t, s, q); !strings.Contains(p, dialogueIndex) {
			t.Errorf("after the reconcile %s does not read %s:\n%s", name, dialogueIndex, p)
		}
	}
	turns, total, err := s.ConversationWindow(20, "")
	if err != nil || total != 3 || len(turns) != 3 {
		t.Fatalf("the window after the reconcile: total %d, %d turns, err %v", total, len(turns), err)
	}
	ids := []string{turns[0].ID, turns[1].ID, turns[2].ID}
	sort.Strings(ids)
	if strings.Join(ids, ",") != "c1,c2,c4" {
		t.Fatalf("the window after the reconcile holds %v, want the three dialogue rows", ids)
	}
	// Reconciled again, nothing moves.
	again, err := s.reconcileSchema(current)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Indexes) != 0 {
		t.Fatalf("a second reconcile touched indexes: %v", again.Indexes)
	}
}
