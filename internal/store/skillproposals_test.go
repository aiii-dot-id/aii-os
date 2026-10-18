package store

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSkillProposalGroundsItsCitations(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.StartWorkSession(t.Context(), "ws_real1234", "the trajectory that taught it"); err != nil {
		t.Fatal(err)
	}

	// A phantom citation is refused with the phantom named.
	if _, err := s.ProposeSkill("yield early", "delta text", "learned in ws_real1234 and ws_deadbeef99"); err == nil {
		t.Fatal("a proposal citing a phantom session was accepted")
	} else if !strings.Contains(err.Error(), "ws_deadbeef99") {
		t.Fatalf("the refusal does not name the phantom: %v", err)
	}

	// Grounded evidence records, and reads back as written.
	id, err := s.ProposeSkill("yield early", "delta text", "learned in ws_real1234")
	if err != nil {
		t.Fatal(err)
	}
	props, err := s.ListSkillProposals(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(props) != 1 || props[0].ID != id || props[0].Title != "yield early" || props[0].Delta != "delta text" || props[0].Evidence != "learned in ws_real1234" {
		t.Fatalf("proposal row = %+v", props)
	}

	// Empty evidence is an opinion, not a proposal.
	if _, err := s.ProposeSkill("t", "d", ""); err == nil {
		t.Fatal("a proposal without evidence was accepted")
	}
}

func TestTheStoreNeverChangesAProposal(t *testing.T) {
	writeRe := regexp.MustCompile(`(?i)\b(INSERT(?: OR [A-Z]+)? INTO|UPDATE|DELETE FROM|REPLACE INTO)\s+` + "`?" + `skill_proposals\b`)
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var writes []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range writeRe.FindAllStringSubmatch(string(src), -1) {
			writes = append(writes, name+": "+strings.ToUpper(m[1]))
		}
	}
	if got := strings.Join(writes, "; "); got != "skillproposals.go: INSERT INTO" {
		t.Fatalf("the store's writes to skill_proposals = %q, want ProposeSkill's INSERT alone", got)
	}
}

const legacySkillProposalsSchema = `CREATE TABLE IF NOT EXISTS skill_proposals (
    -- provenance: ephemeral
    id         TEXT PRIMARY KEY,
    ts_ms      INTEGER NOT NULL,
    title      TEXT NOT NULL,
    delta      TEXT NOT NULL,
    evidence   TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'proposed' CHECK (status IN ('proposed','promoted','rejected')),
    decided_ms INTEGER NOT NULL DEFAULT 0,
    verified   TEXT NOT NULL DEFAULT 'none'
) STRICT`

func TestAnOlderStoresProposalsSurviveTheRetiredStatusFamily(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aii.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{`DROP TABLE skill_proposals`, legacySkillProposalsSchema} {
		if _, err := s.DB().Exec(stmt); err != nil {
			t.Fatalf("aging the store: %v", err)
		}
	}
	type row struct {
		id    string
		ts    int64
		title string
	}
	old := []row{{"sp_one", 1000, "yield early"}, {"sp_two", 2000, "batch organ reads"}}
	for _, r := range old {
		// The one write any release made to the table.
		if _, err := s.DB().Exec(`INSERT INTO skill_proposals (id, ts_ms, title, delta, evidence) VALUES (?, ?, ?, ?, ?)`,
			r.id, r.ts, r.title, "delta of "+r.id, "evidence of "+r.id); err != nil {
			t.Fatal(err)
		}
	}
	var defaults int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM skill_proposals WHERE status='proposed' AND decided_ms=0 AND verified='none'`).Scan(&defaults); err != nil || defaults != len(old) {
		t.Fatalf("rows holding the defaults = %d (%v), want every row", defaults, err)
	}

	// Without the retirement the engine refuses, and nothing moves.
	raw, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	current := string(raw)
	undeclared := current
	for _, c := range []string{"status", "decided_ms", "verified"} {
		undeclared = strings.Replace(undeclared, "    -- retired-column: "+c+"\n", "", 1)
	}
	if strings.Contains(undeclared, "retired-column") {
		t.Fatal("schema.sql no longer retires exactly the status family; the control would test nothing")
	}
	_, err = s.reconcileSchema(undeclared)
	var shape *ShapeError
	if !errors.As(err, &shape) || !strings.Contains(err.Error(), "refusing to rebuild skill_proposals: column(s) decided_ms, status, verified hold data") {
		t.Fatalf("without the declaration the populated columns must be refused by name: %v", err)
	}
	if cols, err := s.liveColumns("skill_proposals"); err != nil || len(cols) != 8 {
		t.Fatalf("a refused rebuild changed the table: %v %v", cols, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// The newer binary opens it.
	s2, err := New(path)
	if err != nil {
		t.Fatalf("an older store did not survive the retirement: %v", err)
	}
	defer s2.Close()
	cols, err := s2.liveColumns("skill_proposals")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(sortedKeys(cols), " "); got != "delta evidence id title ts_ms" {
		t.Fatalf("columns after the upgrade = %q, want the proposal's five", got)
	}
	props, err := s2.ListSkillProposals(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(props) != len(old) {
		t.Fatalf("proposals after the upgrade = %+v, want %d", props, len(old))
	}
	for i, p := range props {
		want := old[len(old)-1-i] // newest first
		if p.ID != want.id || p.TsMs != want.ts || p.Title != want.title || p.Delta != "delta of "+want.id || p.Evidence != "evidence of "+want.id {
			t.Fatalf("proposal %d changed in the upgrade: %+v, want %+v", i, p, want)
		}
	}
	mustConverge(t, s2, current)
}
